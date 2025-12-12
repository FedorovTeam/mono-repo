package usecase

import (
	githubclient "backend-github-publisher-service/internal/infrastructure/github"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"backend-github-publisher-service/internal/domain"
	"backend-github-publisher-service/internal/domain/mocks"
)

func TestPublishUseCase_ReturnsValidationError(t *testing.T) {
	uc := NewPublishUseCase(&mocks.GitHubPublisher{}, nil, NewMemoryIdempotencyStore(), NewRetryPolicyResolver(domain.RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}), "", "", "")

	_, err := uc.Execute(context.Background(), domain.PublishCommand{})
	var validationErr *domain.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestPublishUseCase_LoadsContentFromStorage(t *testing.T) {
	markdown := &stubMarkdown{data: []byte("# hi")}
	publisher := &mocks.GitHubPublisher{CommitResult: "commit-1"}
	uc := NewPublishUseCase(publisher, markdown, NewMemoryIdempotencyStore(), NewRetryPolicyResolver(domain.RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}), "owner", "repo", "main")

	result, err := uc.Execute(context.Background(), domain.PublishCommand{Path: "docs/a.md", Bucket: "b", ObjectKey: "k"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if markdown.bucket != "b" || markdown.key != "k" {
		t.Fatalf("unexpected storage call: %s %s", markdown.bucket, markdown.key)
	}
	if len(publisher.CommitCalls) != 1 {
		t.Fatalf("expected commit call")
	}
	if publisher.CommitCalls[0].Content != "# hi" {
		t.Fatalf("unexpected content: %s", publisher.CommitCalls[0].Content)
	}
	if result.CommitSHA != "commit-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPublishUseCase_IdempotencyHit(t *testing.T) {
	store := NewMemoryIdempotencyStore()
	store.Save("idem", &domain.PublishResult{Status: domain.PublishStatusSuccess, CommitSHA: "cached"})
	publisher := &mocks.GitHubPublisher{}
	uc := NewPublishUseCase(publisher, nil, store, NewRetryPolicyResolver(domain.RetryPolicy{MaxAttempts: 1, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}), "owner", "repo", "main")

	result, err := uc.Execute(context.Background(), domain.PublishCommand{Path: "p.md", IdempotencyKey: "idem"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.IdempotencyHit {
		t.Fatalf("expected idempotency hit")
	}
	if len(publisher.CommitCalls) != 0 {
		t.Fatalf("expected no commits, got %d", len(publisher.CommitCalls))
	}
}

func TestPublishUseCase_RetryOnTemporaryFailure(t *testing.T) {
	publisher := &mocks.GitHubPublisher{}
	publisher.CommitFunc = func(ctx context.Context, owner, repo, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error) {
		if len(publisher.CommitCalls) == 0 {
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
	if len(publisher.CommitCalls) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(publisher.CommitCalls))
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
