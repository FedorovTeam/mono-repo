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

	body := map[string]any{
		"message":   message,
		"content":   base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":    branch,
		"committer": map[string]string{"name": authorName, "email": authorEmail},
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

	return fmt.Errorf(
		"unexpected status %d%s (repo=%s, branch=%s, url=%s): %s",
		resp.StatusCode,
		hint,
		repo,
		branch,
		url,
		string(body),
	)
}
