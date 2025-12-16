package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	BaseURL         = "https://iltumen.ru/api"
	OutRoot         = "./dump"
	UserAgent       = "Mozilla/5.0 (compatible; iltumen-downloader/1.0)"
	RequestTimeout  = 90 * time.Second
	DownloadTimeout = 180 * time.Second
	RateDelay       = 80 * time.Millisecond
	DefaultAttempts = 5
)

var (
	verboseFlag     = flag.Bool("verbose", false, "enable verbose logging")
	attemptFlag     = flag.Int("retries", DefaultAttempts, "download attempts for temporary errors")
	retryBackoffSeq = []time.Duration{
		500 * time.Millisecond,
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
	}
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// ---------- types ----------

type ContainerInfo struct {
	Name string `json:"name"`
}

type PlenarySession struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Date           string `json:"date"` // ISO string or empty
	Transcript     string `json:"transcript"`
	Transcript2    string `json:"transcript2"`
	Protocol       string `json:"protocol"`
	AgendaFilename string `json:"agendaFilename"`
	Newsletter     string `json:"newsletter"`
}

type FileResult struct {
	SessionID   int64  `json:"sessionId"`
	SessionName string `json:"sessionName"`
	Label       string `json:"label"`
	Source      string `json:"source"`
	OutputPath  string `json:"outputPath,omitempty"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
}

type DownloadReport struct {
	GeneratedAt time.Time    `json:"generatedAt"`
	Successes   int          `json:"successes"`
	Failures    int          `json:"failures"`
	Results     []FileResult `json:"results"`
}

// ---------- helpers ----------
var invalidFS = regexp.MustCompile(`[\\/:*?"<>|]+`)
var spaces = regexp.MustCompile(`\s+`)

func safeName(s string) string {
	s = strings.TrimSpace(s)

	// 1) гарантируем валидный UTF-8 (важно для macOS)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "_")
	} else {
		// на всякий случай тоже прогоняем (иногда уже есть "битые" куски)
		s = strings.ToValidUTF8(s, "_")
	}

	// 2) чистим запрещённые символы ФС
	s = invalidFS.ReplaceAllString(s, "_")
	s = spaces.ReplaceAllString(s, " ")

	// 3) ограничим длину (лучше по рунам)
	const maxRunes = 180
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes])
	}

	if s == "" {
		return "untitled"
	}
	return s
}

func mkdirp(p string) error {
	return os.MkdirAll(p, 0o755)
}

func fileExistsNonEmpty(p string) bool {
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	return !st.IsDir() && st.Size() > 0
}

func pickDatePrefix(iso string) string {
	// iso could be "2025-12-15T05:49:16.928Z" or empty
	if len(iso) >= 10 {
		return iso[:10]
	}
	return ""
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout: RequestTimeout,
	}
}

func doJSON[T any](ctx context.Context, client *http.Client, method, u string, out *T) error {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	dec := json.NewDecoder(resp.Body)
	return dec.Decode(out)
}

type httpStatusError struct {
	Code int
	Body string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("download http %d: %s", e.Code, e.Body)
}

func downloadToFile(ctx context.Context, client *http.Client, u, outPath string) error {
	// separate timeout for download
	dlClient := &http.Client{Timeout: DownloadTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)

	resp, err := dlClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		// consume small body for debug
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return &httpStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}

	if err := mkdirp(filepath.Dir(outPath)); err != nil {
		return err
	}

	tmp := outPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = f.Close()

	// atomic replace
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeReport(report DownloadReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(OutRoot, "report.json"), data, 0o644)
}

func pickBackoff(backoff []time.Duration, attempt int) time.Duration {
	if len(backoff) == 0 {
		return 0
	}
	idx := attempt - 1
	if idx >= len(backoff) {
		idx = len(backoff) - 1
	}
	return backoff[idx]
}

func jitterDelay(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	jitter := time.Duration(rng.Int63n(int64(base / 2)))
	return base + jitter
}

func isTemporaryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() || netErr.Temporary() {
			return true
		}
	}
	if errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.Code == http.StatusTooManyRequests || (statusErr.Code >= 500 && statusErr.Code < 600)
	}
	return false
}

func withRetry(ctx context.Context, attempts int, backoff []time.Duration, fn func(int) error) error {
	if attempts < 1 {
		attempts = 1
	}
	if len(backoff) == 0 {
		backoff = retryBackoffSeq
	}

	var lastErr error
	for i := 1; i <= attempts; i++ {
		err := fn(i)
		if err == nil {
			return nil
		}

		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isTemporaryError(err) || i == attempts {
			return err
		}

		delay := jitterDelay(pickBackoff(backoff, i))
		if *verboseFlag {
			fmt.Printf("    temporary error, retrying in %s (attempt %d/%d): %v\n", delay, i, attempts, err)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return lastErr
}

func containerDownloadURL(container, filename string) string {
	// /Containers/:container/download/:file
	// IMPORTANT: each path segment must be escaped separately
	return fmt.Sprintf("%s/Containers/%s/download/%s",
		BaseURL,
		url.PathEscape(container),
		url.PathEscape(filename),
	)
}

// some APIs store "path/to/file.pdf" in the field; container endpoint expects just ":file".
// We'll try both: basename and original.
func candidateFilenames(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	base := filepath.Base(raw)
	if base == raw {
		return []string{raw}
	}
	// Try basename first (more likely), then full raw.
	return []string{base, raw}
}

func reorderContainers(containers []string, preferred []string) []string {
	seen := map[string]bool{}
	var out []string

	for _, p := range preferred {
		for _, c := range containers {
			if c == p && !seen[c] {
				out = append(out, c)
				seen[c] = true
			}
		}
	}
	for _, c := range containers {
		if !seen[c] {
			out = append(out, c)
			seen[c] = true
		}
	}
	return out
}

// ---------- core logic ----------

func fetchContainers(ctx context.Context, client *http.Client) ([]string, error) {
	var items []ContainerInfo
	if err := doJSON(ctx, client, http.MethodGet, BaseURL+"/containers", &items); err != nil {
		return nil, err
	}
	var names []string
	for _, it := range items {
		if strings.TrimSpace(it.Name) != "" {
			names = append(names, it.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func fetchPlenarySessions(ctx context.Context, client *http.Client, limit int) ([]PlenarySession, error) {
	filter := fmt.Sprintf(`{"order":"date DESC","limit":%d}`, limit)
	u := BaseURL + "/PlenarySessions?filter=" + url.QueryEscape(filter)
	var items []PlenarySession
	if err := doJSON(ctx, client, http.MethodGet, u, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func downloadKnownOrSearch(
	ctx context.Context,
	client *http.Client,
	rawFilename string,
	outDir string,
	outName string,
	containers []string,
	preferred []string,
) (string, error) {
	if strings.TrimSpace(rawFilename) == "" {
		return "", nil
	}

	targetPath := filepath.Join(outDir, safeName(outName))
	if fileExistsNonEmpty(targetPath) {
		return targetPath, nil
	}

	order := reorderContainers(containers, preferred)
	cands := candidateFilenames(rawFilename)
	attempts := *attemptFlag
	if attempts <= 0 {
		attempts = DefaultAttempts
	}

	for _, cand := range cands {
		for _, c := range order {
			u := containerDownloadURL(c, cand)
			if *verboseFlag {
				fmt.Printf("    container=%s cand=%s url=%s\n", c, cand, u)
			}
			err := withRetry(ctx, attempts, retryBackoffSeq, func(retryNum int) error {
				if *verboseFlag {
					fmt.Printf("    [%s] attempt %d/%d for %s\n", c, retryNum, attempts, cand)
				}
				dlErr := downloadToFile(ctx, client, u, targetPath)
				if dlErr != nil && *verboseFlag {
					var statusErr *httpStatusError
					if errors.As(dlErr, &statusErr) {
						fmt.Printf("    [%s] attempt %d failed: http %d (%s)\n", c, retryNum, statusErr.Code, statusErr.Body)
					} else {
						fmt.Printf("    [%s] attempt %d failed: %v\n", c, retryNum, dlErr)
					}
				}
				return dlErr
			})
			if err == nil {
				return targetPath, nil
			}

			// quietly continue on 404/500 etc — but keep a tiny delay
			time.Sleep(RateDelay)
		}
	}

	return "", fmt.Errorf("not found in any container: %q", rawFilename)
}

func main() {
	flag.Parse()
	ctx := context.Background()
	client := httpClient()

	fmt.Println("Fetching containers...")
	containers, err := fetchContainers(ctx, client)
	if err != nil {
		fmt.Fprintln(os.Stderr, "containers error:", err)
		os.Exit(1)
	}
	fmt.Printf("Containers: %d\n", len(containers))

	fmt.Println("Fetching plenary sessions...")
	sessions, err := fetchPlenarySessions(ctx, client, 10000)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plenary sessions error:", err)
		os.Exit(1)
	}
	fmt.Printf("PlenarySessions: %d\n", len(sessions))

	root := filepath.Join(OutRoot, "Пленарное заседание")
	if err := mkdirp(root); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir error:", err)
		os.Exit(1)
	}

	preferredContainers := []string{"docfiles", "attachments", "plenarysessions"}

	var results []FileResult
	successCount := 0
	failureCount := 0

	for i, ps := range sessions {
		datePrefix := pickDatePrefix(ps.Date)
		folderName := safeName(strings.TrimSpace(strings.Join([]string{datePrefix, ps.Name}, " ")))
		if folderName == "untitled" {
			folderName = fmt.Sprintf("PlenarySession_%d", ps.ID)
		}
		outDir := filepath.Join(root, folderName)
		if err := mkdirp(outDir); err != nil {
			fmt.Fprintln(os.Stderr, "mkdir error:", err)
			continue
		}

		fmt.Printf("[%d/%d] %s\n", i+1, len(sessions), outDir)

		type FileField struct {
			Label string
			Value string
		}

		fields := []FileField{
			{Label: "Стенограмма.pdf", Value: ps.Transcript},
			{Label: "Стенограмма_2_день.pdf", Value: ps.Transcript2},
			{Label: "Протокол.pdf", Value: ps.Protocol},
			{Label: "Повестка.pdf", Value: ps.AgendaFilename},
			{Label: "Информационный_бюллетень.pdf", Value: ps.Newsletter},
		}

		for _, f := range fields {
			if strings.TrimSpace(f.Value) == "" {
				continue
			}
			outPath, err := downloadKnownOrSearch(ctx, client, f.Value, outDir, f.Label, containers, preferredContainers)
			if err != nil {
				// не падаем — просто логируем
				fmt.Printf("  - FAIL %s (%s): %v\n", f.Label, f.Value, err)
				failureCount++
				results = append(results, FileResult{
					SessionID:   ps.ID,
					SessionName: ps.Name,
					Label:       f.Label,
					Source:      f.Value,
					Success:     false,
					Error:       err.Error(),
				})
			} else {
				fmt.Printf("  - OK   %s\n", f.Label)
				successCount++
				results = append(results, FileResult{
					SessionID:   ps.ID,
					SessionName: ps.Name,
					Label:       f.Label,
					Source:      f.Value,
					OutputPath:  outPath,
					Success:     true,
				})
			}
			time.Sleep(RateDelay)
		}
	}

	report := DownloadReport{
		GeneratedAt: time.Now().UTC(),
		Successes:   successCount,
		Failures:    failureCount,
		Results:     results,
	}

	fmt.Printf("Summary: success=%d failures=%d\n", successCount, failureCount)
	if failureCount > 0 {
		fmt.Println("Failed files:")
		for _, r := range results {
			if r.Success {
				continue
			}
			fmt.Printf("  - session %d (%s) %s (%s): %s\n", r.SessionID, r.SessionName, r.Label, r.Source, r.Error)
		}
	}

	if err := writeReport(report); err != nil {
		fmt.Fprintln(os.Stderr, "report error:", err)
	}

	fmt.Println("Done.")
}

// ---------- optional: add later for votes/questions ----------
// Здесь сознательно не реализовано скачивание “Протоколы голосования по вопросу X”,
// потому что на твоём фрагменте не видно однозначной связи “вопрос → файл”.
// Как только ты покажешь 1 реальный пример (sessionId или questionId + где в UI лежит файл),
// добавим второй проход: Sessions -> SessionQuestions/QuestionDocuments/RefFileDelos -> download.
