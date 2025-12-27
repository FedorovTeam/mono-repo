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

// account:
// Username: uploader
// Secret: fgqjPDaqjB5F0epj3YVe90xlzXx4IB1ARExVHS8wg2Rmiil6kX57S1VFPQB2

// provider client_id: L6AmiswYzea2gAs5aGfzhLP9jlapG42ppPhtutoF

/*
curl -sS -X POST 'https://auth.tech.fedorov-team.tech/application/o/token/' \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=client_credentials' \
  --data-urlencode 'client_id=L6AmiswYzea2gAs5aGfzhLP9jlapG42ppPhtutoF' \
  --data-urlencode 'username=uploader' \
  --data-urlencode 'password=fgqjPDaqjB5F0epj3YVe90xlzXx4IB1ARExVHS8wg2Rmiil6kX57S1VFPQB2' \
  --data-urlencode 'scope=openid profile email' | jq
*/

func main() {
	endpoint := flag.String("endpoint", "https://gateway.tech.fedorov-team.tech/query", "GraphQL endpoint gateway-service (/query)")
	token := flag.String("token", "Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6IjhmOGUyZjc5ZTcyY2YyNzkxNjJhM2QxZDU2NzVlNTRkIiwidHlwIjoiSldUIn0.eyJpc3MiOiJodHRwczovL2F1dGgudGVjaC5mZWRvcm92LXRlYW0udGVjaC9hcHBsaWNhdGlvbi9vL2NvbnNvbGUvIiwic3ViIjoiNmJhMDlkYjcxMWJiODc3ZDE3NTYyM2ZjMDhkZGQzNGU1NzU3ODZiMDdjODI0NmIzNzVjMzY1OWE4ZGFhNGZiZiIsImF1ZCI6Ikw2QW1pc3dZemVhMmdBczVhR2Z6aExQOWpsYXBHNDJwcFBodHV0b0YiLCJleHAiOjE3NjY4MDgyOTUsImlhdCI6MTc2NjgwNzk5NSwiYXV0aF90aW1lIjoxNzY2ODA3OTk1LCJhY3IiOiJnb2F1dGhlbnRpay5pby9wcm92aWRlcnMvb2F1dGgyL2RlZmF1bHQiLCJlbWFpbCI6IiIsImVtYWlsX3ZlcmlmaWVkIjp0cnVlLCJuYW1lIjoidXBsb2FkZXIiLCJnaXZlbl9uYW1lIjoidXBsb2FkZXIiLCJwcmVmZXJyZWRfdXNlcm5hbWUiOiJ1cGxvYWRlciIsIm5pY2tuYW1lIjoidXBsb2FkZXIiLCJncm91cHMiOltdLCJhenAiOiJMNkFtaXN3WXplYTJnQXM1YUdmemhMUDlqbGFwRzQycHBQaHR1dG9GIiwidWlkIjoiam5NRGVDTzVXTUNkVGtaR2laR0hObjJxSnV5YkV3NzNET1Y1bWhKRCJ9.WZ5KsHejI3z3my_26kuYpQP5nMpcGBGfCZxFPWU7Vv670LVbdcGVc55hRaEDlAQJiIhW3gnKTU3m2LOS4VgZNGTfdtr6FCprHNXk4yN679JsrmAZX7PnZZaHG9prAZPTLyyRAtMzLC6CLsFYACTKD0kdGel9IhK44cFB54oHDbbptywonQ8HDw2Ko5new8mjJ1zhmwXXqSC928QeC2-T46eZjlsmn0MCEMvAAR9MdWmnFMw7Btv6ScKzmfQDY8LW2dRpCLydmrimSfDwmWqfjDCqN37dskZPcnxLvGt2Ttt39dMybv6qB1xSrJk0GX9FkK57VaM9kvKv1fyYlSFfG9PK1mvH2W1XobuuTezdbSYfknCQy1ul68XjA7lps_ObpcD1ggiu7qL-5BY_dzk_fnIRBtHgEJm5nxuaxo8bWz0EJ3SItDRPz7ZPx1c0hvHLBWLaWm1E8I7fKXiQ5LTNg75smEP9ldhwd6NY07pkuHfJu6vD6uvTUeojJdXPo-qW10xsTi6GsIPDMnXZJTFGF6irxnHnUHXvXxj2ZeJ6HLVw4qMCeMfxWI7me1n8afUseYhkekAChcC4YNXtNlv3PsPz_q3-7tcZIxiN-AB7sQe5oF1zPpAIkvv7W0r_BArA40rNevH7CowgdvE-Fapr12GLzw8ulV2qpcdyROTfB04", "Заголовок Authorization: Bearer <...>")
	apiKey := flag.String("apiKey", "secret", "Значение X-API-Key, если включено")
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
