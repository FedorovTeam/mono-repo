package usecase

import (
	githubclient "backend-github-publisher-service/internal/infrastructure/github"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"backend-github-publisher-service/internal/domain"
)

func TestPublishUseCase_ReturnsValidationError(t *testing.T) {
	uc := NewPublishUseCase(&stubPublisher{}, nil, NewMemoryIdempotencyStore(), NewRetryPolicyResolver(domain.RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}), "", "", "")

	_, err := uc.Execute(context.Background(), domain.PublishCommand{})
	var validationErr *domain.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestPublishUseCase_LoadsContentFromStorage(t *testing.T) {
	markdown := &stubMarkdown{data: []byte("# hi")}
	publisher := &stubPublisher{commitResult: "commit-1"}
	uc := NewPublishUseCase(publisher, markdown, NewMemoryIdempotencyStore(), NewRetryPolicyResolver(domain.RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}), "owner", "repo", "main")

	result, err := uc.Execute(context.Background(), domain.PublishCommand{Path: "docs/a.md", Bucket: "b", ObjectKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if markdown.bucket != "b" || markdown.key != "k" {
		t.Fatalf("unexpected storage call: %s %s", markdown.bucket, markdown.key)
	}
	if len(publisher.commitCalls) != 1 {
		t.Fatalf("expected commit call")
	}
	if publisher.commitCalls[0].content != "# hi" {
		t.Fatalf("unexpected content: %s", publisher.commitCalls[0].content)
	}
	if result.CommitSHA != "commit-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPublishUseCase_IdempotencyHit(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	store.Save("idem", &domain.PublishResult{Status: domain.PublishStatusSuccess, CommitSHA: "cached"})
	publisher := &stubPublisher{}
	uc := NewPublishUseCase(publisher, nil, store, NewRetryPolicyResolver(domain.RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}), "owner", "repo", "main")

	result, err := uc.Execute(context.Background(), domain.PublishCommand{Path: "p.md", IdempotencyKey: "idem"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IdempotencyHit {
		t.Fatalf("expected idempotency hit")
	}
	if len(publisher.commitCalls) != 0 {
		t.Fatalf("expected no commits, got %d", len(publisher.commitCalls))
	}
}

func TestPublishUseCase_RetryOnTemporaryFailure(t *testing.T) {
	publisher := &stubPublisher{}
	publisher.commitFunc = func(ctx context.Context, owner, repo, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error) {
		if len(publisher.commitCalls) == 0 {
			return "", &githubclient.APIError{StatusCode: http.StatusInternalServerError, Message: "fail"}
		}
		return "retry-success", nil
	}

	policy := domain.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}
	uc := NewPublishUseCase(publisher, nil, NewMemoryIdempotencyStore(), NewRetryPolicyResolver(policy), "owner", "repo", "main")

	result, err := uc.Execute(context.Background(), domain.PublishCommand{Path: "p.md", Content: "data"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Status != domain.PublishStatusSuccess {
		t.Fatalf("expected success, got %+v", result)
	}
	if len(publisher.commitCalls) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(publisher.commitCalls))
	}
}

type stubMarkdown struct {
	data   []byte
	bucket string
	key    string
	err    error
}

func (s *stubMarkdown) LoadMarkdown(_ context.Context, bucket, key string) ([]byte, error) {
	s.bucket = bucket
	s.key = key
	if s.err != nil {
		return nil, s.err
	}
	return s.data, nil
}

type commitCall struct {
	owner       string
	repository  string
	path        string
	content     string
	message     string
	branch      string
	correlation string
	authorName  string
	authorEmail string
}

type prCall struct {
	branch      string
	baseBranch  string
	title       string
	body        string
	correlation string
}

type stubPublisher struct {
	commitFunc   func(ctx context.Context, owner, repo, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error)
	prFunc       func(ctx context.Context, owner, repo, branch, baseBranch, title, body, correlationID string) (int64, string, error)
	commitResult string
	prResult     int64
	prURL        string
	commitCalls  []commitCall
	prCalls      []prCall
}

func (s *stubPublisher) CommitMarkdown(ctx context.Context, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error) {
	call := commitCall{owner: owner, repository: repository, path: path, content: content, message: message, branch: branch, correlation: correlationID, authorName: authorName, authorEmail: authorEmail}
	if s.commitFunc != nil {
		result, err := s.commitFunc(ctx, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail)
		s.commitCalls = append(s.commitCalls, call)
		return result, err
	}
	s.commitCalls = append(s.commitCalls, call)
	return s.commitResult, nil
}

func (s *stubPublisher) CreatePullRequest(ctx context.Context, owner, repository, branch, baseBranch, title, body, correlationID string) (int64, string, error) {
	s.prCalls = append(s.prCalls, prCall{branch: branch, baseBranch: baseBranch, title: title, body: body, correlation: correlationID})
	if s.prFunc != nil {
		return s.prFunc(ctx, owner, repository, branch, baseBranch, title, body, correlationID)
	}
	return s.prResult, s.prURL, nil
}

var _ domain.GitHubPublisher = (*stubPublisher)(nil)
