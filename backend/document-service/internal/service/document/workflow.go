package document

import (
	"context"
	"fmt"
	"time"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc/metadata"
)

const (
	ActivityUploadOriginalName    = "document.uploadOriginal"
	ActivityConvertToMarkdownName = "document.convertToMarkdown"
	ActivityPublishMarkdownName   = "document.publishMarkdown"
	CorrelationIDHeader           = "x-correlation-id"
)

type WorkflowStarter interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
}

type WorkflowInput struct {
	DocumentID        string
	OriginalBucket    string
	MarkdownBucket    string
	OriginalObjectKey string
	FilenameExt       string
	PublishPath       string
	OriginalContent   []byte
	Version           int32
	CorrelationID     string
}

type uploadOriginalInput struct {
	DocumentID      string
	FilenameExt     string
	OriginalContent []byte
	Version         int32
	CorrelationID   string
}

type convertInput struct {
	DocumentID      string
	FilenameExt     string
	OriginalContent []byte
	Version         int32
	CorrelationID   string
}

type publishInput struct {
	DocumentID      string
	MarkdownContent []byte
	Version         int32
	Path            string
	CorrelationID   string
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
	repoName    string
	branch      string
}

func NewActivities(repo Repository, storageClient storagev1.StorageServiceClient, transformerClient transformerv1.TransformerServiceClient, publisherClient githubpublisherv1.GithubPublisherServiceClient, repoName, branch string) *Activities {
	return &Activities{
		repo:        repo,
		storage:     storageClient,
		transformer: transformerClient,
		publisher:   publisherClient,
		repoName:    repoName,
		branch:      branch,
	}
}

func DocumentProcessingWorkflow(ctx workflow.Context, input WorkflowInput) error {
	correlationID := input.CorrelationID
	if correlationID == "" {
		correlationID = CorrelationIDFromWorkflowContext(ctx)
	}
	if correlationID == "" {
		correlationID = workflow.GetInfo(ctx).WorkflowExecution.ID
	}
	ctx = WithWorkflowCorrelationID(ctx, correlationID)

	options := workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute * 2,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	uploadArg := uploadOriginalInput{
		DocumentID:      input.DocumentID,
		FilenameExt:     input.FilenameExt,
		OriginalContent: input.OriginalContent,
		Version:         input.Version + 1,
		CorrelationID:   correlationID,
	}
	if err := workflow.ExecuteActivity(ctx, ActivityUploadOriginalName, uploadArg).Get(ctx, nil); err != nil {
		return err
	}

	convArg := convertInput{
		DocumentID:      input.DocumentID,
		FilenameExt:     input.FilenameExt,
		OriginalContent: input.OriginalContent,
		Version:         uploadArg.Version + 1,
		CorrelationID:   correlationID,
	}
	var convRes convertResult
	if err := workflow.ExecuteActivity(ctx, ActivityConvertToMarkdownName, convArg).Get(ctx, &convRes); err != nil {
		return err
	}

	pubArg := publishInput{
		DocumentID:      input.DocumentID,
		MarkdownContent: convRes.MarkdownContent,
		Version:         convArg.Version + 1,
		Path:            input.PublishPath,
		CorrelationID:   correlationID,
	}
	if err := workflow.ExecuteActivity(ctx, ActivityPublishMarkdownName, pubArg).Get(ctx, nil); err != nil {
		return err
	}
	return nil
}

func (a *Activities) UploadOriginal(ctx context.Context, input uploadOriginalInput) error {
	if a.storage == nil {
		return fmt.Errorf("storage client is not configured")
	}
	corrCtx := appendCorrelationID(ctx, input.CorrelationID)
	_, err := a.storage.UploadOriginal(corrCtx, &storagev1.UploadOriginalRequest{
		DocumentId:        input.DocumentID,
		FilenameExtension: input.FilenameExt,
		Content:           input.OriginalContent,
	})
	if err != nil {
		_ = a.fail(corrCtx, input.DocumentID, fmt.Sprintf("upload original: %v", err))
		return err
	}
	_, err = a.repo.UpdateStatus(corrCtx, input.DocumentID, model.StatusStoredOriginal, "", &model.DocumentVersion{Version: input.Version})
	return err
}

func (a *Activities) ConvertToMarkdown(ctx context.Context, input convertInput) (convertResult, error) {
	if a.transformer == nil || a.storage == nil {
		return convertResult{}, fmt.Errorf("transformer or storage client is not configured")
	}
	corrCtx := appendCorrelationID(ctx, input.CorrelationID)
	resp, err := a.transformer.ConvertDocxToMarkdown(corrCtx, &transformerv1.ConvertDocxToMarkdownRequest{
		Content:           input.OriginalContent,
		FilenameExtension: input.FilenameExt,
	})
	if err != nil {
		_ = a.fail(corrCtx, input.DocumentID, fmt.Sprintf("convert: %v", err))
		return convertResult{}, err
	}
	upload, err := a.storage.UploadMarkdown(corrCtx, &storagev1.UploadMarkdownRequest{
		DocumentId: input.DocumentID,
		Content:    resp.MarkdownContent,
	})
	if err != nil {
		_ = a.fail(corrCtx, input.DocumentID, fmt.Sprintf("upload markdown: %v", err))
		return convertResult{}, err
	}
	if _, err := a.repo.UpdateStatus(corrCtx, input.DocumentID, model.StatusConvertedToMarkdown, "", &model.DocumentVersion{Version: input.Version, MarkdownObjectKey: upload.ObjectKey}); err != nil {
		return convertResult{}, err
	}
	return convertResult{MarkdownContent: resp.MarkdownContent, MarkdownKey: upload.ObjectKey, MediaType: resp.MediaType}, nil
}

func (a *Activities) PublishMarkdown(ctx context.Context, input publishInput) error {
	if a.publisher == nil {
		return fmt.Errorf("publisher client is not configured")
	}
	corrCtx := appendCorrelationID(ctx, input.CorrelationID)
	_, err := a.publisher.PublishMarkdown(corrCtx, &githubpublisherv1.PublishMarkdownRequest{
		Repository: a.repoName,
		Branch:     a.branch,
		Path:       input.Path,
		Content:    input.MarkdownContent,
	})
	if err != nil {
		_ = a.fail(corrCtx, input.DocumentID, fmt.Sprintf("publish: %v", err))
		return err
	}
	_, err = a.repo.UpdateStatus(corrCtx, input.DocumentID, model.StatusPublishedToGitHub, "", &model.DocumentVersion{Version: input.Version})
	return err
}

func (a *Activities) fail(ctx context.Context, documentID, reason string) error {
	_, err := a.repo.UpdateStatus(ctx, documentID, model.StatusFailed, reason, nil)
	return err
}

func appendCorrelationID(ctx context.Context, fallback string) context.Context {
	correlationID := CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = fallback
	}
	if correlationID != "" {
		ctx = ContextWithCorrelationID(ctx, correlationID)
		return metadata.AppendToOutgoingContext(ctx, CorrelationIDHeader, correlationID)
	}
	return ctx
}
