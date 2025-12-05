package document

import (
	"context"
	"fmt"
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
	OriginalBucket    string
	MarkdownBucket    string
	OriginalObjectKey string
	MarkdownObjectKey string
	FilenameExt       string
	PublishPath       string
	OriginalContent   []byte
	Version           int32
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
	DocumentID      string
	MarkdownContent []byte
	Version         int32
	Path            string
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
	}
	if err := workflow.ExecuteActivity(ctx, ActivityUploadOriginalName, uploadArg).Get(ctx, nil); err != nil {
		return err
	}

	convArg := convertInput{
		DocumentID:        input.DocumentID,
		OriginalBucket:    input.OriginalBucket,
		MarkdownBucket:    input.MarkdownBucket,
		OriginalObjectKey: input.OriginalObjectKey,
		MarkdownObjectKey: input.MarkdownObjectKey,
		Version:           uploadArg.Version + 1,
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
	_, err = a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusStoredOriginal, "", &model.DocumentVersion{Version: input.Version})
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

	if _, err := a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusConvertedToMarkdown, "", &model.DocumentVersion{Version: input.Version, MarkdownObjectKey: resp.MarkdownObjectKey}); err != nil {
		return convertResult{}, err
	}
	return convertResult{MarkdownContent: resp.MarkdownContent, MarkdownKey: resp.MarkdownObjectKey, MediaType: resp.MediaType}, nil
}

func (a *Activities) PublishMarkdown(ctx context.Context, input publishInput) error {
	ctx = a.withCorrelationID(ctx)
	if a.publisher == nil {
		return fmt.Errorf("publisher client is not configured")
	}
	_, err := a.publisher.PublishMarkdown(ctx, &githubpublisherv1.PublishMarkdownRequest{
		Repository: a.repoName,
		Branch:     a.branch,
		Path:       input.Path,
		Content:    input.MarkdownContent,
	})
	if err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("publish: %v", err))
		return err
	}
	_, err = a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusPublishedToGitHub, "", &model.DocumentVersion{Version: input.Version})
	return err
}

func (a *Activities) fail(ctx context.Context, documentID, reason string) error {
	_, err := a.repo.UpdateStatus(ctx, documentID, model.StatusFailed, reason, nil)
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
