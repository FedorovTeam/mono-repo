package service

import (
	"context"
	"strings"

	"backend-github-publisher-service/internal/config"
	githubclient "backend-github-publisher-service/internal/github"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// PublisherServer реализует gRPC слой поверх GitHub клиента.
type PublisherServer struct {
	githubpublisherv1.UnimplementedGithubPublisherServiceServer

	httpClient githubclient.HTTPClient
	cfg        *config.Config
}

// NewPublisherServer создаёт сервер публикации.
func NewPublisherServer(cfg *config.Config, httpClient githubclient.HTTPClient) *PublisherServer {
	return &PublisherServer{cfg: cfg, httpClient: httpClient}
}

// PublishMarkdown сохраняет Markdown в GitHub и, при необходимости, открывает Pull Request.
func (s *PublisherServer) PublishMarkdown(ctx context.Context, req *githubpublisherv1.PublishMarkdownRequest) (*githubpublisherv1.PublishMarkdownResponse, error) {
	if req.GetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "path is required")
	}
	if req.GetContent() == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}
	if req.GetCommitMessage() == "" {
		return nil, status.Error(codes.InvalidArgument, "commit message is required")
	}

	owner := req.GetOwner()
	if owner == "" {
		owner = s.cfg.GitHub.Owner
	}
	repository := req.GetRepository()
	if repository == "" {
		repository = s.cfg.GitHub.Repository
	}

	correlationID := s.pickCorrelationID(ctx, req.GetCorrelationId())

	branch := req.GetBranch()
	baseBranch := req.GetBaseBranch()
	if branch == "" {
		branch = s.cfg.GitHub.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = s.cfg.GitHub.DefaultBranch
	}

	client := githubclient.New(
		s.httpClient,
		s.cfg.GitHub.BaseURL,
		s.cfg.GitHub.Token,
		owner,
		repository,
		s.cfg.GitHub.DefaultBranch,
		s.cfg.GitHub.CorrelationHeader,
		s.cfg.GitHub.AuthorName,
		s.cfg.GitHub.AuthorEmail,
	)

	commitSHA, err := client.CommitMarkdown(ctx, req.GetPath(), req.GetContent(), req.GetCommitMessage(), branch, correlationID, req.GetAuthorName(), req.GetAuthorEmail())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "commit markdown: %v", err)
	}

	var prNumber int64
	var prURL string
	if req.GetCreatePr() {
		prNumber, prURL, err = client.CreatePullRequest(ctx, branch, baseBranch, req.GetPrTitle(), req.GetPrBody(), correlationID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "create pull request: %v", err)
		}
	}

	return &githubpublisherv1.PublishMarkdownResponse{
		CommitSha:         commitSHA,
		PullRequestNumber: prNumber,
		PullRequestUrl:    prURL,
	}, nil
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
