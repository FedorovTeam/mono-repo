package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const knowledgeRoot = "knowledge/docs"

// ---------------- GraphQL types ----------------

type uploadResponse struct {
	Data struct {
		UploadKnowledgeDocument struct {
			UploadURL       string `json:"uploadUrl"`
			UploadMethod    string `json:"uploadMethod"`
			RequiredHeaders []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"requiredHeaders"`
			Document struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Path   string `json:"path"`
			} `json:"document"`
		} `json:"uploadKnowledgeDocument"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type uploadRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

// ---------------- Authentik token fetcher ----------------

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"` // seconds
	IDToken     string `json:"id_token"`
}

type tokenProvider interface {
	Token(ctx context.Context) (string, error)
	ForceRefresh(ctx context.Context) (string, error)
}

type AuthentikTokenSource struct {
	Client      *http.Client
	TokenURL    string
	ClientID    string
	Username    string
	Password    string
	Scope       string
	Skew        time.Duration
	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

func (ts *AuthentikTokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.accessToken != "" && time.Until(ts.expiresAt) > ts.Skew {
		return "Bearer " + ts.accessToken, nil
	}
	if err := ts.refreshLocked(ctx); err != nil {
		return "", err
	}
	return "Bearer " + ts.accessToken, nil
}

func (ts *AuthentikTokenSource) ForceRefresh(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.accessToken = ""
	ts.expiresAt = time.Time{}
	if err := ts.refreshLocked(ctx); err != nil {
		return "", err
	}
	return "Bearer " + ts.accessToken, nil
}

func (ts *AuthentikTokenSource) refreshLocked(ctx context.Context) error {
	if ts.Client == nil {
		return errors.New("token source: http client is nil")
	}
	if ts.TokenURL == "" || ts.ClientID == "" || ts.Username == "" || ts.Password == "" {
		return errors.New("token source: TokenURL/clientID/username/password must be set")
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", ts.ClientID)
	form.Set("username", ts.Username)
	form.Set("password", ts.Password)
	if strings.TrimSpace(ts.Scope) != "" {
		form.Set("scope", ts.Scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := ts.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(raw))
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return fmt.Errorf("decode token response: %w (%s)", err, string(raw))
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("token response: empty access_token (%s)", string(raw))
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 60
	}

	ts.accessToken = tr.AccessToken
	ts.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	fmt.Printf("  🔑 token refreshed, expires in %ds (at %s)\n", tr.ExpiresIn, ts.expiresAt.Format(time.RFC3339))
	return nil
}

type staticTokenSource struct{ token string }

func (s *staticTokenSource) Token(ctx context.Context) (string, error)        { return s.token, nil }
func (s *staticTokenSource) ForceRefresh(ctx context.Context) (string, error) { return s.token, nil }

// ---------------- Log (resume) ----------------

type uploadLogEvent struct {
	TS           string `json:"ts"`
	Event        string `json:"event"`
	Root         string `json:"root,omitempty"`
	FilePath     string `json:"file_path,omitempty"`   // abs local path
	UploadPath   string `json:"upload_path,omitempty"` // markdown destination path in repo
	Size         int64  `json:"size,omitempty"`
	DocID        string `json:"doc_id,omitempty"`
	Status       string `json:"status,omitempty"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	Error        string `json:"error,omitempty"`
	OriginalName string `json:"original_name,omitempty"` // original filename (pdf/docx/...)
	StoredName   string `json:"stored_name,omitempty"`   // markdown target name (*.md)
}

type uploadLogger struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

func newUploadLogger(path string) (*uploadLogger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &uploadLogger{
		f: f,
		w: bufio.NewWriterSize(f, 64*1024),
	}, nil
}

func (l *uploadLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		_ = l.w.Flush()
	}
	if l.f != nil {
		return l.f.Close()
	}
	return nil
}

func (l *uploadLogger) Log(ev uploadLogEvent) {
	ev.TS = time.Now().Format(time.RFC3339)

	b, err := json.Marshal(ev)
	if err != nil {
		fmt.Fprintln(os.Stderr, "log marshal error:", err)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	_, _ = l.w.Write(b)
	_, _ = l.w.WriteString("\n")
	_ = l.w.Flush()
}

func loadCompletedFromLog(logPath string) (map[string]bool, error) {
	completed := make(map[string]bool)

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return completed, nil
		}
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 4*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev uploadLogEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Event == "success" && ev.FilePath != "" {
			completed[filepath.Clean(ev.FilePath)] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return completed, nil
}

// ---------------- Name mapping: original upload, markdown destination naming ----------------

func shortSHA1(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

// markdownTargetName returns deterministic markdown filename for the CONVERTED artifact.
// IMPORTANT: This is NOT the original filename. Original stays original.
func markdownTargetName(originalPath string) string {
	base := filepath.Base(originalPath)
	nameNoExt := strings.TrimSuffix(base, filepath.Ext(base))
	if nameNoExt == "" {
		nameNoExt = base
	}

	abs := originalPath
	if ap, err := filepath.Abs(originalPath); err == nil {
		abs = ap
	}
	sfx := shortSHA1(filepath.Clean(abs))
	return fmt.Sprintf("%s__%s.md", nameNoExt, sfx)
}

// ---------------- Main ----------------

func main() {
	endpoint := flag.String("endpoint", "https://gateway.tech.fedorov-team.tech/query", "GraphQL endpoint gateway-service (/query)")
	apiKey := flag.String("apiKey", "secret", "X-API-Key, если включено")

	repository := flag.String("repository", "docs", "Целевой репозиторий")
	branch := flag.String("branch", "main", "Целевая ветка")
	root := flag.String("root", ".", "Корневая папка с файлами для загрузки")
	delay := flag.Duration("delay", 100*time.Millisecond, "Пауза между загрузками")
	dryRun := flag.Bool("dryRun", false, "Только вывести план без загрузки")

	logPath := flag.String("log", "upload.log", "Путь к файлу журнала (JSONL). Используется для resume.")
	resume := flag.Bool("resume", true, "Пропускать файлы, которые уже успешно загружены (по upload.log)")

	// Authentik
	tokenURL := flag.String("authTokenUrl", "https://auth.tech.fedorov-team.tech/application/o/token/", "Authentik token endpoint")
	clientID := flag.String("authClientId", "L6AmiswYzea2gAs5aGfzhLP9jlapG42ppPhtutoF", "Authentik OAuth2 client_id")
	authUser := flag.String("authUsername", "uploader", "Authentik service account username")
	authPass := flag.String("authPassword", "fgqjPDaqjB5F0epj3YVe90xlzXx4IB1ARExVHS8wg2Rmiil6kX57S1VFPQB2", "Authentik service account app-password/token")
	authScope := flag.String("authScope", "openid profile email", "Scope for token request")
	refreshSkew := flag.Duration("authRefreshSkew", 30*time.Second, "Обновлять токен заранее за N секунд до exp")

	staticToken := flag.String("token", "", "Если задан — использовать этот Bearer-токен вместо authentik авто-режима")

	flag.Parse()

	if *endpoint == "" {
		exitWithError(errors.New("нужен адрес GraphQL endpoint (флаг -endpoint)"))
	}

	ctx := context.Background()
	client := &http.Client{Timeout: 60 * time.Second}

	logger, err := newUploadLogger(*logPath)
	if err != nil {
		exitWithError(fmt.Errorf("open log: %w", err))
	}
	defer logger.Close()

	completed := map[string]bool{}
	if *resume && !*dryRun {
		completed, err = loadCompletedFromLog(*logPath)
		if err != nil {
			exitWithError(fmt.Errorf("read log for resume: %w", err))
		}
		fmt.Printf("📒 resume enabled: already completed files: %d\n", len(completed))
	}

	var tp tokenProvider
	if strings.TrimSpace(*staticToken) != "" {
		tp = &staticTokenSource{token: *staticToken}
		fmt.Println("🔐 Using static token from -token")
	} else {
		if *clientID == "" || *authUser == "" || *authPass == "" {
			exitWithError(errors.New("нужно либо -token, либо authentik флаги: -authClientId -authUsername -authPassword"))
		}
		tp = &AuthentikTokenSource{
			Client:   client,
			TokenURL: *tokenURL,
			ClientID: *clientID,
			Username: *authUser,
			Password: *authPass,
			Scope:    *authScope,
			Skew:     *refreshSkew,
		}
		fmt.Println("🔐 Using authentik client_credentials token source")
	}

	files, err := collectFiles(*root)
	if err != nil {
		exitWithError(err)
	}
	sort.Strings(files)

	fmt.Printf("Найдено файлов: %d\n", len(files))
	logger.Log(uploadLogEvent{Event: "run_start", Root: *root})

	for i, p := range files {
		absPath := p
		if ap, err := filepath.Abs(p); err == nil {
			absPath = ap
		}
		absPath = filepath.Clean(absPath)

		if *resume && !*dryRun && completed[absPath] {
			fmt.Printf("[%d/%d] ⏭ skip (already uploaded): %s\n", i+1, len(files), absPath)
			continue
		}

		info, err := os.Stat(p)
		if err != nil {
			logger.Log(uploadLogEvent{Event: "error", FilePath: absPath, Error: err.Error()})
			exitWithError(fmt.Errorf("не удалось прочитать %s: %w", p, err))
		}

		originalName := filepath.Base(p)            // pdf/docx/... (ORIGINAL!)
		mdName := markdownTargetName(p)             // *.md (for converted artifact)
		uploadPath := resolvePath(*root, p, mdName) // path ends with *.md

		fmt.Printf("[%d/%d] upload original: %s -> markdown path: %s\n", i+1, len(files), p, uploadPath)

		logger.Log(uploadLogEvent{
			Event:        "start",
			FilePath:     absPath,
			UploadPath:   uploadPath,
			Size:         info.Size(),
			OriginalName: originalName,
			StoredName:   mdName,
		})

		if *dryRun {
			continue
		}

		docID, docStatus, err := uploadDocument(ctx, client, *endpoint, tp, *apiKey, *repository, *branch, uploadPath, originalName, p, info.Size())
		if err != nil {
			logger.Log(uploadLogEvent{
				Event:        "error",
				FilePath:     absPath,
				UploadPath:   uploadPath,
				Size:         info.Size(),
				Error:        err.Error(),
				OriginalName: originalName,
				StoredName:   mdName,
			})
			exitWithError(fmt.Errorf("ошибка загрузки %s: %w", p, err))
		}

		logger.Log(uploadLogEvent{
			Event:        "success",
			FilePath:     absPath,
			UploadPath:   uploadPath,
			Size:         info.Size(),
			DocID:        docID,
			Status:       docStatus,
			OriginalName: originalName,
			StoredName:   mdName,
		})

		completed[absPath] = true
		time.Sleep(*delay)
	}

	logger.Log(uploadLogEvent{Event: "run_finish"})
}

// ---------------- File collection/path logic ----------------

func collectFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(strings.ToLower(name), "~") {
			return nil
		}
		if name == ".DS_Store" || name == "go.mod" || name == "upload.log" || name == "upload.go" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func resolvePath(root, filePath, mdName string) string {
	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		rel = filepath.Base(filePath)
	}

	sessionNumber := extractLeadingDigits(filepath.Dir(rel))
	if sessionNumber == "" {
		sessionNumber = "00"
	}
	sessionNumber = pad(sessionNumber, 2)

	// classification based on ORIGINAL filename (pdf/docx/...)
	docType, _ := classifyDocument(filepath.Base(filePath))

	switch docType {
	case "voting":
		return filepath.ToSlash(fmt.Sprintf("%s/plenary/%s/votings/%s", knowledgeRoot, sessionNumber, mdName))
	default:
		return filepath.ToSlash(fmt.Sprintf("%s/plenary/%s/transcripts/%s", knowledgeRoot, sessionNumber, mdName))
	}
}

func extractLeadingDigits(dir string) string {
	base := filepath.Base(dir)
	return regexp.MustCompile(`^(\d+)`).FindString(base)
}

func classifyDocument(name string) (string, string) {
	lower := strings.ToLower(name)
	digits := regexp.MustCompile(`(\d+)`).FindString(name)

	if strings.Contains(lower, "голос") || strings.Contains(lower, "voting") {
		if digits != "" {
			return "voting", digits
		}
		return "voting", "1"
	}

	if strings.Contains(lower, "биографии") {
		if digits != "" {
			return "voting", digits
		}
		return "voting", "1"
	}

	if digits != "" {
		return "transcript", digits
	}
	return "transcript", "1"
}

// ---------------- Upload with auto-refresh + retry on 401/403 ----------------

// IMPORTANT: fileName is ORIGINAL filename (pdf/docx/...)
// path is MARKDOWN destination (*.md)
func uploadDocument(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	tp tokenProvider,
	apiKey, repository, branch, markdownPath, originalFileName, filePath string,
	size int64,
) (string, string, error) {
	mutation := "" +
		"mutation($input: UploadKnowledgeDocumentInput!){" +
		" uploadKnowledgeDocument(input:$input){" +
		" uploadUrl uploadMethod requiredHeaders{ name value } document{ id status path } } }"

	payload := uploadRequest{
		Query: mutation,
		Variables: map[string]interface{}{
			"input": map[string]interface{}{
				"fileName":      originalFileName, // ORIGINAL (pdf/docx/...)
				"fileSizeBytes": size,             // size of ORIGINAL file
				"repository":    repository,
				"branch":        branch,
				"path":          markdownPath, // where markdown will be saved/published (*.md)
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}

	gqlResp, status, raw, err := doGraphQL(ctx, client, endpoint, tp, apiKey, body, false)
	if err != nil {
		return "", "", err
	}

	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		fmt.Printf("  🔁 got %d, refreshing token and retrying...\n", status)
		gqlResp, status, raw, err = doGraphQL(ctx, client, endpoint, tp, apiKey, body, true)
		if err != nil {
			return "", "", err
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return "", "", fmt.Errorf("GraphQL unauthorized after refresh (%d): %s", status, string(raw))
		}
	}

	if status >= 300 {
		return "", "", fmt.Errorf("GraphQL ответ %d: %s", status, string(raw))
	}
	if len(gqlResp.Errors) > 0 {
		return "", "", fmt.Errorf("GraphQL ошибка: %s", gqlResp.Errors[0].Message)
	}

	payloadData := gqlResp.Data.UploadKnowledgeDocument
	if payloadData.UploadURL == "" {
		return "", "", errors.New("пустой uploadUrl в ответе")
	}

	requiredHeaders := make(http.Header)
	for _, h := range payloadData.RequiredHeaders {
		requiredHeaders.Set(h.Name, h.Value)
	}

	// Upload ORIGINAL bytes
	file, err := os.Open(filePath)
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	putReq, err := http.NewRequestWithContext(ctx, payloadData.UploadMethod, payloadData.UploadURL, file)
	if err != nil {
		return "", "", err
	}
	putReq.ContentLength = size
	for name, values := range requiredHeaders {
		for _, v := range values {
			putReq.Header.Add(name, v)
		}
	}

	putResp, err := client.Do(putReq)
	if err != nil {
		return "", "", err
	}
	defer putResp.Body.Close()

	if putResp.StatusCode >= 300 {
		data, _ := io.ReadAll(putResp.Body)
		return "", "", fmt.Errorf("загрузка завершилась статусом %d: %s", putResp.StatusCode, string(data))
	}

	fmt.Printf("  ✔ Uploaded original %s; markdown path: %s; (id=%s, status=%s)\n",
		originalFileName, payloadData.Document.Path, payloadData.Document.ID, payloadData.Document.Status)

	return payloadData.Document.ID, payloadData.Document.Status, nil
}

func doGraphQL(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	tp tokenProvider,
	apiKey string,
	body []byte,
	forceRefresh bool,
) (uploadResponse, int, []byte, error) {
	var token string
	var err error
	if forceRefresh {
		token, err = tp.ForceRefresh(ctx)
	} else {
		token, err = tp.Token(ctx)
	}
	if err != nil {
		return uploadResponse{}, 0, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return uploadResponse{}, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return uploadResponse{}, 0, nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var gqlResp uploadResponse
	_ = json.Unmarshal(raw, &gqlResp)

	return gqlResp, resp.StatusCode, raw, nil
}

// ---------------- Utils ----------------

func pad(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return fmt.Sprintf("%0*s", width, value)
}

func exitWithError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
