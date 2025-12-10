package service

import (
	"context"
	"log"
	"strings"

	"backend-github-publisher-service/internal/config"
	githubclient "backend-github-publisher-service/internal/github"
	"backend-github-publisher-service/internal/storage"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// PublisherServer реализует gRPC слой поверх GitHub клиента.
type PublisherServer struct {
	githubpublisherv1.UnimplementedGithubPublisherServiceServer

	httpClient githubclient.HTTPClient
	storage    storage.MarkdownLoader
	cfg        *config.Config
}

// NewPublisherServer создаёт сервер публикации.
func NewPublisherServer(cfg *config.Config, httpClient githubclient.HTTPClient, storage storage.MarkdownLoader) *PublisherServer {
	return &PublisherServer{cfg: cfg, httpClient: httpClient, storage: storage}
}

// PublishMarkdown сохраняет Markdown в GitHub и, при необходимости, открывает Pull Request.
func (s *PublisherServer) PublishMarkdown(ctx context.Context, req *githubpublisherv1.PublishMarkdownRequest) (*githubpublisherv1.PublishMarkdownResponse, error) {
	if req.GetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}
	if req.GetCommitMessage() == "" {
		return nil, status.Error(codes.InvalidArgument, "commit message is required")
	}

	correlationID := s.pickCorrelationID(ctx, req.GetCorrelationId())

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
		content = string(data)
	}

	branch := req.GetBranch()
	baseBranch := req.GetBaseBranch()
	if branch == "" {
		branch = s.cfg.GitHub.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = s.cfg.GitHub.DefaultBranch
	}

	client := githubclient.NewFromConfig(
		s.httpClient,
		s.cfg.GitHub,
		owner,
		repository,
	)

	commitSHA, err := client.CommitMarkdown(ctx, req.GetPath(), content, req.GetCommitMessage(), branch, correlationID, req.GetAuthorName(), req.GetAuthorEmail())
	if err != nil {
		log.Printf("correlation_id=%s commit markdown error: %v", correlationID, err)
		return nil, status.Errorf(codes.FailedPrecondition, "commit markdown: %v", err)
	}

	var prNumber int64
	var prURL string
	if req.GetCreatePr() {
		prNumber, prURL, err = client.CreatePullRequest(ctx, branch, baseBranch, req.GetPrTitle(), req.GetPrBody(), correlationID)
		if err != nil {
			log.Printf("correlation_id=%s create pr error: %v", correlationID, err)
			return nil, status.Errorf(codes.FailedPrecondition, "create pull request: %v", err)
		}
	}

	resp := &githubpublisherv1.PublishMarkdownResponse{
		CommitSha:         commitSHA,
		PullRequestNumber: prNumber,
		PullRequestUrl:    prURL,
	}
	log.Printf("correlation_id=%s published commit=%s pr=%d", correlationID, commitSHA, prNumber)

	return resp, nil
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
