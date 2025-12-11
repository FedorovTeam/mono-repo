package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"backend-github-publisher-service/internal/config"
)

// APIError описывает неуспешный ответ GitHub API.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
}

// HTTPClient определяет интерфейс минимального HTTP-клиента.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client реализует интеграцию с GitHub API для публикации Markdown.
type Client struct {
	httpClient        HTTPClient
	baseURL           string
	token             string
	owner             string
	repository        string
	defaultBranch     string
	correlationHeader string
	authorName        string
	authorEmail       string
}

type repositoryFile struct {
	SHA      string `json:"sha"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

// New создаёт GitHub клиента на основе конфигурации.
func New(httpClient HTTPClient, baseURL, token, owner, repository, defaultBranch, correlationHeader, authorName, authorEmail string) *Client {
	return &Client{
		httpClient:        httpClient,
		baseURL:           strings.TrimSuffix(baseURL, "/"),
		token:             token,
		owner:             owner,
		repository:        repository,
		defaultBranch:     defaultBranch,
		correlationHeader: correlationHeader,
		authorName:        authorName,
		authorEmail:       authorEmail,
	}
}

// NewFromConfig создаёт GitHub клиент, используя значения из конфигурации и опциональные переопределения owner/repository.
func NewFromConfig(httpClient HTTPClient, cfg config.GitHubConfig, owner, repository string) *Client {
	if owner == "" {
		owner = cfg.Owner
	}
	if repository == "" {
		repository = cfg.Repository
	}

	return New(
		httpClient,
		cfg.BaseURL,
		cfg.Token,
		owner,
		repository,
		cfg.DefaultBranch,
		cfg.CorrelationHeader,
		cfg.AuthorName,
		cfg.AuthorEmail,
	)
}

// CommitMarkdown создаёт или обновляет Markdown-файл в репозитории и возвращает SHA коммита.
func (c *Client) CommitMarkdown(ctx context.Context, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error) {
	if branch == "" {
		branch = c.defaultBranch
	}
	if authorName == "" {
		authorName = c.authorName
	}
	if authorEmail == "" {
		authorEmail = c.authorEmail
	}

	existingFile, err := c.getExistingFile(ctx, path, branch, correlationID)
	if err != nil {
		return "", fmt.Errorf("get existing file: %w", err)
	}

	body := map[string]any{
		"message":   message,
		"content":   base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":    branch,
		"committer": map[string]string{"name": authorName, "email": authorEmail},
	}

	if existingFile != nil {
		existingContent, err := base64.StdEncoding.DecodeString(existingFile.Content)
		if err != nil {
			return "", fmt.Errorf("decode existing content: %w", err)
		}
		if string(existingContent) == content {
			return existingFile.SHA, nil
		}

		body["sha"] = existingFile.SHA
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, c.owner, c.repository, strings.TrimPrefix(path, "/"))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req, correlationID)

	if existingFile != nil && existingFile.SHA != "" {
		req.Header.Set("If-Match", existingFile.SHA)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", c.unexpectedStatusError(resp, branch, url)
	}

	var commitResp struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&commitResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	return commitResp.Commit.SHA, nil
}

func (c *Client) getExistingFile(ctx context.Context, path, branch, correlationID string) (*repositoryFile, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, c.owner, c.repository, strings.TrimPrefix(path, "/"))
	if branch != "" {
		url += "?ref=" + branch
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req, correlationID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, c.unexpectedStatusError(resp, branch, url)
	}

	var file repositoryFile
	if err := json.NewDecoder(resp.Body).Decode(&file); err != nil {
		return nil, fmt.Errorf("decode existing file: %w", err)
	}

	return &file, nil
}

// CreatePullRequest открывает Pull Request из ветки branch в baseBranch.
func (c *Client) CreatePullRequest(ctx context.Context, branch, baseBranch, title, body, correlationID string) (int64, string, error) {
	if baseBranch == "" {
		baseBranch = c.defaultBranch
	}
	requestBody := map[string]string{
		"title": title,
		"head":  branch,
		"base":  baseBranch,
		"body":  body,
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return 0, "", fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/repos/%s/%s/pulls", c.baseURL, c.owner, c.repository)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, "", fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req, correlationID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return 0, "", c.unexpectedStatusError(resp, branch, url)
	}

	var prResp struct {
		Number int64  `json:"number"`
		URL    string `json:"html_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&prResp); err != nil {
		return 0, "", fmt.Errorf("decode response: %w", err)
	}

	return prResp.Number, prResp.URL, nil
}

func (c *Client) setHeaders(req *http.Request, correlationID string) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if correlationID != "" {
		req.Header.Set(c.correlationHeader, correlationID)
	}
}

func (c *Client) unexpectedStatusError(resp *http.Response, branch, url string) error {
	body, _ := io.ReadAll(resp.Body)
	repo := fmt.Sprintf("%s/%s", c.owner, c.repository)
	hint := ""
	if resp.StatusCode == http.StatusNotFound {
		hint = "; возможно, указан некорректный репозиторий или токен не имеет нужных прав"
	}

	return &APIError{
		StatusCode: resp.StatusCode,
		Message: fmt.Sprintf(
			"unexpected status %d%s (repo=%s, branch=%s, url=%s): %s",
			resp.StatusCode,
			hint,
			repo,
			branch,
			url,
			string(body),
		),
	}
}
