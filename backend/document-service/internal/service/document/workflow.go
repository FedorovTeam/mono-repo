package document

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc/metadata"
)

const (
	ActivityUploadOriginalName    = "document.uploadOriginal"
	ActivityConvertToMarkdownName = "document.convertToMarkdown"
	ActivityPublishMarkdownName   = "document.publishMarkdown"

	correlationIDHeader = "x-correlation-id"
)

type WorkflowStarter interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
}

type WorkflowInput struct {
	DocumentID        string
	DocumentName      string
	Owner             string
	Repository        string
	Branch            string
	BaseBranch        string
	OriginalBucket    string
	MarkdownBucket    string
	OriginalObjectKey string
	MarkdownObjectKey string
	FilenameExt       string
	PublishPath       string
	Path              string
	OriginalContent   []byte
	Version           int32
	CreatePR          bool
}

type uploadOriginalInput struct {
	DocumentID      string
	FilenameExt     string
	OriginalContent []byte
	Version         int32
}

type convertInput struct {
	DocumentID        string
	OriginalBucket    string
	MarkdownBucket    string
	OriginalObjectKey string
	MarkdownObjectKey string
	Version           int32
}

type publishInput struct {
	DocumentID        string
	DocumentName      string
	Owner             string
	MarkdownContent   []byte
	Version           int32
	MarkdownBucket    string
	MarkdownObjectKey string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	CreatePR          bool
}

type convertResult struct {
	MarkdownContent []byte
	MarkdownKey     string
	MediaType       string
}

type Activities struct {
	repo        Repository
	storage     storagev1.StorageServiceClient
	transformer transformerv1.TransformerServiceClient
	publisher   githubpublisherv1.GithubPublisherServiceClient
	repoOwner   string
	repoName    string
	branch      string
	baseBranch  string
	baseURL     string
	httpClient  *http.Client
}

func NewActivities(repo Repository, storageClient storagev1.StorageServiceClient, transformerClient transformerv1.TransformerServiceClient, publisherClient githubpublisherv1.GithubPublisherServiceClient, repoOwner, repoName, branch, baseBranch, baseURL string) *Activities {
	return &Activities{
		repo:        repo,
		storage:     storageClient,
		transformer: transformerClient,
		publisher:   publisherClient,
		repoOwner:   repoOwner,
		repoName:    repoName,
		branch:      branch,
		baseBranch:  baseBranch,
		baseURL:     baseURL,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

func DocumentProcessingWorkflow(ctx workflow.Context, input WorkflowInput) error {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	currentVersion := input.Version

	// 👇 Если байты есть — сами кладём оригинал в MinIO
	if len(input.OriginalContent) > 0 {
		uploadArg := uploadOriginalInput{
			DocumentID:      input.DocumentID,
			FilenameExt:     input.FilenameExt,
			OriginalContent: input.OriginalContent,
			Version:         currentVersion + 1,
		}

		if err := workflow.ExecuteActivity(ctx, ActivityUploadOriginalName, uploadArg).Get(ctx, nil); err != nil {
			return err
		}

		currentVersion = uploadArg.Version
	}

	convArg := convertInput{
		DocumentID:        input.DocumentID,
		OriginalBucket:    input.OriginalBucket,
		MarkdownBucket:    input.MarkdownBucket,
		OriginalObjectKey: input.OriginalObjectKey,
		MarkdownObjectKey: input.MarkdownObjectKey,
		Version:           currentVersion + 1,
	}

	var convRes convertResult
	if err := workflow.ExecuteActivity(ctx, ActivityConvertToMarkdownName, convArg).Get(ctx, &convRes); err != nil {
		return err
	}

	markdownKey := convRes.MarkdownKey
	if markdownKey == "" {
		markdownKey = input.MarkdownObjectKey
	}

	pubArg := publishInput{
		DocumentID:        input.DocumentID,
		DocumentName:      input.DocumentName,
		Owner:             input.Owner,
		MarkdownContent:   convRes.MarkdownContent,
		MarkdownBucket:    input.MarkdownBucket,
		MarkdownObjectKey: markdownKey,
		Version:           convArg.Version + 1,
		Repository:        input.Repository,
		Branch:            input.Branch,
		BaseBranch:        input.BaseBranch,
		Path:              input.PublishPath,
		CreatePR:          input.CreatePR,
	}

	if err := workflow.ExecuteActivity(ctx, ActivityPublishMarkdownName, pubArg).Get(ctx, nil); err != nil {
		return err
	}

	return nil
}

func (a *Activities) UploadOriginal(ctx context.Context, input uploadOriginalInput) error {
	ctx = a.withCorrelationID(ctx)
	if a.storage == nil {
		return fmt.Errorf("storage client is not configured")
	}
	_, err := a.storage.UploadOriginal(ctx, &storagev1.UploadOriginalRequest{
		DocumentId:        input.DocumentID,
		FilenameExtension: input.FilenameExt,
		Content:           input.OriginalContent,
	})
	if err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("upload original: %v", err))
		return err
	}
	_, err = a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusStoredOriginal, "", "", &model.DocumentVersion{Version: input.Version})
	return err
}

func (a *Activities) ConvertToMarkdown(ctx context.Context, input convertInput) (convertResult, error) {
	ctx = a.withCorrelationID(ctx)
	if a.transformer == nil {
		return convertResult{}, fmt.Errorf("transformer client is not configured")
	}
	resp, err := a.transformer.ConvertDocxToMarkdown(ctx, &transformerv1.ConvertDocxToMarkdownRequest{
		DocumentId:        input.DocumentID,
		OriginalBucket:    input.OriginalBucket,
		OriginalObjectKey: input.OriginalObjectKey,
		MarkdownBucket:    input.MarkdownBucket,
		MarkdownObjectKey: input.MarkdownObjectKey,
	})
	if err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("convert: %v", err))
		return convertResult{}, err
	}

	if _, err := a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusConvertedToMarkdown, "", "", &model.DocumentVersion{Version: input.Version, MarkdownObjectKey: resp.MarkdownObjectKey}); err != nil {
		return convertResult{}, err
	}
	return convertResult{MarkdownContent: resp.MarkdownContent, MarkdownKey: resp.MarkdownObjectKey, MediaType: resp.MediaType}, nil
}

func (a *Activities) PublishMarkdown(ctx context.Context, input publishInput) error {
	ctx = a.withCorrelationID(ctx)
	if a.publisher == nil {
		return fmt.Errorf("publisher client is not configured")
	}
	owner := input.Owner
	if owner == "" {
		owner = a.repoOwner
	}
	repository := input.Repository
	if repository == "" {
		repository = a.repoName
	}
	branch := input.Branch
	if branch == "" {
		branch = a.branch
	}
	baseBranch := input.BaseBranch
	if baseBranch == "" {
		baseBranch = a.baseBranch
	}
	if baseBranch == "" {
		baseBranch = branch
	}
	path := input.Path

	correlationID := ""
	if info, ok := activityInfo(ctx); ok {
		correlationID = info.WorkflowExecution.ID
	}

	commitMessage := input.DocumentName
	if commitMessage == "" {
		commitMessage = path
	}

	if err := a.validateRepository(ctx, owner, repository); err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("validate repository: %v", err))
		return err
	}

	req := &githubpublisherv1.PublishMarkdownRequest{
		Owner:         owner,
		Repository:    repository,
		Branch:        branch,
		BaseBranch:    baseBranch,
		Path:          path,
		CommitMessage: commitMessage,
		CreatePr:      input.CreatePR,
		CorrelationId: correlationID,
	}

	if len(input.MarkdownContent) > 0 {
		req.Content = string(input.MarkdownContent)
	} else {
		req.Bucket = input.MarkdownBucket
		req.ObjectKey = input.MarkdownObjectKey
		if req.Bucket == "" || req.ObjectKey == "" {
			return fmt.Errorf("markdown content is empty and no bucket/object_key provided")
		}
	}
	_, err := a.publisher.PublishMarkdown(ctx, req)
	if err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("publish: %v", err))
		return err
	}
	_, err = a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusPublishedToGitHub, "", "", &model.DocumentVersion{Version: input.Version})
	return err
}

func (a *Activities) fail(ctx context.Context, documentID, reason string) error {
	_, err := a.repo.UpdateStatus(ctx, documentID, model.StatusFailed, reason, reason, nil)
	return err
}

func (a *Activities) withCorrelationID(ctx context.Context) context.Context {
	info, ok := activityInfo(ctx)
	if !ok || info.WorkflowExecution.ID == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, correlationIDHeader, info.WorkflowExecution.ID)
}

func activityInfo(ctx context.Context) (activity.Info, bool) {
	defer func() {
		_ = recover()
	}()
	return activity.GetInfo(ctx), true
}

func (a *Activities) validateRepository(ctx context.Context, owner, repository string) error {
	client := a.httpClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	baseURL := strings.TrimSuffix(a.baseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/%s", baseURL, owner, repository), nil)
	if err != nil {
		return fmt.Errorf("build repository check request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github repository validation failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("repository %s/%s not found", owner, repository)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("no access to repository %s/%s: %s", owner, repository, resp.Status)
	default:
		return fmt.Errorf("github repository validation returned status %s", resp.Status)
	}
}
