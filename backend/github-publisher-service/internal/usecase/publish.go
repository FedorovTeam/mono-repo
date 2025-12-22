package usecase

import (
	githubclient "backend-github-publisher-service/internal/infrastructure/github"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"backend-github-publisher-service/internal/domain"
	"go.uber.org/zap"
)

type PublishUseCase struct {
	publisher           domain.GitHubPublisher
	markdown            domain.MarkdownProvider
	idempotency         domain.IdempotencyStore
	retryPolicyResolver domain.RetryPolicyResolver
	defaultOwner        string
	defaultRepository   string
	defaultBranch       string
	logger              *zap.Logger
}

func NewPublishUseCase(logger *zap.Logger, publisher domain.GitHubPublisher, markdown domain.MarkdownProvider, idempotency domain.IdempotencyStore, retryResolver domain.RetryPolicyResolver, defaultOwner, defaultRepository, defaultBranch string) *PublishUseCase {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &PublishUseCase{
		publisher:           publisher,
		markdown:            markdown,
		idempotency:         idempotency,
		retryPolicyResolver: retryResolver,
		defaultOwner:        defaultOwner,
		defaultRepository:   defaultRepository,
		defaultBranch:       defaultBranch,
		logger:              logger,
	}
}

func (u *PublishUseCase) Execute(ctx context.Context, cmd domain.PublishCommand) (*domain.PublishResult, error) {
	if strings.TrimSpace(cmd.Path) == "" {
		return nil, &domain.ValidationError{Msg: "path is required"}
	}

	if cached, ok := u.idempotency.Get(strings.TrimSpace(cmd.IdempotencyKey)); ok {
		cachedCopy := *cached
		cachedCopy.IdempotencyHit = true
		return &cachedCopy, nil
	}

	owner, repository, err := u.resolveOwnerRepository(cmd.Owner, cmd.Repository)
	if err != nil {
		return nil, err
	}

	branch := cmd.Branch
	baseBranch := cmd.BaseBranch
	if branch == "" {
		branch = u.defaultBranch
	}
	if baseBranch == "" {
		baseBranch = u.defaultBranch
	}

	content := cmd.Content
	if strings.TrimSpace(content) == "" {
		if u.markdown == nil {
			return nil, &domain.PreconditionError{Msg: "storage client is not configured"}
		}
		if cmd.Bucket == "" || cmd.ObjectKey == "" {
			return nil, &domain.ValidationError{Msg: "bucket and object_key are required when content is empty"}
		}
		data, err := u.markdown.LoadMarkdown(ctx, cmd.Bucket, cmd.ObjectKey)
		if err != nil {
			return nil, &domain.PreconditionError{Msg: fmt.Sprintf("load markdown: %v", err)}
		}
		content, err = convertToUTF8(data)
		if err != nil {
			return nil, &domain.ValidationError{Msg: fmt.Sprintf("invalid content encoding: %v", err)}
		}
	}

	commitMessage := strings.TrimSpace(cmd.CommitMessage)
	if commitMessage == "" {
		commitMessage = defaultCommitMessage(cmd.Path)
	}

	policy := u.retryPolicyResolver.Resolve(cmd.RetryPolicy)
	u.logger.Info("resolved retry policy",
		zap.String("correlation_id", cmd.CorrelationID),
		zap.Int("attempt", int(cmd.Attempt)),
		zap.Int("max_attempts", policy.MaxAttempts),
		zap.Duration("initial_backoff", policy.InitialBackoff),
		zap.Duration("max_backoff", policy.MaxBackoff),
	)

	attempts := 0
	var lastResp *domain.PublishResult

	for attempts < policy.MaxAttempts {
		attempts++
		attemptNumber := attempts + int(cmd.Attempt)
		resp := u.publishOnce(ctx, publishAttempt{
			owner:         owner,
			repository:    repository,
			cmd:           cmd,
			branch:        branch,
			baseBranch:    baseBranch,
			content:       content,
			correlationID: cmd.CorrelationID,
			commitMessage: commitMessage,
		})

		u.logger.Info("publish attempt finished",
			zap.String("correlation_id", cmd.CorrelationID),
			zap.Int("status", int(resp.Status)),
			zap.Int("error_code", int(resp.ErrorCode)),
			zap.String("commit_sha", resp.CommitSHA),
			zap.Int64("pr_number", resp.PullRequestNumber),
			zap.Int("attempt", attemptNumber),
		)

		if resp.Status != domain.PublishStatusTemporaryFailure {
			if resp.Status == domain.PublishStatusSuccess {
				u.idempotency.Save(strings.TrimSpace(cmd.IdempotencyKey), resp)
			}
			return resp, nil
		}

		lastResp = resp
		delay := backoffDuration(policy, attempts+int(cmd.Attempt))
		if attempts >= policy.MaxAttempts {
			break
		}
		u.logger.Info("temporary publish failure, scheduling retry",
			zap.String("correlation_id", cmd.CorrelationID),
			zap.Int("status", int(resp.Status)),
			zap.Int("error_code", int(resp.ErrorCode)),
			zap.String("commit_sha", resp.CommitSHA),
			zap.Int64("pr_number", resp.PullRequestNumber),
			zap.Int("attempt", attemptNumber),
			zap.Duration("retry_in", delay),
		)
		select {
		case <-ctx.Done():
			resp.RetryAfter = delay
			return resp, nil
		case <-time.After(delay):
		}
	}

	if lastResp != nil {
		lastResp.RetryAfter = backoffDuration(policy, attempts+1+int(cmd.Attempt))
		return lastResp, nil
	}

	return &domain.PublishResult{Status: domain.PublishStatusPermanentFailure, ErrorCode: domain.PublishErrorCodeUnknown, ErrorMessage: "publish failed without response"}, nil
}

type publishAttempt struct {
	owner         string
	repository    string
	cmd           domain.PublishCommand
	branch        string
	baseBranch    string
	content       string
	correlationID string
	commitMessage string
}

func (u *PublishUseCase) publishOnce(ctx context.Context, attempt publishAttempt) *domain.PublishResult {
	commitSHA, err := u.publisher.CommitMarkdown(ctx, attempt.owner, attempt.repository, attempt.cmd.Path, attempt.content, attempt.commitMessage, attempt.branch, attempt.correlationID, attempt.cmd.AuthorName, attempt.cmd.AuthorEmail)
	if err != nil {
		statusValue, code, message := classifyPublishError(err)
		return &domain.PublishResult{
			Status:       statusValue,
			ErrorCode:    code,
			ErrorMessage: message,
		}
	}

	var prNumber int64
	var prURL string
	if attempt.cmd.CreatePR {
		prNumber, prURL, err = u.publisher.CreatePullRequest(ctx, attempt.owner, attempt.repository, attempt.branch, attempt.baseBranch, attempt.cmd.PrTitle, attempt.cmd.PrBody, attempt.correlationID)
		if err != nil {
			statusValue, code, message := classifyPublishError(err)
			return &domain.PublishResult{
				Status:       statusValue,
				ErrorCode:    code,
				ErrorMessage: message,
				CommitSHA:    commitSHA,
			}
		}
	}

	return &domain.PublishResult{
		Status:            domain.PublishStatusSuccess,
		CommitSHA:         commitSHA,
		PullRequestNumber: prNumber,
		PullRequestURL:    prURL,
	}
}

func (u *PublishUseCase) resolveOwnerRepository(owner, repository string) (string, string, error) {
	owner = strings.TrimSpace(owner)
	repository = strings.TrimSpace(repository)

	if strings.Contains(repository, "/") {
		parts := strings.Split(repository, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", &domain.ValidationError{Msg: "repository must not contain slash"}
		}
		if owner != "" && owner != parts[0] {
			return "", "", &domain.ValidationError{Msg: "repository must not contain owner prefix"}
		}
		owner, repository = parts[0], parts[1]
	}

	if owner == "" {
		owner = u.defaultOwner
	}
	if repository == "" {
		repository = u.defaultRepository
	}

	return owner, repository, nil
}

func classifyPublishError(err error) (domain.PublishStatus, domain.PublishErrorCode, string) {
	if err == nil {
		return domain.PublishStatusSuccess, domain.PublishErrorCodeUnspecified, ""
	}

	if errors.Is(err, context.DeadlineExceeded) { //nolint:errorlint
		return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeTimeout, err.Error()
	}

	var apiErr *githubclient.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden:
			return domain.PublishStatusPermanentFailure, domain.PublishErrorCodePermission, apiErr.Error()
		case apiErr.StatusCode == http.StatusNotFound:
			return domain.PublishStatusPermanentFailure, domain.PublishErrorCodeNotFound, apiErr.Error()
		case apiErr.StatusCode == http.StatusConflict:
			return domain.PublishStatusPermanentFailure, domain.PublishErrorCodeConflict, apiErr.Error()
		case apiErr.StatusCode == http.StatusTooManyRequests:
			return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeRateLimit, apiErr.Error()
		case apiErr.StatusCode >= 500:
			return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeServerError, apiErr.Error()
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeTimeout, err.Error()
		}
		return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeNetwork, err.Error()
	}

	lowerMsg := strings.ToLower(err.Error())
	if strings.Contains(lowerMsg, "rate limit") {
		return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeRateLimit, err.Error()
	}
	if strings.Contains(lowerMsg, "timeout") || strings.Contains(lowerMsg, "deadline") {
		return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeTimeout, err.Error()
	}
	if strings.Contains(lowerMsg, "connection reset") || strings.Contains(lowerMsg, "connection refused") || strings.Contains(lowerMsg, "network is unreachable") {
		return domain.PublishStatusTemporaryFailure, domain.PublishErrorCodeNetwork, err.Error()
	}

	return domain.PublishStatusPermanentFailure, domain.PublishErrorCodeUnknown, err.Error()
}

func defaultCommitMessage(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "Update markdown content"
	}
	return "Update " + path
}

func backoffDuration(policy domain.RetryPolicy, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := policy.InitialBackoff
	if delay <= 0 {
		delay = 5 * time.Second
	}
	maxBackoff := policy.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}

	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= maxBackoff {
			return maxBackoff
		}
	}

	if delay > maxBackoff {
		return maxBackoff
	}
	return delay
}
