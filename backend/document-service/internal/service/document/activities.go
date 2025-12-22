package document

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"backend-document-service/internal/domain"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	preprocessv1 "backend-document-service/proto/backend/preprocess/v1"
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
	MarkdownKey string
	QueueID     string
	Status      string
	Version     int32
	Ready       bool
}

type Activities struct {
	repo        Repository
	statuses    *StatusApplication
	preprocess  preprocessv1.PreprocessServiceClient
	transformer transformerv1.TransformerServiceClient
	publisher   githubpublisherv1.GithubPublisherServiceClient
}

func NewActivities(repo Repository, preprocessClient preprocessv1.PreprocessServiceClient, transformerClient transformerv1.TransformerServiceClient, publisherClient githubpublisherv1.GithubPublisherServiceClient) *Activities {
	return &Activities{
		repo:        repo,
		statuses:    NewStatusApplication(repo),
		preprocess:  preprocessClient,
		transformer: transformerClient,
		publisher:   publisherClient,
	}
}

func (a *Activities) MarkStoredOriginal(ctx context.Context, documentID string) error {
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

	_, err = a.statuses.Transition(ctx, doc, domain.DocumentStatusStoredOriginal, "", "", &domain.DocumentVersion{Version: version, OriginalObjectKey: doc.OriginalObjectKey})
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

	return convertResult{MarkdownKey: markdownKey, QueueID: resp.GetQueueId(), Status: statusVal, Version: version, Ready: ready}, nil
}

func (a *Activities) MarkConverted(ctx context.Context, doc *domain.Document, markdownKey, queueID string, version int32) error {
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

	_, err = a.statuses.Transition(ctx, doc, domain.DocumentStatusConvertedToMarkdown, "", "", &domain.DocumentVersion{
		Version:           version,
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
