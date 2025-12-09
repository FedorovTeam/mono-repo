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
	ActivityAwaitOriginalName     = "document.awaitOriginal"
	ActivityUploadOriginalName    = "document.uploadOriginal"
	ActivityConvertToMarkdownName = "document.convertToMarkdown"
	ActivityPublishMarkdownName   = "document.publishMarkdown"

	correlationIDHeader = "x-correlation-id"
)

type WorkflowStarter interface {
	ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error)
}

type WorkflowInput struct {
	DocumentID string
}

type documentSnapshot struct {
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
	PublishPath       string
	Path              string
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
	Version           int32
	MarkdownBucket    string
	MarkdownObjectKey string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	CreatePR          bool
	CorrelationID     string
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

	var snapshot documentSnapshot
	if err := workflow.ExecuteActivity(ctx, ActivityAwaitOriginalName, input.DocumentID).Get(ctx, &snapshot); err != nil {
		return err
	}

	convArg := convertInput{
		DocumentID:        snapshot.DocumentID,
		OriginalBucket:    snapshot.OriginalBucket,
		MarkdownBucket:    snapshot.MarkdownBucket,
		OriginalObjectKey: snapshot.OriginalObjectKey,
		MarkdownObjectKey: snapshot.MarkdownObjectKey,
		Version:           snapshot.Version + 1,
	}

	var convRes convertResult
	if err := workflow.ExecuteActivity(ctx, ActivityConvertToMarkdownName, convArg).Get(ctx, &convRes); err != nil {
		return err
	}

	markdownKey := convRes.MarkdownKey
	if markdownKey == "" {
		markdownKey = snapshot.MarkdownObjectKey
	}

	pubArg := publishInput{
		DocumentID:        snapshot.DocumentID,
		DocumentName:      snapshot.DocumentName,
		Owner:             snapshot.Owner,
		MarkdownBucket:    snapshot.MarkdownBucket,
		MarkdownObjectKey: markdownKey,
		Version:           convArg.Version + 1,
		Repository:        snapshot.Repository,
		Branch:            snapshot.Branch,
		BaseBranch:        snapshot.BaseBranch,
		Path:              snapshot.PublishPath,
		CreatePR:          snapshot.CreatePR,
		CorrelationID:     workflow.GetInfo(ctx).WorkflowExecution.ID,
	}

	if err := workflow.ExecuteActivity(ctx, ActivityPublishMarkdownName, pubArg).Get(ctx, nil); err != nil {
		return err
	}

	return nil
}

func (a *Activities) AwaitOriginal(ctx context.Context, documentID string) (documentSnapshot, error) {
	ctx = a.withCorrelationID(ctx, "")
	if a.storage == nil {
		return documentSnapshot{}, fmt.Errorf("storage client is not configured")
	}

	doc, err := a.repo.GetDocument(ctx, documentID)
	if err != nil {
		return documentSnapshot{}, err
	}
	if doc == nil {
		return documentSnapshot{}, fmt.Errorf("document %s not found", documentID)
	}
	if doc.OriginalBucket == "" || doc.OriginalObjectKey == "" {
		return documentSnapshot{}, fmt.Errorf("document %s has no original object key", documentID)
	}

	if _, err := a.storage.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: doc.OriginalBucket, ObjectKey: doc.OriginalObjectKey}); err != nil {
		_ = a.fail(ctx, documentID, fmt.Sprintf("wait original: %v", err))
		return documentSnapshot{}, err
	}

	version := int32(1)
	if len(doc.Versions) > 0 {
		version = doc.Versions[len(doc.Versions)-1].Version
	}

	if _, err := a.repo.UpdateStatus(ctx, documentID, model.StatusStoredOriginal, "", "", &model.DocumentVersion{Version: version, OriginalObjectKey: doc.OriginalObjectKey}); err != nil {
		return documentSnapshot{}, err
	}

	publishPath := doc.Path
	if publishPath == "" {
		publishPath = doc.Name
	}
	baseBranch := doc.BaseBranch
	if baseBranch == "" {
		baseBranch = doc.Branch
	}

	return documentSnapshot{
		DocumentID:        documentID,
		DocumentName:      doc.Name,
		Owner:             doc.Owner,
		Repository:        doc.Repository,
		Branch:            doc.Branch,
		BaseBranch:        baseBranch,
		OriginalBucket:    doc.OriginalBucket,
		MarkdownBucket:    doc.MarkdownBucket,
		OriginalObjectKey: doc.OriginalObjectKey,
		MarkdownObjectKey: doc.MarkdownObjectKey,
		PublishPath:       publishPath,
		Path:              doc.Path,
		Version:           version,
		CreatePR:          doc.CreatePR,
	}, nil
}

func (a *Activities) UploadOriginal(ctx context.Context, input uploadOriginalInput) error {
	ctx = a.withCorrelationID(ctx, "")
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
	ctx = a.withCorrelationID(ctx, "")
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
	ctx = a.withCorrelationID(ctx, input.CorrelationID)
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

	correlationID := input.CorrelationID
	if correlationID == "" {
		if info, ok := activityInfo(ctx); ok {
			correlationID = info.WorkflowExecution.ID
		}
	}

	commitMessage := input.DocumentName
	if commitMessage == "" {
		commitMessage = path
	}

	if err := a.validateRepository(ctx, owner, repository); err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("validate repository: %v", err))
		return err
	}

	if input.MarkdownBucket == "" || input.MarkdownObjectKey == "" {
		_ = a.fail(ctx, input.DocumentID, "markdown content is empty and no bucket/object_key provided")
		return fmt.Errorf("markdown content is empty and no bucket/object_key provided")
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
		Bucket:        input.MarkdownBucket,
		ObjectKey:     input.MarkdownObjectKey,
	}

	resp, err := a.publisher.PublishMarkdown(ctx, req)
	if err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("publish: %v", err))
		return err
	}
	if resp == nil || resp.CommitSha == "" {
		_ = a.fail(ctx, input.DocumentID, "publish: empty response")
		return fmt.Errorf("publish returned empty response")
	}
	_, err = a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusPublishedToGitHub, "", "", &model.DocumentVersion{Version: input.Version})
	return err
}

func (a *Activities) fail(ctx context.Context, documentID, reason string) error {
	_, err := a.repo.UpdateStatus(ctx, documentID, model.StatusFailed, reason, reason, nil)
	return err
}

func (a *Activities) withCorrelationID(ctx context.Context, explicit string) context.Context {
	id := explicit
	if id == "" {
		id = correlationIDFromContext(ctx)
	}
	if id == "" {
		if info, ok := activityInfo(ctx); ok {
			id = info.WorkflowExecution.ID
		}
	}
	if id == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, correlationIDHeader, id)
}

func activityInfo(ctx context.Context) (activity.Info, bool) {
	defer func() {
		_ = recover()
	}()
	return activity.GetInfo(ctx), true
}

func correlationIDFromContext(ctx context.Context) string {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		if vals := md.Get(correlationIDHeader); len(vals) > 0 {
			return vals[0]
		}
	}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(correlationIDHeader); len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
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
