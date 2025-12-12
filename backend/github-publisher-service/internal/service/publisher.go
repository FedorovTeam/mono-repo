package service

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"backend-github-publisher-service/internal/config"
	githubclient "backend-github-publisher-service/internal/github"
	"backend-github-publisher-service/internal/storage"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// PublisherServer реализует gRPC слой поверх GitHub клиента.
type PublisherServer struct {
	githubpublisherv1.UnimplementedGithubPublisherServiceServer

	httpClient githubclient.HTTPClient
	storage    storage.MarkdownLoader
	cfg        *config.Config
	cache      *idempotencyCache
}

// NewPublisherServer создаёт сервер публикации.
func NewPublisherServer(cfg *config.Config, httpClient githubclient.HTTPClient, storage storage.MarkdownLoader) *PublisherServer {
	return &PublisherServer{cfg: cfg, httpClient: httpClient, storage: storage, cache: newIdempotencyCache()}
}

// PublishMarkdown сохраняет Markdown в GitHub и, при необходимости, открывает Pull Request.
func (s *PublisherServer) PublishMarkdown(ctx context.Context, req *githubpublisherv1.PublishMarkdownRequest) (*githubpublisherv1.PublishMarkdownResponse, error) {
	if req.GetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}

	correlationID := s.pickCorrelationID(ctx, req.GetCorrelationId())

	if cached, ok := s.cache.Get(strings.TrimSpace(req.GetIdempotencyKey())); ok {
		log.Printf("correlation_id=%s publish request path=%s owner=%s repo=%s create_pr=%t idempotency_hit=true", correlationID, req.GetPath(), req.GetOwner(), req.GetRepository(), req.GetCreatePr())
		resp := *cached
		resp.IdempotencyHit = true
		return &resp, nil
	}

	log.Printf("correlation_id=%s publish request path=%s owner=%s repo=%s create_pr=%t", correlationID, req.GetPath(), req.GetOwner(), req.GetRepository(), req.GetCreatePr())

	owner := req.GetOwner()
	repository := req.GetRepository()

	if strings.Contains(repository, "/") {
		parts := strings.Split(repository, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, status.Error(codes.InvalidArgument, "repository must not contain slash")
		}
		if owner != "" && owner != parts[0] {
			return nil, status.Error(codes.InvalidArgument, "repository must not contain owner prefix")
		}
		owner, repository = parts[0], parts[1]
	}

	if owner == "" {
		owner = s.cfg.GitHub.Owner
	}
	if repository == "" {
		repository = s.cfg.GitHub.Repository
	}

	content := req.GetContent()
	if content == "" {
		if s.storage == nil {
			log.Printf("correlation_id=%s storage client is not configured", correlationID)
			return nil, status.Error(codes.FailedPrecondition, "storage client is not configured")
		}
		bucket := req.GetBucket()
		key := req.GetObjectKey()
		if bucket == "" || key == "" {
			return nil, status.Error(codes.InvalidArgument, "bucket and object_key are required when content is empty")
		}
		data, err := s.storage.LoadMarkdown(ctx, bucket, key)
		if err != nil {
			log.Printf("correlation_id=%s load markdown error: %v", correlationID, err)
			return nil, status.Errorf(codes.FailedPrecondition, "load markdown: %v", err)
		}
		log.Printf("correlation_id=%s loaded markdown bucket=%s key=%s", correlationID, bucket, key)
		content, err = convertToUTF8(data)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid content encoding: %v", err)
		}
	}

	branch := req.GetBranch()
	baseBranch := req.GetBaseBranch()
	if branch == "" {
		branch = s.cfg.GitHub.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = s.cfg.GitHub.DefaultBranch
	}

	commitMessage := strings.TrimSpace(req.GetCommitMessage())
	if commitMessage == "" {
		commitMessage = defaultCommitMessage(req.GetPath())
	}

	policy := s.resolveRetryPolicy(req.GetRetryPolicy())

	log.Printf("correlation_id=%s resolved retry policy: attempts=%d initial=%s max=%s", correlationID, policy.maxAttempts, policy.initialBackoff, policy.maxBackoff)

	client := githubclient.NewFromConfig(
		s.httpClient,
		s.cfg.GitHub,
		owner,
		repository,
	)

	attempts := 0
	var lastResp *githubpublisherv1.PublishMarkdownResponse

	for attempts < policy.maxAttempts {
		attempts++
		resp := s.publishOnce(ctx, publishAttempt{
			client:        client,
			req:           req,
			branch:        branch,
			baseBranch:    baseBranch,
			content:       content,
			correlationID: correlationID,
			commitMessage: commitMessage,
		})

		log.Printf("correlation_id=%s publish attempt status=%v error_code=%v commit=%s pr=%d", correlationID, resp.GetStatus(), resp.GetErrorCode(), resp.GetCommitSha(), resp.GetPullRequestNumber())

		if resp.GetStatus() != githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE {
			if resp.GetStatus() == githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS {
				s.cache.Set(strings.TrimSpace(req.GetIdempotencyKey()), resp)
			}
			return resp, nil
		}

		lastResp = resp
		delay := s.backoffDuration(policy, attempts)
		if attempts >= policy.maxAttempts {
			break
		}
		log.Printf("correlation_id=%s temporary publish failure, retry in %s (attempt %d/%d)", correlationID, delay, attempts, policy.maxAttempts)
		select {
		case <-ctx.Done():
			resp.RetryAfter = durationpb.New(delay)
			return resp, nil
		case <-time.After(delay):
		}
	}

	if lastResp != nil {
		lastResp.RetryAfter = durationpb.New(s.backoffDuration(policy, attempts+1))
		return lastResp, nil
	}

	return &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_PERMANENT_FAILURE, ErrorCode: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_UNKNOWN, ErrorMessage: "publish failed without response"}, nil
}

type publishAttempt struct {
	client        *githubclient.Client
	req           *githubpublisherv1.PublishMarkdownRequest
	branch        string
	baseBranch    string
	content       string
	correlationID string
	commitMessage string
}

func (s *PublisherServer) publishOnce(ctx context.Context, attempt publishAttempt) *githubpublisherv1.PublishMarkdownResponse {
	commitSHA, err := attempt.client.CommitMarkdown(ctx, attempt.req.GetPath(), attempt.content, attempt.commitMessage, attempt.branch, attempt.correlationID, attempt.req.GetAuthorName(), attempt.req.GetAuthorEmail())
	if err != nil {
		log.Printf("correlation_id=%s commit markdown error: %v", attempt.correlationID, err)
		statusValue, code, message := classifyPublishError(err)
		return &githubpublisherv1.PublishMarkdownResponse{
			Status:       statusValue,
			ErrorCode:    code,
			ErrorMessage: message,
		}
	}

	var prNumber int64
	var prURL string
	if attempt.req.GetCreatePr() {
		prNumber, prURL, err = attempt.client.CreatePullRequest(ctx, attempt.branch, attempt.baseBranch, attempt.req.GetPrTitle(), attempt.req.GetPrBody(), attempt.correlationID)
		if err != nil {
			log.Printf("correlation_id=%s create pr error: %v", attempt.correlationID, err)
			statusValue, code, message := classifyPublishError(err)
			return &githubpublisherv1.PublishMarkdownResponse{
				Status:       statusValue,
				ErrorCode:    code,
				ErrorMessage: message,
				CommitSha:    commitSHA,
			}
		}
	}

	log.Printf("correlation_id=%s published commit=%s pr=%d", attempt.correlationID, commitSHA, prNumber)

	return &githubpublisherv1.PublishMarkdownResponse{
		Status:            githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS,
		CommitSha:         commitSHA,
		PullRequestNumber: prNumber,
		PullRequestUrl:    prURL,
	}
}

func defaultCommitMessage(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "Update markdown content"
	}
	return "Update " + path
}

type retryPolicy struct {
	initialBackoff time.Duration
	maxBackoff     time.Duration
	maxAttempts    int
}

func (s *PublisherServer) resolveRetryPolicy(policy *githubpublisherv1.RetryPolicy) retryPolicy {
	defaultInitial := s.cfg.Retry.InitialBackoff
	if defaultInitial <= 0 {
		defaultInitial = 5 * time.Second
	}
	defaultMax := s.cfg.Retry.MaxBackoff
	if defaultMax <= 0 {
		defaultMax = 30 * time.Second
	}
	defaultAttempts := s.cfg.Retry.MaxAttempts
	if defaultAttempts <= 0 {
		defaultAttempts = 3
	}

	resolved := retryPolicy{
		initialBackoff: defaultInitial,
		maxBackoff:     defaultMax,
		maxAttempts:    defaultAttempts,
	}

	if policy != nil {
		if policy.GetInitialBackoff().AsDuration() > 0 {
			resolved.initialBackoff = policy.GetInitialBackoff().AsDuration()
		}
		if policy.GetMaxBackoff().AsDuration() > 0 {
			resolved.maxBackoff = policy.GetMaxBackoff().AsDuration()
		}
		if policy.GetMaxAttempts() > 0 {
			resolved.maxAttempts = int(policy.GetMaxAttempts())
		}
	}

	if resolved.maxAttempts <= 0 {
		resolved.maxAttempts = defaultAttempts
	}
	if resolved.initialBackoff <= 0 {
		resolved.initialBackoff = defaultInitial
	}
	if resolved.maxBackoff <= 0 {
		resolved.maxBackoff = defaultMax
	}

	return resolved
}

func (s *PublisherServer) backoffDuration(policy retryPolicy, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := policy.initialBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= policy.maxBackoff {
			return policy.maxBackoff
		}
	}

	if delay > policy.maxBackoff {
		return policy.maxBackoff
	}
	return delay
}

func (s *PublisherServer) pickCorrelationID(ctx context.Context, explicit string) string {
	if explicit != "" {
		return explicit
	}
	header := s.cfg.GitHub.CorrelationHeader
	if header == "" {
		header = "x-correlation-id"
	}
	header = strings.ToLower(header)

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for key, values := range md {
			if strings.ToLower(key) == header && len(values) > 0 {
				return values[0]
			}
		}
	}
	return ""
}

func classifyPublishError(err error) (githubpublisherv1.PublishResultStatus, githubpublisherv1.PublishErrorCode, string) {
	if err == nil {
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_UNSPECIFIED, ""
	}

	if errors.Is(err, context.DeadlineExceeded) { //nolint:errorlint
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT, err.Error()
	}

	var apiErr *githubclient.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden:
			return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_PERMANENT_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_PERMISSION, apiErr.Error()
		case apiErr.StatusCode == http.StatusNotFound:
			return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_PERMANENT_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NOT_FOUND, apiErr.Error()
		case apiErr.StatusCode == http.StatusConflict:
			return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_PERMANENT_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_CONFLICT, apiErr.Error()
		case apiErr.StatusCode == http.StatusTooManyRequests:
			return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_RATE_LIMIT, apiErr.Error()
		case apiErr.StatusCode >= 500:
			return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_SERVER_ERROR, apiErr.Error()
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT, err.Error()
		}
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NETWORK, err.Error()
	}

	lowerMsg := strings.ToLower(err.Error())
	if strings.Contains(lowerMsg, "rate limit") {
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_RATE_LIMIT, err.Error()
	}
	if strings.Contains(lowerMsg, "timeout") || strings.Contains(lowerMsg, "deadline") {
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT, err.Error()
	}
	if strings.Contains(lowerMsg, "connection reset") || strings.Contains(lowerMsg, "connection refused") || strings.Contains(lowerMsg, "network is unreachable") {
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NETWORK, err.Error()
	}

	return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_PERMANENT_FAILURE, githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_UNKNOWN, err.Error()
}

type idempotencyCache struct {
	mu    sync.RWMutex
	items map[string]*githubpublisherv1.PublishMarkdownResponse
}

func newIdempotencyCache() *idempotencyCache {
	return &idempotencyCache{items: make(map[string]*githubpublisherv1.PublishMarkdownResponse)}
}

func (c *idempotencyCache) Get(key string) (*githubpublisherv1.PublishMarkdownResponse, bool) {
	if key == "" {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	resp, ok := c.items[key]
	if !ok {
		return nil, false
	}
	return resp, true
}

func (c *idempotencyCache) Set(key string, resp *githubpublisherv1.PublishMarkdownResponse) {
	if key == "" || resp == nil || resp.GetStatus() != githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = resp
}
