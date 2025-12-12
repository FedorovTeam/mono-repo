package service

import (
	githubclient "backend-github-publisher-service/internal/infrastructure/github"
	"backend-github-publisher-service/internal/infrastructure/storage"
	"backend-github-publisher-service/pkg/config"
	"context"
	"strings"

	"backend-github-publisher-service/internal/domain"
	"backend-github-publisher-service/internal/usecase"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// PublisherServer реализует gRPC адаптер для use-case публикации.
type PublisherServer struct {
	githubpublisherv1.UnimplementedGithubPublisherServiceServer

	useCase           *usecase.PublishUseCase
	correlationHeader string
}

// NewPublisherServer создаёт сервер публикации.
func NewPublisherServer(cfg *config.Config, httpClient githubclient.HTTPClient, storage storage.MarkdownLoader) *PublisherServer {
	retryResolver := usecase.NewRetryPolicyResolver(domain.RetryPolicy{InitialBackoff: cfg.Retry.InitialBackoff, MaxBackoff: cfg.Retry.MaxBackoff, MaxAttempts: cfg.Retry.MaxAttempts})
	idempotency := usecase.NewMemoryIdempotencyStore()
	publisher := NewGitHubClientPublisher(cfg, httpClient)
	uc := usecase.NewPublishUseCase(publisher, storage, idempotency, retryResolver, cfg.GitHub.Owner, cfg.GitHub.Repository, cfg.GitHub.DefaultBranch)

	header := cfg.GitHub.CorrelationHeader
	if header == "" {
		header = "x-correlation-id"
	}

	return &PublisherServer{useCase: uc, correlationHeader: strings.ToLower(header)}
}

// PublishMarkdown переводит gRPC-запрос в доменную команду и возвращает результат.
func (s *PublisherServer) PublishMarkdown(ctx context.Context, req *githubpublisherv1.PublishMarkdownRequest) (*githubpublisherv1.PublishMarkdownResponse, error) {
	correlationID := s.pickCorrelationID(ctx, req.GetCorrelationId())

	cmd := domain.PublishCommand{
		Owner:          req.GetOwner(),
		Repository:     req.GetRepository(),
		Branch:         req.GetBranch(),
		BaseBranch:     req.GetBaseBranch(),
		Path:           req.GetPath(),
		Content:        req.GetContent(),
		CommitMessage:  req.GetCommitMessage(),
		PrTitle:        req.GetPrTitle(),
		PrBody:         req.GetPrBody(),
		CreatePR:       req.GetCreatePr(),
		CorrelationID:  correlationID,
		AuthorName:     req.GetAuthorName(),
		AuthorEmail:    req.GetAuthorEmail(),
		Bucket:         req.GetBucket(),
		ObjectKey:      req.GetObjectKey(),
		IdempotencyKey: req.GetIdempotencyKey(),
		Attempt:        req.GetAttempt(),
		RetryPolicy:    toDomainRetryPolicy(req.GetRetryPolicy()),
	}

	result, err := s.useCase.Execute(ctx, cmd)
	if err != nil {
		switch err.(type) {
		case *domain.ValidationError:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case *domain.PreconditionError:
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return toProtoResponse(result), nil
}

func (s *PublisherServer) pickCorrelationID(ctx context.Context, explicit string) string {
	if explicit != "" {
		return explicit
	}

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for key, values := range md {
			if strings.ToLower(key) == s.correlationHeader && len(values) > 0 {
				return values[0]
			}
		}
	}
	return ""
}

func toDomainRetryPolicy(policy *githubpublisherv1.RetryPolicy) *domain.RetryPolicy {
	if policy == nil {
		return nil
	}
	return &domain.RetryPolicy{
		InitialBackoff: policy.GetInitialBackoff().AsDuration(),
		MaxBackoff:     policy.GetMaxBackoff().AsDuration(),
		MaxAttempts:    int(policy.GetMaxAttempts()),
	}
}

func toProtoResponse(result *domain.PublishResult) *githubpublisherv1.PublishMarkdownResponse {
	resp := &githubpublisherv1.PublishMarkdownResponse{
		Status:            toProtoStatus(result.Status),
		CommitSha:         result.CommitSHA,
		PullRequestNumber: result.PullRequestNumber,
		PullRequestUrl:    result.PullRequestURL,
		ErrorCode:         toProtoErrorCode(result.ErrorCode),
		ErrorMessage:      result.ErrorMessage,
		IdempotencyHit:    result.IdempotencyHit,
	}

	if result.RetryAfter > 0 {
		resp.RetryAfter = durationpb.New(result.RetryAfter)
	}

	return resp
}

func toProtoStatus(status domain.PublishStatus) githubpublisherv1.PublishResultStatus {
	switch status {
	case domain.PublishStatusSuccess:
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS
	case domain.PublishStatusTemporaryFailure:
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE
	case domain.PublishStatusPermanentFailure:
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_PERMANENT_FAILURE
	default:
		return githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_UNSPECIFIED
	}
}

func toProtoErrorCode(code domain.PublishErrorCode) githubpublisherv1.PublishErrorCode {
	switch code {
	case domain.PublishErrorCodeTimeout:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT
	case domain.PublishErrorCodeServerError:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_SERVER_ERROR
	case domain.PublishErrorCodeRateLimit:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_RATE_LIMIT
	case domain.PublishErrorCodeConflict:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_CONFLICT
	case domain.PublishErrorCodePermission:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_PERMISSION
	case domain.PublishErrorCodeNetwork:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NETWORK
	case domain.PublishErrorCodeNotFound:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NOT_FOUND
	case domain.PublishErrorCodeUnknown:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_UNKNOWN
	default:
		return githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_UNSPECIFIED
	}
}
