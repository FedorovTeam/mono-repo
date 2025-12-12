package github

import (
	"context"

	"backend-github-publisher-service/internal/domain"
	"backend-github-publisher-service/pkg/config"
)

// Publisher адаптирует GitHub Client под доменный интерфейс публикации.
type Publisher struct {
	cfg        config.GitHubConfig
	httpClient HTTPClient
}

// NewPublisher создаёт адаптер публикации для GitHub.
func NewPublisher(cfg config.GitHubConfig, httpClient HTTPClient) *Publisher {
	return &Publisher{cfg: cfg, httpClient: httpClient}
}

func (p *Publisher) client(owner, repository string) *Client {
	return NewFromConfig(p.httpClient, p.cfg, owner, repository)
}

// CommitMarkdown публикует контент Markdown в GitHub.
func (p *Publisher) CommitMarkdown(ctx context.Context, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error) {
	return p.client(owner, repository).CommitMarkdown(ctx, path, content, message, branch, correlationID, authorName, authorEmail)
}

// CreatePullRequest открывает Pull Request в GitHub.
func (p *Publisher) CreatePullRequest(ctx context.Context, owner, repository, branch, baseBranch, title, body, correlationID string) (int64, string, error) {
	return p.client(owner, repository).CreatePullRequest(ctx, branch, baseBranch, title, body, correlationID)
}

var _ domain.GitHubPublisher = (*Publisher)(nil)
