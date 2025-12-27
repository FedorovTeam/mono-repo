package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Скрипт перебирает файлы в каталоге "БД ИИ" и последовательно загружает их
// в backend через GraphQL мутацию uploadKnowledgeDocument.
// Расчёт путей совместим с логикой фронтенда: knowledge/docs/plenary/<номер>/...
// Запуск: go run upload.go -endpoint http://localhost:8080/query -token "Bearer ..." -repository docs -branch main
// Дополнительные флаги: -apiKey для X-API-Key, -delay для паузы между загрузками, -dryRun для проверки без отправки.

const knowledgeRoot = "knowledge/docs"

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

func main() {
	endpoint := flag.String("endpoint", "https://gateway.tech.fedorov-team.tech/query", "GraphQL endpoint gateway-service (/query)")
	token := flag.String("token", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiYWRtaW4iOnRydWUsImlhdCI6MTUxNjIzOTAyMn0.KMUFsIDTnFmyG3nMiGM6H9FNFUROf3wh7SmqJp-QV30", "Заголовок Authorization: Bearer <...>")
	apiKey := flag.String("apiKey", "", "Значение X-API-Key, если включено")
	repository := flag.String("repository", "docs", "Целевой репозиторий")
	branch := flag.String("branch", "main", "Целевая ветка")
	root := flag.String("root", ".", "Корневая папка с файлами для загрузки")
	delay := flag.Duration("delay", time.Second, "Пауза между загрузками")
	dryRun := flag.Bool("dryRun", false, "Только вывести план без загрузки")
	flag.Parse()

	if *endpoint == "" {
		exitWithError(errors.New("нужен адрес GraphQL endpoint (флаг -endpoint)"))
	}
	if *token == "" {
		exitWithError(errors.New("укажите токен авторизации в флаге -token"))
	}

	ctx := context.Background()
	files, err := collectFiles(*root)
	if err != nil {
		exitWithError(err)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	fmt.Printf("Найдено файлов: %d\n", len(files))

	for i, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			exitWithError(fmt.Errorf("не удалось прочитать %s: %w", path, err))
		}

		uploadPath := resolvePath(*root, path)
		fmt.Printf("[%d/%d] %s -> %s\n", i+1, len(files), path, uploadPath)

		if *dryRun {
			continue
		}

		if err := uploadDocument(ctx, client, *endpoint, *token, *apiKey, *repository, *branch, uploadPath, path, info.Size()); err != nil {
			exitWithError(fmt.Errorf("ошибка загрузки %s: %w", path, err))
		}

		time.Sleep(*delay)
	}
}

func collectFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), "~") {
			return nil
		}
		if d.Name() == ".DS_Store" {
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

func resolvePath(root, filePath string) string {
	rel, err := filepath.Rel(root, filePath)
	if err != nil {
		rel = filepath.Base(filePath)
	}

	sessionNumber := extractLeadingDigits(filepath.Dir(rel))
	if sessionNumber == "" {
		sessionNumber = "00"
	}
	sessionNumber = pad(sessionNumber, 2)

	origName := filepath.Base(filePath)

	docType, _ := classifyDocument(origName)

	switch docType {
	case "voting":
		return filepath.ToSlash(fmt.Sprintf("%s/plenary/%s/votings/%s", knowledgeRoot, sessionNumber, origName))
	default:
		return filepath.ToSlash(fmt.Sprintf("%s/plenary/%s/transcripts/%s", knowledgeRoot, sessionNumber, origName))
	}
}

func extractLeadingDigits(dir string) string {
	base := filepath.Base(dir)
	digits := regexp.MustCompile(`^(\d+)`).FindString(base)
	return digits
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

	if digits != "" {
		return "transcript", digits
	}
	return "transcript", "1"
}

func uploadDocument(ctx context.Context, client *http.Client, endpoint, token, apiKey, repository, branch, path, filePath string, size int64) error {
	mutation := "" +
		"mutation($input: UploadKnowledgeDocumentInput!){" +
		" uploadKnowledgeDocument(input:$input){" +
		" uploadUrl uploadMethod requiredHeaders{ name value } document{ id status path } } }"

	payload := uploadRequest{
		Query: mutation,
		Variables: map[string]interface{}{
			"input": map[string]interface{}{
				"fileName":      filepath.Base(filePath),
				"fileSizeBytes": size,
				"repository":    repository,
				"branch":        branch,
				"path":          path,
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GraphQL ответ %d: %s", resp.StatusCode, string(data))
	}

	var gqlResp uploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return err
	}
	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("GraphQL ошибка: %s", gqlResp.Errors[0].Message)
	}

	payloadData := gqlResp.Data.UploadKnowledgeDocument
	if payloadData.UploadURL == "" {
		return errors.New("пустой uploadUrl в ответе")
	}

	requiredHeaders := make(http.Header)
	for _, h := range payloadData.RequiredHeaders {
		requiredHeaders.Set(h.Name, h.Value)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	putReq, err := http.NewRequestWithContext(ctx, payloadData.UploadMethod, payloadData.UploadURL, file)
	if err != nil {
		return err
	}
	putReq.ContentLength = size
	for name, values := range requiredHeaders {
		for _, v := range values {
			putReq.Header.Add(name, v)
		}
	}

	putResp, err := client.Do(putReq)
	if err != nil {
		return err
	}
	defer putResp.Body.Close()

	if putResp.StatusCode >= 300 {
		data, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("загрузка завершилась статусом %d: %s", putResp.StatusCode, string(data))
	}

	fmt.Printf("  ✔ Документ %s загружен (id=%s, статус=%s)\n", payloadData.Document.Path, payloadData.Document.ID, payloadData.Document.Status)
	return nil
}

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
