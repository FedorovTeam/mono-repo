package mocks

import (
	"context"

	"backend-github-publisher-service/internal/domain"
)

type CommitCall struct {
	Owner       string
	Repository  string
	Path        string
	Content     string
	Message     string
	Branch      string
	Correlation string
	AuthorName  string
	AuthorEmail string
}

type PullRequestCall struct {
	Owner       string
	Repository  string
	Branch      string
	BaseBranch  string
	Title       string
	Body        string
	Correlation string
}

type GitHubPublisher struct {
	CommitFunc func(ctx context.Context, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error)
	PRFunc     func(ctx context.Context, owner, repository, branch, baseBranch, title, body, correlationID string) (int64, string, error)

	CommitResult string
	PRResult     int64
	PRURL        string

	CommitCalls []CommitCall
	PRCalls     []PullRequestCall
}

func (p *GitHubPublisher) CommitMarkdown(ctx context.Context, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error) {
	call := CommitCall{
		Owner:       owner,
		Repository:  repository,
		Path:        path,
		Content:     content,
		Message:     message,
		Branch:      branch,
		Correlation: correlationID,
		AuthorName:  authorName,
		AuthorEmail: authorEmail,
	}

	if p.CommitFunc != nil {
		result, err := p.CommitFunc(ctx, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail)
		p.CommitCalls = append(p.CommitCalls, call)
		return result, err
	}

	p.CommitCalls = append(p.CommitCalls, call)
	return p.CommitResult, nil
}

func (p *GitHubPublisher) CreatePullRequest(ctx context.Context, owner, repository, branch, baseBranch, title, body, correlationID string) (int64, string, error) {
	call := PullRequestCall{
		Owner:       owner,
		Repository:  repository,
		Branch:      branch,
		BaseBranch:  baseBranch,
		Title:       title,
		Body:        body,
		Correlation: correlationID,
	}

	if p.PRFunc != nil {
		result, url, err := p.PRFunc(ctx, owner, repository, branch, baseBranch, title, body, correlationID)
		p.PRCalls = append(p.PRCalls, call)
		return result, url, err
	}

	p.PRCalls = append(p.PRCalls, call)
	return p.PRResult, p.PRURL, nil
}

var _ domain.GitHubPublisher = (*GitHubPublisher)(nil)
