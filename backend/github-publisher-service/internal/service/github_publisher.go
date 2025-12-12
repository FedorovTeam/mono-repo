package service

import (
	"context"

	"backend-github-publisher-service/internal/config"
	"backend-github-publisher-service/internal/domain"
	githubclient "backend-github-publisher-service/internal/github"
)

type GitHubClientPublisher struct {
	cfg        *config.Config
	httpClient githubclient.HTTPClient
}

func NewGitHubClientPublisher(cfg *config.Config, httpClient githubclient.HTTPClient) *GitHubClientPublisher {
	return &GitHubClientPublisher{cfg: cfg, httpClient: httpClient}
}

func (p *GitHubClientPublisher) CommitMarkdown(ctx context.Context, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error) {
	client := githubclient.NewFromConfig(p.httpClient, p.cfg.GitHub, owner, repository)
	return client.CommitMarkdown(ctx, path, content, message, branch, correlationID, authorName, authorEmail)
}

func (p *GitHubClientPublisher) CreatePullRequest(ctx context.Context, owner, repository, branch, baseBranch, title, body, correlationID string) (int64, string, error) {
	client := githubclient.NewFromConfig(p.httpClient, p.cfg.GitHub, owner, repository)
	return client.CreatePullRequest(ctx, branch, baseBranch, title, body, correlationID)
}

var _ domain.GitHubPublisher = (*GitHubClientPublisher)(nil)
