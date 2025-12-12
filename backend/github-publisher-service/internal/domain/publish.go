package domain

import (
	"context"
	"time"
)

// PublishStatus описывает итог выполнения публикации.
type PublishStatus int

const (
	PublishStatusUnspecified PublishStatus = iota
	PublishStatusSuccess
	PublishStatusTemporaryFailure
	PublishStatusPermanentFailure
)

// PublishErrorCode определяет тип ошибки публикации.
type PublishErrorCode int

const (
	PublishErrorCodeUnspecified PublishErrorCode = iota
	PublishErrorCodeTimeout
	PublishErrorCodeServerError
	PublishErrorCodeRateLimit
	PublishErrorCodeConflict
	PublishErrorCodePermission
	PublishErrorCodeNetwork
	PublishErrorCodeNotFound
	PublishErrorCodeUnknown
)

// PublishCommand описывает входные параметры публикации Markdown.
type PublishCommand struct {
	Owner          string
	Repository     string
	Branch         string
	BaseBranch     string
	Path           string
	Content        string
	CommitMessage  string
	PrTitle        string
	PrBody         string
	CreatePR       bool
	CorrelationID  string
	AuthorName     string
	AuthorEmail    string
	Bucket         string
	ObjectKey      string
	IdempotencyKey string
	Attempt        uint32
	RetryPolicy    *RetryPolicy
}

// PublishResult содержит результат выполнения use-case публикации.
type PublishResult struct {
	Status            PublishStatus
	CommitSHA         string
	PullRequestNumber int64
	PullRequestURL    string
	ErrorCode         PublishErrorCode
	ErrorMessage      string
	RetryAfter        time.Duration
	IdempotencyHit    bool
}

// RetryPolicy описывает стратегию ретраев.
type RetryPolicy struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MaxAttempts    int
}

// MarkdownProvider умеет загружать Markdown из хранилища.
type MarkdownProvider interface {
	LoadMarkdown(ctx context.Context, bucket, key string) ([]byte, error)
}

// GitHubPublisher публикует Markdown в GitHub и создаёт PR при необходимости.
type GitHubPublisher interface {
	CommitMarkdown(ctx context.Context, owner, repository, path, content, message, branch, correlationID, authorName, authorEmail string) (string, error)
	CreatePullRequest(ctx context.Context, owner, repository, branch, baseBranch, title, body, correlationID string) (int64, string, error)
}

// IdempotencyStore хранит результаты успешных публикаций.
type IdempotencyStore interface {
	Get(key string) (*PublishResult, bool)
	Save(key string, result *PublishResult)
}

// RetryPolicyResolver преобразует переданную политику ретраев в фактическую.
type RetryPolicyResolver interface {
	Resolve(policy *RetryPolicy) RetryPolicy
}

// ValidationError сигнализирует об ошибках входных данных.
type ValidationError struct {
	Msg string
}

func (e *ValidationError) Error() string { return e.Msg }

// PreconditionError описывает ошибки окружения/зависимостей.
type PreconditionError struct {
	Msg string
}

func (e *PreconditionError) Error() string { return e.Msg }
