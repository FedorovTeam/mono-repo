package document

import (
	"context"
	"fmt"
	"log"
	"path"
	"regexp"
	"strings"
	"time"

	"backend-document-service/internal/domain"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	preprocessv1 "backend-document-service/proto/backend/preprocess/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	tableextractorv1 "backend-document-service/proto/backend/tableextractor/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	publishGithubReasonPrefix    = "publish_github_"
	publishGithubTimeoutReason   = publishGithubReasonPrefix + "timeout"
	publishGithub5xxReason       = publishGithubReasonPrefix + "5xx"
	publishGithubConflictReason  = publishGithubReasonPrefix + "conflict"
	publishGithubRateLimitReason = publishGithubReasonPrefix + "rate_limit"
	publishGithubNetworkReason   = publishGithubReasonPrefix + "network"
	publishGithubPermanentReason = publishGithubReasonPrefix + "permanent"
	publishGithubUnknownReason   = publishGithubReasonPrefix + "unknown"

	correlationIDHeader = "x-correlation-id"
)

type preprocessInput struct {
	JobID           string
	DocumentID      string
	SourceBucket    string
	SourceObjectKey string
	SourceETag      string
	TargetBucket    string
	TargetObjectKey string
	CorrelationID   string
}

type preprocessResult struct {
	JobID           string
	Status          preprocessv1.PreprocessStatus
	TargetBucket    string
	TargetObjectKey string
	ErrorMessage    string
	ErrorCode       string
	CorrelationID   string
	UpdatedAt       time.Time
}

type convertInput struct {
	DocumentID        string
	OriginalBucket    string
	MarkdownBucket    string
	OriginalObjectKey string
	MarkdownObjectKey string
	Version           int32
	FilenameExtension string
}

type tableExtractionInput struct {
	DocumentID              string
	OriginalBucket          string
	OriginalObjectKey       string
	ArtifactsBucket         string
	ArtifactsPrefix         string
	ArtifactsPrefixTemplate string
	MarkdownBucket          string
	MarkdownObjectKey       string
	CorrelationID           string
}

type tableExtractionResult struct {
	QueueID           string
	Status            tableextractorv1.TableExtractionStatus
	StatusReason      string
	ArtifactsBucket   string
	ArtifactsPrefix   string
	MarkdownBucket    string
	MarkdownObjectKey string
	CorrelationID     string
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
	IdempotencyKey    string
	CommitMessage     string
	AuthorName        string
	AuthorEmail       string
	PRTitle           string
	PRBody            string
}

type convertResult struct {
	MarkdownKey   string
	QueueID       string
	Status        string
	Version       int32
	Ready         bool
	TableCount    int32
	TablePages    []int32
	TableMetadata domain.TableMetadata
}

type Activities struct {
	repo           Repository
	storage        storagev1.StorageServiceClient
	statuses       *StatusApplication
	preprocess     preprocessv1.PreprocessServiceClient
	tableExtractor tableextractorv1.TableExtractorServiceClient
	transformer    transformerv1.TransformerServiceClient
	publisher      githubpublisherv1.GithubPublisherServiceClient
}

func NewActivities(repo Repository, preprocessClient preprocessv1.PreprocessServiceClient, transformerClient transformerv1.TransformerServiceClient, tableExtractorClient tableextractorv1.TableExtractorServiceClient, publisherClient githubpublisherv1.GithubPublisherServiceClient, storageClient storagev1.StorageServiceClient) *Activities {
	return &Activities{
		repo:           repo,
		storage:        storageClient,
		statuses:       NewStatusApplication(repo),
		preprocess:     preprocessClient,
		tableExtractor: tableExtractorClient,
		transformer:    transformerClient,
		publisher:      publisherClient,
	}
}

func (a *Activities) MarkStoredOriginal(ctx context.Context, documentID, originalETag string) error {
	ctx = a.withCorrelationID(ctx, "")
	doc, err := a.repo.GetDocument(ctx, documentID)
	if err != nil {
		return err
	}
	if doc == nil {
		return fmt.Errorf("document %s not found", documentID)
	}

	status, err := a.statuses.ResolveCurrentStatus(ctx, doc)
	if err != nil {
		return err
	}
	if status != domain.DocumentStatusUploaded {
		return nil
	}
	if doc.OriginalBucket == "" || doc.OriginalObjectKey == "" {
		return fmt.Errorf("document %s has no original object key", documentID)
	}

	version := latestVersion(doc).Version
	if version == 0 {
		version = 1
	}

	_, err = a.statuses.Transition(ctx, doc, domain.DocumentStatusStoredOriginal, "", "", &domain.DocumentVersion{Version: version, OriginalObjectKey: doc.OriginalObjectKey, OriginalETag: originalETag})
	return err
}

func (a *Activities) SubmitPreprocess(ctx context.Context, input preprocessInput) (preprocessResult, error) {
	if a.preprocess == nil {
		return preprocessResult{}, fmt.Errorf("preprocess client is not configured")
	}
	if input.JobID != "" {
		return a.GetPreprocessStatus(ctx, input.JobID, input.CorrelationID)
	}
	if strings.TrimSpace(input.SourceBucket) == "" || strings.TrimSpace(input.SourceObjectKey) == "" {
		return preprocessResult{}, fmt.Errorf("source bucket and key are required")
	}
	if strings.TrimSpace(input.TargetBucket) == "" || strings.TrimSpace(input.TargetObjectKey) == "" {
		return preprocessResult{}, fmt.Errorf("target bucket and key are required")
	}
	ctx = a.withCorrelationID(ctx, input.CorrelationID)
	req := &preprocessv1.SubmitPreprocessRequest{
		SourceBucket:  input.SourceBucket,
		SourceKey:     input.SourceObjectKey,
		SourceEtag:    input.SourceETag,
		TargetBucket:  input.TargetBucket,
		TargetKey:     input.TargetObjectKey,
		DocumentId:    input.DocumentID,
		CorrelationId: correlationIDFromContext(ctx),
	}

	resp, err := a.preprocess.SubmitPreprocess(ctx, req)
	if err != nil {
		return preprocessResult{}, err
	}

	result := preprocessResult{
		JobID:           strings.TrimSpace(resp.GetJobId()),
		Status:          resp.GetStatus(),
		TargetBucket:    input.TargetBucket,
		TargetObjectKey: input.TargetObjectKey,
		ErrorMessage:    resp.GetErrorMessage(),
		CorrelationID:   firstNonEmpty(resp.GetCorrelationId(), correlationIDFromContext(ctx)),
	}
	if result.JobID == "" {
		return result, status.Error(codes.FailedPrecondition, "preprocess job_id is empty")
	}
	return result, nil
}

func (a *Activities) GetPreprocessStatus(ctx context.Context, jobID, correlationID string) (preprocessResult, error) {
	if a.preprocess == nil {
		return preprocessResult{}, fmt.Errorf("preprocess client is not configured")
	}
	if strings.TrimSpace(jobID) == "" {
		return preprocessResult{}, fmt.Errorf("job_id is required")
	}
	ctx = a.withCorrelationID(ctx, correlationID)
	resp, err := a.preprocess.GetPreprocessStatus(ctx, &preprocessv1.GetPreprocessStatusRequest{
		JobId:         jobID,
		CorrelationId: correlationIDFromContext(ctx),
	})
	if err != nil {
		return preprocessResult{}, err
	}

	result := preprocessResult{
		JobID:           strings.TrimSpace(resp.GetJobId()),
		Status:          resp.GetStatus(),
		TargetBucket:    resp.GetTargetBucket(),
		TargetObjectKey: resp.GetTargetKey(),
		ErrorMessage:    resp.GetErrorMessage(),
		ErrorCode:       resp.GetErrorCode(),
		CorrelationID:   firstNonEmpty(resp.GetCorrelationId(), correlationIDFromContext(ctx)),
	}
	if ts := resp.GetUpdatedAt(); ts != nil {
		result.UpdatedAt = ts.AsTime()
	}
	if result.Status == preprocessv1.PreprocessStatus_PREPROCESS_STATUS_UNSPECIFIED {
		return result, status.Error(codes.FailedPrecondition, "preprocess status is empty")
	}
	return result, nil
}

func (a *Activities) ExtractTables(ctx context.Context, input tableExtractionInput) (tableExtractionResult, error) {
	if a.tableExtractor == nil {
		return tableExtractionResult{}, fmt.Errorf("table extractor client is not configured")
	}
	if strings.TrimSpace(input.DocumentID) == "" {
		return tableExtractionResult{}, fmt.Errorf("document_id is required for table extraction")
	}
	doc, err := a.repo.GetDocument(ctx, input.DocumentID)
	if err != nil {
		return tableExtractionResult{}, fmt.Errorf("load document: %w", err)
	}
	if doc == nil {
		return tableExtractionResult{}, fmt.Errorf("document %s not found", input.DocumentID)
	}
	if strings.TrimSpace(input.OriginalBucket) == "" || strings.TrimSpace(input.OriginalObjectKey) == "" {
		input.OriginalBucket = firstNonEmpty(input.OriginalBucket, doc.OriginalBucket)
		input.OriginalObjectKey = firstNonEmpty(input.OriginalObjectKey, doc.OriginalObjectKey)
	}
	if strings.TrimSpace(input.OriginalBucket) == "" || strings.TrimSpace(input.OriginalObjectKey) == "" {
		return tableExtractionResult{}, fmt.Errorf("original bucket and key are required for table extraction")
	}
	if strings.TrimSpace(input.ArtifactsBucket) == "" {
		input.ArtifactsBucket = firstNonEmpty(input.ArtifactsBucket, doc.MarkdownBucket)
	}
	input.MarkdownBucket = firstNonEmpty(input.MarkdownBucket, doc.MarkdownBucket)
	input.MarkdownObjectKey = firstNonEmpty(input.MarkdownObjectKey, resolvedMarkdownKey(doc))
	prefix, err := resolveArtifactsPrefix(input.ArtifactsPrefixTemplate, input.DocumentID, "", input.ArtifactsPrefix, input.MarkdownObjectKey)
	if err != nil {
		return tableExtractionResult{}, fmt.Errorf("resolve artifacts prefix: %w", err)
	}
	input.ArtifactsPrefix = prefix
	if strings.TrimSpace(input.MarkdownBucket) == "" || strings.TrimSpace(input.MarkdownObjectKey) == "" {
		return tableExtractionResult{}, fmt.Errorf("markdown bucket and key are required for table extraction")
	}
	if strings.TrimSpace(input.ArtifactsBucket) == "" {
		return tableExtractionResult{}, fmt.Errorf("artifacts bucket is required for table extraction")
	}

	ctx = a.withCorrelationID(ctx, input.CorrelationID)
	req := &tableextractorv1.ExtractTablesFromPdfRequest{
		DocumentId:        input.DocumentID,
		OriginalBucket:    input.OriginalBucket,
		OriginalObjectKey: input.OriginalObjectKey,
		ArtifactsBucket:   input.ArtifactsBucket,
		ArtifactsPrefix:   input.ArtifactsPrefix,
		MarkdownBucket:    input.MarkdownBucket,
		MarkdownObjectKey: input.MarkdownObjectKey,
		WaitForResult:     false,
		CorrelationId:     correlationIDFromContext(ctx),
	}

	resp, err := a.tableExtractor.ExtractTablesFromPdf(ctx, req)
	if err != nil {
		return tableExtractionResult{}, err
	}

	return mapTableJob(resp.GetJob(), correlationIDFromContext(ctx), input.ArtifactsPrefix, input.ArtifactsBucket), nil
}

func (a *Activities) GetTableExtractionJob(ctx context.Context, queueID, correlationID string) (tableExtractionResult, error) {
	if a.tableExtractor == nil {
		return tableExtractionResult{}, fmt.Errorf("table extractor client is not configured")
	}
	if strings.TrimSpace(queueID) == "" {
		return tableExtractionResult{}, fmt.Errorf("queue_id is required to poll table extractor job")
	}

	ctx = a.withCorrelationID(ctx, correlationID)
	resp, err := a.tableExtractor.GetTableExtractionJob(ctx, &tableextractorv1.GetTableExtractionJobRequest{
		QueueId:       queueID,
		CorrelationId: correlationIDFromContext(ctx),
	})
	if err != nil {
		return tableExtractionResult{}, err
	}

	return mapTableJob(resp.GetJob(), correlationIDFromContext(ctx), "", ""), nil
}

func (a *Activities) ConvertToMarkdown(ctx context.Context, input convertInput) (convertResult, error) {
	ctx = a.withCorrelationID(ctx, "")
	if a.transformer == nil {
		return convertResult{}, fmt.Errorf("transformer client is not configured")
	}
	doc, err := a.repo.GetDocument(ctx, input.DocumentID)
	if err != nil {
		return convertResult{}, err
	}

	filenameExtension := strings.TrimSpace(input.FilenameExtension)
	if filenameExtension == "" {
		filenameExtension = firstNonEmpty(
			fileExtension(input.OriginalObjectKey),
			fileExtension(doc.OriginalObjectKey),
			fileExtension(doc.Name),
		)
	}

	currentStatus, err := a.statuses.ResolveCurrentStatus(ctx, doc)
	if err != nil {
		return convertResult{}, err
	}
	if currentStatus == domain.DocumentStatusConvertedToMarkdown || currentStatus == domain.DocumentStatusPublishedToGitHub {
		latest := latestVersion(doc)
		return convertResult{MarkdownKey: resolvedMarkdownKey(doc), QueueID: latest.MarkdownQueueID, Status: string(currentStatus), Version: latest.Version, Ready: true}, nil
	}

	correlationID := correlationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = uuid.NewString()
		ctx = metadata.AppendToOutgoingContext(ctx, correlationIDHeader, correlationID)
	}
	resp, err := a.transformer.ConvertDocumentToMarkdown(ctx, &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        input.DocumentID,
		OriginalBucket:    input.OriginalBucket,
		OriginalObjectKey: input.OriginalObjectKey,
		MarkdownBucket:    input.MarkdownBucket,
		MarkdownObjectKey: input.MarkdownObjectKey,
		FilenameExtension: filenameExtension,
		WaitForResult:     false,
		CorrelationId:     correlationID,
	})
	if err != nil {
		return convertResult{}, err
	}

	statusVal := strings.TrimSpace(resp.GetStatus())
	if isTransformerFailureStatus(statusVal) {
		reason := fmt.Sprintf("transformer returned status %q", statusVal)
		return convertResult{}, status.Error(codes.FailedPrecondition, reason)
	}

	markdownKey := resp.GetMarkdownObjectKey()
	if markdownKey == "" {
		markdownKey = input.MarkdownObjectKey
	}

	if markdownKey == "" {
		reason := fmt.Sprintf("markdown object key is empty for bucket=%q", input.MarkdownBucket)
		return convertResult{}, status.Error(codes.FailedPrecondition, reason)
	}

	version := input.Version
	if version == 0 {
		version = latestVersion(doc).Version + 1
	}

	ready := strings.EqualFold(statusVal, "done") || strings.EqualFold(statusVal, string(domain.DocumentStatusConvertedToMarkdown))
	meta := domain.TableMetadata{}
	if resp.GetTableMetadata() != nil {
		meta = domain.TableMetadata{
			TablesCount: resp.GetTableMetadata().GetTablesCount(),
			Pages:       resp.GetTableMetadata().GetPages(),
		}
	}
	tableCount := resp.GetTableCount()
	tablePages := resp.GetTablePages()
	if meta.TablesCount > tableCount {
		tableCount = meta.TablesCount
	}
	if len(tablePages) == 0 && len(meta.Pages) > 0 {
		tablePages = append([]int32(nil), meta.Pages...)
	}

	return convertResult{
		MarkdownKey:   markdownKey,
		QueueID:       resp.GetQueueId(),
		Status:        statusVal,
		Version:       version,
		Ready:         ready,
		TableCount:    tableCount,
		TablePages:    tablePages,
		TableMetadata: meta,
	}, nil
}

type mergeTablesInput struct {
	Doc             *domain.Document
	BaseBucket      string
	BaseObjectKey   string
	TablesBucket    string
	TablesObjectKey string
	TargetBucket    string
	TargetObjectKey string
	CorrelationID   string
}

func (a *Activities) MergeTables(ctx context.Context, input mergeTablesInput) (string, error) {
	ctx = a.withCorrelationID(ctx, input.CorrelationID)
	if input.Doc == nil {
		return "", fmt.Errorf("document is required for merge")
	}
	if a.storage == nil {
		return "", fmt.Errorf("storage client is not configured")
	}
	baseBucket := strings.TrimSpace(input.BaseBucket)
	baseKey := strings.TrimSpace(input.BaseObjectKey)
	tablesBucket := strings.TrimSpace(input.TablesBucket)
	tablesKey := strings.TrimSpace(input.TablesObjectKey)
	targetBucket := strings.TrimSpace(input.TargetBucket)
	targetKey := strings.TrimSpace(input.TargetObjectKey)

	if tablesBucket == "" || tablesKey == "" {
		return "", fmt.Errorf("tables location is required for merge")
	}
	if targetBucket == "" {
		targetBucket = input.Doc.MarkdownBucket
	}
	if targetKey == "" {
		targetKey = resolvedMarkdownKey(input.Doc)
	}
	if targetBucket == "" || targetKey == "" {
		return "", fmt.Errorf("target markdown location is required for merge")
	}

	tablesResp, err := a.storage.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: tablesBucket, ObjectKey: tablesKey})
	if err != nil {
		return "", fmt.Errorf("download tables markdown: %w", err)
	}
	baseContent := ""
	if baseBucket != "" && baseKey != "" {
		baseResp, baseErr := a.storage.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: baseBucket, ObjectKey: baseKey})
		if baseErr != nil {
			return "", fmt.Errorf("download base markdown: %w", baseErr)
		}
		baseContent = string(baseResp.GetContent())
	}

	merged, err := mergeMarkdownWithTables(baseContent, string(tablesResp.GetContent()))
	if err != nil {
		return "", err
	}
	uploadResp, err := a.storage.UploadMarkdown(ctx, &storagev1.UploadMarkdownRequest{
		OrgSlug:     input.Doc.Owner,
		ProjectSlug: input.Doc.Repository,
		DocumentId:  input.Doc.ID,
		Content:     []byte(merged),
	})
	if err != nil {
		return "", fmt.Errorf("upload merged markdown: %w", err)
	}

	uploadedKey := strings.TrimSpace(uploadResp.GetObjectKey())
	if uploadedKey == "" {
		uploadedKey = strings.TrimSpace(targetKey)
	}
	return uploadedKey, nil
}

func (a *Activities) MarkConverted(ctx context.Context, doc *domain.Document, markdownKey, sourceObjectKey, queueID string, version int32) error {
	if doc == nil {
		return fmt.Errorf("document is required")
	}
	statusVal, err := a.statuses.ResolveCurrentStatus(ctx, doc)
	if err != nil {
		return err
	}
	if statusVal == domain.DocumentStatusConvertedToMarkdown || statusVal == domain.DocumentStatusPublishedToGitHub {
		return nil
	}

	if markdownKey == "" {
		markdownKey = resolvedMarkdownKey(doc)
	}
	if markdownKey == "" {
		return fmt.Errorf("markdown object key is empty for document %s", doc.ID)
	}

	if version == 0 {
		version = latestVersion(doc).Version + 1
	}

	if sourceObjectKey == "" {
		sourceObjectKey = doc.OriginalObjectKey
	}

	_, err = a.statuses.Transition(ctx, doc, domain.DocumentStatusConvertedToMarkdown, "", "", &domain.DocumentVersion{
		Version:           version,
		OriginalObjectKey: sourceObjectKey,
		MarkdownObjectKey: markdownKey,
		MarkdownQueueID:   queueID,
	})
	return err
}

func (a *Activities) PublishMarkdown(ctx context.Context, input publishInput) (*githubpublisherv1.PublishMarkdownResponse, error) {
	ctx = a.withCorrelationID(ctx, input.CorrelationID)
	if a.publisher == nil {
		return nil, fmt.Errorf("publisher client is not configured")
	}
	doc, err := a.repo.GetDocument(ctx, input.DocumentID)
	if err != nil {
		return nil, err
	}

	statusVal, err := a.statuses.ResolveCurrentStatus(ctx, doc)
	if err != nil {
		return nil, err
	}
	if statusVal == domain.DocumentStatusPublishedToGitHub {
		return nil, nil
	}
	owner := strings.TrimSpace(input.Owner)
	if owner == "" {
		owner = doc.Owner
	}
	repository := strings.TrimSpace(input.Repository)
	if repository == "" {
		repository = doc.Repository
	}
	branch := input.Branch
	if branch == "" {
		branch = doc.Branch
	}
	baseBranch := input.BaseBranch
	if baseBranch == "" {
		baseBranch = doc.BaseBranch
	}
	if baseBranch == "" {
		baseBranch = branch
	}
	path := input.Path
	if path == "" {
		path = doc.Path
	}

	correlationID := input.CorrelationID
	if correlationID == "" {
		correlationID = correlationIDFromContext(ctx)
	}

	markdownBucket := input.MarkdownBucket
	if markdownBucket == "" {
		markdownBucket = doc.MarkdownBucket
	}

	markdownObjectKey := input.MarkdownObjectKey
	if markdownObjectKey == "" {
		markdownObjectKey = resolvedMarkdownKey(doc)
	}

	if markdownBucket == "" || markdownObjectKey == "" {
		reason := fmt.Sprintf("markdown content is empty (bucket=%q, object_key=%q)", markdownBucket, markdownObjectKey)
		return nil, status.Error(codes.FailedPrecondition, reason)
	}

	repoPath := path
	if repoPath == "" {
		repoPath = doc.Path
	}

	req := &githubpublisherv1.PublishMarkdownRequest{
		Owner:          owner,
		Repository:     repository,
		Branch:         branch,
		BaseBranch:     baseBranch,
		Path:           repoPath,
		Bucket:         markdownBucket,
		ObjectKey:      markdownObjectKey,
		CreatePr:       input.CreatePR,
		CorrelationId:  correlationID,
		IdempotencyKey: input.IdempotencyKey,
		CommitMessage:  input.CommitMessage,
		AuthorName:     input.AuthorName,
		AuthorEmail:    input.AuthorEmail,
		PrTitle:        input.PRTitle,
		PrBody:         input.PRBody,
	}

	resp, err := a.publisher.PublishMarkdown(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("publish markdown rpc: %w", err)
	}

	if resp.GetStatus() != githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS {
		reason := mapPublishErrorCode(resp.GetErrorCode())
		if msg := strings.TrimSpace(resp.GetErrorMessage()); msg != "" {
			reason = fmt.Sprintf("%s: %s", reason, msg)
		}
		correlation := correlationID
		if correlation == "" {
			correlation = correlationIDFromContext(ctx)
		}
		log.Printf("publish markdown failed: document_id=%s correlation_id=%s reason=%s status=%v", input.DocumentID, correlation, reason, resp.GetStatus())
		if retryAfter := resp.GetRetryAfter(); retryAfter != nil {
			log.Printf("publish markdown retry hint: document_id=%s correlation_id=%s retry_after=%s", input.DocumentID, correlation, retryAfter.AsDuration())
		}
		retryable := resp.GetStatus() == githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE
		if retryAfter := resp.GetRetryAfter(); retryAfter != nil {
			retryable = true
		}
		if retryable {
			return resp, fmt.Errorf("temporary github failure: %s", resp.GetErrorMessage())
		}
		return resp, fmt.Errorf("permanent github failure: %s", resp.GetErrorMessage())
	}

	var version *domain.DocumentVersion
	if input.Version > 0 {
		version = &domain.DocumentVersion{Version: input.Version, MarkdownObjectKey: markdownObjectKey}
	}

	if _, err := a.statuses.TransitionByID(ctx, input.DocumentID, domain.DocumentStatusPublishedToGitHub, resp.GetCommitSha(), resp.GetCommitSha(), version); err != nil {
		return resp, err
	}

	return resp, nil
}

func preprocessFailureReason(code, message string) string {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)
	var reason string
	if code != "" {
		reason = fmt.Sprintf("preprocess %s", code)
	} else {
		reason = "preprocess failed"
	}
	if message != "" {
		return fmt.Sprintf("%s: %s", reason, message)
	}
	return reason
}

func mapPublishErrorCode(code githubpublisherv1.PublishErrorCode) string {
	switch code {
	case githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT:
		return publishGithubTimeoutReason
	case githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_SERVER_ERROR:
		return publishGithub5xxReason
	case githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_RATE_LIMIT:
		return publishGithubRateLimitReason
	case githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_CONFLICT:
		return publishGithubConflictReason
	case githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_PERMISSION:
		return publishGithubPermanentReason
	case githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NETWORK:
		return publishGithubNetworkReason
	case githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NOT_FOUND:
		return publishGithubPermanentReason
	default:
		return publishGithubUnknownReason
	}
}

func mapTableJob(job *tableextractorv1.TableExtractionJob, correlationID, fallbackPrefix, fallbackBucket string) tableExtractionResult {
	if job == nil {
		return tableExtractionResult{
			ArtifactsPrefix: fallbackPrefix,
			ArtifactsBucket: fallbackBucket,
			CorrelationID:   correlationID,
		}
	}
	artifacts := job.GetArtifacts()
	markdown := artifacts.GetMarkdown()
	manifest := artifacts.GetManifest()
	debugPrefix := artifacts.GetDebugPrefix()

	prefix := firstNonEmpty(debugPrefix.GetObjectKey(), fallbackPrefix)
	bucket := firstNonEmpty(markdown.GetBucket(), manifest.GetBucket(), fallbackBucket)
	markdownKey := markdown.GetObjectKey()
	if markdownKey == "" && prefix != "" {
		markdownKey = tablesMarkdownKey(prefix)
	} else if markdownKey != "" && prefix == "" {
		prefix = path.Dir(markdownKey)
	}

	return tableExtractionResult{
		QueueID:           strings.TrimSpace(job.GetQueueId()),
		Status:            job.GetStatus(),
		StatusReason:      job.GetStatusReason(),
		ArtifactsBucket:   bucket,
		ArtifactsPrefix:   prefix,
		MarkdownBucket:    bucket,
		MarkdownObjectKey: markdownKey,
		CorrelationID:     firstNonEmpty(job.GetCorrelationId(), correlationID),
	}
}

func latestVersion(doc *domain.Document) domain.DocumentVersion {
	var latest domain.DocumentVersion
	for i := range doc.Versions {
		if doc.Versions[i].Version > latest.Version {
			latest = doc.Versions[i]
		}
	}
	return latest
}

func resolvedMarkdownKey(doc *domain.Document) string {
	latest := latestVersion(doc)
	if latest.MarkdownObjectKey != "" {
		return latest.MarkdownObjectKey
	}
	return doc.MarkdownObjectKey
}

func (a *Activities) withCorrelationID(ctx context.Context, explicit string) context.Context {
	id := explicit
	if id == "" {
		id = correlationIDFromContext(ctx)
	}
	if id == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, correlationIDHeader, id)
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

func mergeMarkdownWithTables(baseMarkdown, tablesMarkdown string) (string, error) {
	if strings.TrimSpace(tablesMarkdown) == "" {
		return baseMarkdown, nil
	}
	const (
		tablesStart = "<!-- tables:start -->"
		tablesEnd   = "<!-- tables:end -->"
	)

	trimmedTables := strings.TrimSpace(tablesMarkdown)
	markerStart := regexp.MustCompile(`<!--\s*tables:start(?:\s*#\d+)?\s*-->`)
	markerEnd := regexp.MustCompile(`<!--\s*tables:end(?:\s*#\d+)?\s*-->`)

	isTableLine := func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, "|")
	}

	tableSignature := func(header string) string {
		trimmed := strings.Trim(header, "|")
		if strings.TrimSpace(trimmed) == "" {
			return ""
		}
		parts := strings.Split(trimmed, "|")
		cols := make([]string, 0, len(parts))
		for _, part := range parts {
			cols = append(cols, strings.ToLower(strings.TrimSpace(part)))
		}
		if len(cols) == 0 {
			return ""
		}
		return fmt.Sprintf("%d:%s", len(cols), strings.Join(cols, "|"))
	}

	type tableBlock struct {
		start, end int
		lines      []string
		signature  string
	}

	findTables := func(md string) []tableBlock {
		lines := strings.Split(md, "\n")
		blocks := make([]tableBlock, 0)
		for i := 0; i < len(lines); i++ {
			if !isTableLine(lines[i]) {
				continue
			}
			start := i
			j := i
			for j < len(lines) && isTableLine(lines[j]) {
				j++
			}
			blockLines := lines[start:j]
			signature := tableSignature(blockLines[0])
			if signature == "" {
				i = j - 1
				continue
			}
			blocks = append(blocks, tableBlock{start: start, end: j - 1, lines: blockLines, signature: signature})
			i = j - 1
		}
		return blocks
	}

	columnCount := func(block tableBlock) int {
		header := strings.Trim(block.lines[0], "|")
		if header == "" {
			return 0
		}
		parts := strings.Split(header, "|")
		return len(parts)
	}

	tableSignatures := func(blocks []tableBlock) []string {
		signatures := make([]string, 0, len(blocks))
		for _, block := range blocks {
			signatures = append(signatures, block.signature)
		}
		return signatures
	}

	replaceTablesBySignature := func(md string, newTables []tableBlock) (string, bool) {
		lines := strings.Split(md, "\n")
		baseTables := findTables(md)
		if len(baseTables) == 0 || len(newTables) == 0 {
			return md, false
		}
		used := make(map[int]struct{})
		replaced := false
		for idx := len(baseTables) - 1; idx >= 0; idx-- {
			baseBlock := baseTables[idx]
			match := -1
			for newIdx, tbl := range newTables {
				if tbl.signature == baseBlock.signature {
					if _, ok := used[newIdx]; ok {
						continue
					}
					match = newIdx
					break
				}
			}
			if match == -1 {
				continue
			}
			used[match] = struct{}{}
			replaced = true
			lines = append(append(append([]string{}, lines[:baseBlock.start]...), newTables[match].lines...), lines[baseBlock.end+1:]...)
		}
		return strings.Join(lines, "\n"), replaced
	}

	deduplicateTables := func(md string) string {
		lines := strings.Split(md, "\n")
		tables := findTables(md)
		seen := make(map[string]struct{})
		for i := len(tables) - 1; i >= 0; i-- {
			block := tables[i]
			if _, ok := seen[block.signature]; ok {
				lines = append(lines[:block.start], lines[block.end+1:]...)
				continue
			}
			seen[block.signature] = struct{}{}
		}
		return strings.Join(lines, "\n")
	}

	markerBlocks := func(md string) ([]string, [][2]int) {
		blocks := make([]string, 0)
		ranges := make([][2]int, 0)
		pos := 0
		for {
			startLoc := markerStart.FindStringIndex(md[pos:])
			if startLoc == nil {
				break
			}
			startIdx := pos + startLoc[0]
			startEnd := pos + startLoc[1]
			endLoc := markerEnd.FindStringIndex(md[startEnd:])
			if endLoc == nil {
				break
			}
			endIdx := startEnd + endLoc[1]
			blocks = append(blocks, strings.TrimSpace(md[startIdx:endIdx]))
			ranges = append(ranges, [2]int{startIdx, endIdx})
			pos = endIdx
		}
		return blocks, ranges
	}

	extractContent := func(md string) (string, bool) {
		blocks, ranges := markerBlocks(md)
		log.Printf("mergeMarkdownWithTables: markerBlocks detected=%d ranges=%v", len(blocks), ranges)
		if len(blocks) > 0 {
			return strings.Join(blocks, "\n\n"), true
		}
		start := strings.Index(md, tablesStart)
		end := strings.Index(md, tablesEnd)
		if start != -1 && end != -1 && end >= start {
			content := strings.TrimSpace(md[start+len(tablesStart) : end])
			return content, true
		}
		return strings.TrimSpace(md), false
	}

	tablesContent, hasMarkers := extractContent(trimmedTables)
	log.Printf("mergeMarkdownWithTables: tables markers found=%v", hasMarkers)
	if !hasMarkers {
		if tablesBlocks := findTables(trimmedTables); len(tablesBlocks) > 0 {
			lines := strings.Split(trimmedTables, "\n")
			start := tablesBlocks[0].start
			end := tablesBlocks[len(tablesBlocks)-1].end
			tablesContent = strings.TrimSpace(strings.Join(lines[start:end+1], "\n"))
		} else {
			return "", fmt.Errorf("tables block not found in artifact")
		}
	}

	tablesBlock := tablesContent
	if !hasMarkers {
		tablesBlock = fmt.Sprintf("%s\n%s\n%s", tablesStart, tablesContent, tablesEnd)
	}

	baseBlocks, baseRanges := markerBlocks(baseMarkdown)
	log.Printf("mergeMarkdownWithTables: base markerBlocks detected=%d ranges=%v", len(baseBlocks), baseRanges)
	if hasMarkers && len(baseRanges) > 0 {
		start := baseRanges[0][0]
		end := baseRanges[len(baseRanges)-1][1]
		merged := fmt.Sprintf("%s%s%s", baseMarkdown[:start], tablesContent, baseMarkdown[end:])
		return deduplicateTables(merged), nil
	}

	if start := strings.Index(baseMarkdown, tablesStart); start != -1 {
		if end := strings.Index(baseMarkdown, tablesEnd); end != -1 && end >= start {
			prefix := baseMarkdown[:start+len(tablesStart)]
			suffix := baseMarkdown[end:]
			merged := fmt.Sprintf("%s\n\n%s\n%s", prefix, tablesContent, suffix)
			return deduplicateTables(merged), nil
		}
	}

	newTables := findTables(tablesContent)
	log.Printf("mergeMarkdownWithTables: newTables found=%d signatures=%v", len(newTables), tableSignatures(newTables))
	baseTables := findTables(baseMarkdown)
	log.Printf("mergeMarkdownWithTables: baseTables found=%d signatures=%v", len(baseTables), tableSignatures(baseTables))
	if mergedBySignature, replaced := replaceTablesBySignature(baseMarkdown, newTables); replaced {
		log.Printf("mergeMarkdownWithTables: replaceTablesBySignature applied replaced=%v", replaced)
		return deduplicateTables(mergedBySignature), nil
	}
	log.Printf("mergeMarkdownWithTables: replaceTablesBySignature applied replaced=%v", false)

	if len(baseTables) > 0 && len(newTables) > 0 {
		targetIdx := -1
		newCols := columnCount(newTables[0])
		for idx, tbl := range baseTables {
			if tbl.signature == newTables[0].signature {
				targetIdx = idx
				break
			}
			if targetIdx == -1 && newCols > 0 && columnCount(tbl) == newCols {
				targetIdx = idx
			}
		}
		if targetIdx == -1 {
			targetIdx = 0
		}
		log.Printf("mergeMarkdownWithTables: targetIdx=%d", targetIdx)
		lines := strings.Split(baseMarkdown, "\n")
		start := baseTables[targetIdx].start
		end := baseTables[targetIdx].end
		mergedLines := append(append([]string{}, lines[:start]...), strings.Split(tablesBlock, "\n")...)
		mergedLines = append(mergedLines, lines[end+1:]...)
		return deduplicateTables(strings.Join(mergedLines, "\n")), nil
	}

	if strings.TrimSpace(baseMarkdown) == "" {
		return tablesBlock, nil
	}

	fallbackNote := "<!-- tables:appended (no anchor found) -->"
	log.Printf("mergeMarkdownWithTables: fallback append marker added")
	return deduplicateTables(fmt.Sprintf("%s\n\n%s\n%s\n", strings.TrimSpace(baseMarkdown), fallbackNote, tablesBlock)), nil
}

func isTransformerFailureStatus(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "failed", "failure", "error", "errored", strings.ToLower(string(domain.DocumentStatusFailed)):
		return true
	default:
		return false
	}
}

func isTemporaryGRPCError(err error) bool {
	code := status.Code(err)
	switch code {
	case codes.DeadlineExceeded, codes.Unavailable, codes.Internal, codes.Aborted, codes.ResourceExhausted:
		return true
	default:
		return false
	}
}
