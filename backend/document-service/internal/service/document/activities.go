package document

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"
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

	markdownAwaitTimeout          = 15 * time.Minute
	markdownAwaitInitialBackoff   = 2 * time.Second
	markdownAwaitMaxBackoff       = 20 * time.Second
	conversionNotificationTimeout = "conversion notification timeout"

	publishObjectCheckInitialBackoff = 500 * time.Millisecond
	publishObjectCheckMaxBackoff     = 5 * time.Second
	publishObjectCheckAttempts       = 3
)

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
	FilenameExtension string
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

type awaitMarkdownInput struct {
	DocumentID        string
	MarkdownBucket    string
	MarkdownObjectKey string
	CorrelationID     string
	QueueID           string
	TransformerStatus string
}

type convertResult struct {
	MarkdownKey string
	QueueID     string
	Status      string
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
}

func NewActivities(repo Repository, storageClient storagev1.StorageServiceClient, transformerClient transformerv1.TransformerServiceClient, publisherClient githubpublisherv1.GithubPublisherServiceClient, repoOwner, repoName, branch, baseBranch string) *Activities {
	return &Activities{
		repo:        repo,
		storage:     storageClient,
		transformer: transformerClient,
		publisher:   publisherClient,
		repoOwner:   repoOwner,
		repoName:    repoName,
		branch:      branch,
		baseBranch:  baseBranch,
	}
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

	status, err := a.progressStatus(ctx, doc)
	if err != nil {
		return documentSnapshot{}, err
	}
	if doc.OriginalBucket == "" || doc.OriginalObjectKey == "" {
		return documentSnapshot{}, fmt.Errorf("document %s has no original object key", documentID)
	}

	version := latestVersion(doc).Version
	if status == model.StatusUploaded {
		if _, err := a.storage.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: doc.OriginalBucket, ObjectKey: doc.OriginalObjectKey}); err != nil {
			_ = a.fail(ctx, documentID, fmt.Sprintf("wait original: %v", err))
			return documentSnapshot{}, err
		}

		if version == 0 {
			version = 1
		}

		if _, err := a.repo.UpdateStatus(ctx, documentID, model.StatusStoredOriginal, "", "", &model.DocumentVersion{Version: version, OriginalObjectKey: doc.OriginalObjectKey}); err != nil {
			return documentSnapshot{}, err
		}
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
	doc, err := a.repo.GetDocument(ctx, input.DocumentID)
	if err != nil {
		return convertResult{}, err
	}

	status, err := a.progressStatus(ctx, doc)
	if err != nil {
		return convertResult{}, err
	}
	if status == model.StatusConvertedToMarkdown || status == model.StatusPublishedToGitHub {
		latest := latestVersion(doc)
		return convertResult{MarkdownKey: resolvedMarkdownKey(doc), QueueID: latest.MarkdownQueueID, Status: string(status)}, nil
	}
	extension := input.FilenameExtension
	if extension == "" {
		extension = strings.TrimPrefix(strings.ToLower(filepath.Ext(input.OriginalObjectKey)), ".")
	}

	resp, err := a.transformer.ConvertDocumentToMarkdown(ctx, &transformerv1.ConvertDocumentToMarkdownRequest{
		DocumentId:        input.DocumentID,
		OriginalBucket:    input.OriginalBucket,
		OriginalObjectKey: input.OriginalObjectKey,
		MarkdownBucket:    input.MarkdownBucket,
		MarkdownObjectKey: input.MarkdownObjectKey,
		FilenameExtension: extension,
	})
	if err != nil {
		_ = a.fail(ctx, input.DocumentID, fmt.Sprintf("convert: %v", err))
		return convertResult{}, err
	}

	markdownKey := resp.GetMarkdownObjectKey()
	if markdownKey == "" {
		markdownKey = input.MarkdownObjectKey
	}

	if markdownKey == "" {
		reason := fmt.Sprintf("markdown object key is empty for bucket=%q", input.MarkdownBucket)
		_ = a.fail(ctx, input.DocumentID, reason)
		return convertResult{}, status.Error(codes.FailedPrecondition, reason)
	}

	version := latestVersion(doc).Version + 1
	if _, err := a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusConvertedToMarkdown, "", "", &model.DocumentVersion{
		Version:           version,
		MarkdownObjectKey: markdownKey,
		MarkdownQueueID:   resp.GetQueueId(),
	}); err != nil {
		return convertResult{}, err
	}
	return convertResult{MarkdownKey: markdownKey, QueueID: resp.GetQueueId(), Status: resp.GetStatus()}, nil
}

func (a *Activities) AwaitMarkdown(ctx context.Context, input awaitMarkdownInput) error {
	ctx = a.withCorrelationID(ctx, input.CorrelationID)
	if a.storage == nil {
		return fmt.Errorf("storage client is not configured")
	}
	if input.MarkdownBucket == "" || input.MarkdownObjectKey == "" {
		return fmt.Errorf("markdown bucket/object key is required")
	}

	deadline := time.Now().Add(markdownAwaitTimeout)
	backoff := markdownAwaitInitialBackoff

	for {
		if _, err := a.storage.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: input.MarkdownBucket, ObjectKey: input.MarkdownObjectKey}); err == nil {
			log.Printf("markdown object detected: document_id=%s correlation_id=%s queue_id=%s", input.DocumentID, correlationIDFromContext(ctx), input.QueueID)
			return nil
		}

		if time.Now().After(deadline) {
			_ = a.fail(ctx, input.DocumentID, conversionNotificationTimeout)
			return errors.New(conversionNotificationTimeout)
		}

		log.Printf("markdown object not ready, retrying: document_id=%s correlation_id=%s backoff=%s", input.DocumentID, correlationIDFromContext(ctx), backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff = backoff * 2
		if backoff > markdownAwaitMaxBackoff {
			backoff = markdownAwaitMaxBackoff
		}
	}
}

func (a *Activities) PublishMarkdown(ctx context.Context, input publishInput) error {
	ctx = a.withCorrelationID(ctx, input.CorrelationID)
	if a.publisher == nil {
		return fmt.Errorf("publisher client is not configured")
	}
	doc, err := a.repo.GetDocument(ctx, input.DocumentID)
	if err != nil {
		return err
	}

	statusVal, err := a.progressStatus(ctx, doc)
	if err != nil {
		return err
	}
	if statusVal == model.StatusPublishedToGitHub {
		return nil
	}
	owner := strings.TrimSpace(input.Owner)
	if owner == "" {
		owner = doc.Owner
		if owner == "" {
			owner = a.repoOwner
		}
	}
	repository := strings.TrimSpace(input.Repository)
	if repository == "" {
		repository = doc.Repository
		if repository == "" {
			repository = a.repoName
		}
	}
	branch := input.Branch
	if branch == "" {
		branch = doc.Branch
		if branch == "" {
			branch = a.branch
		}
	}
	baseBranch := input.BaseBranch
	if baseBranch == "" {
		baseBranch = doc.BaseBranch
		if baseBranch == "" {
			baseBranch = a.baseBranch
		}
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

	commitMessage := input.DocumentName
	if commitMessage == "" {
		commitMessage = path
	}

	if owner == "" || repository == "" {
		reason := "owner and repository are required"
		_ = a.fail(ctx, input.DocumentID, reason)
		return fmt.Errorf("%s", reason)
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
		_ = a.fail(ctx, input.DocumentID, reason)
		return status.Error(codes.FailedPrecondition, reason)
	}

	if a.storage == nil {
		return fmt.Errorf("storage client is not configured")
	}

	backoff := publishObjectCheckInitialBackoff
	for attempt := 1; attempt <= publishObjectCheckAttempts; attempt++ {
		if _, err := a.storage.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: markdownBucket, ObjectKey: markdownObjectKey}); err == nil {
			break
		} else {
			log.Printf("markdown object not available: document_id=%s bucket=%s object_key=%s correlation_id=%s attempt=%d error=%v", input.DocumentID, markdownBucket, markdownObjectKey, correlationIDFromContext(ctx), attempt, err)

			if attempt == publishObjectCheckAttempts {
				reason := fmt.Sprintf("markdown object %s/%s unavailable (correlation_id=%s): %v", markdownBucket, markdownObjectKey, correlationIDFromContext(ctx), err)
				_ = a.fail(ctx, input.DocumentID, reason)
				return fmt.Errorf("%s", reason)
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}

			backoff *= 2
			if backoff > publishObjectCheckMaxBackoff {
				backoff = publishObjectCheckMaxBackoff
			}
		}
	}

	repoPath := path
	if repoPath == "" {
		repoPath = doc.Path
	}

	req := &githubpublisherv1.PublishMarkdownRequest{
		DocumentId:        input.DocumentID,
		RepositoryOwner:   owner,
		RepositoryName:    repository,
		Branch:            branch,
		BaseBranch:        baseBranch,
		Path:              repoPath,
		CommitMessage:     commitMessage,
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownObjectKey,
		CreatePr:          input.CreatePR,
	}

	resp, err := a.publisher.PublishMarkdown(ctx, req)
	if err != nil {
		correlation := correlationID
		if correlation == "" {
			correlation = correlationIDFromContext(ctx)
		}
		info := classifyPublishError(err)
		log.Printf("publish markdown failed: document_id=%s correlation_id=%s reason=%s retryable=%v error=%v", input.DocumentID, correlation, info.reason, info.retryable, err)
		if _, updErr := a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusFailed, info.reason, info.reason, nil); updErr != nil {
			log.Printf("update status after publish error: %v", updErr)
		}
		if info.retryable {
			return err
		}
		return fmt.Errorf("publish markdown: %w", err)
	}

	if _, err := a.repo.UpdateStatus(ctx, input.DocumentID, model.StatusPublishedToGitHub, resp.GetCommitSha(), resp.GetCommitSha(), nil); err != nil {
		return err
	}

	return nil
}

type publishErrorInfo struct {
	reason    string
	retryable bool
}

func classifyPublishError(err error) publishErrorInfo {
	info := publishErrorInfo{reason: publishGithubUnknownReason, retryable: false}
	if err == nil {
		return info
	}

	st, ok := status.FromError(err)
	if !ok {
		return info
	}

	code := st.Code()
	lowerMsg := strings.ToLower(st.Message())

	if code == codes.DeadlineExceeded { //nolint:errorlint
		info.reason = publishGithubTimeoutReason
		info.retryable = true
		return info
	}

	if code == codes.ResourceExhausted { //nolint:errorlint
		info.reason = publishGithubRateLimitReason
		info.retryable = true
		return info
	}

	if code == codes.AlreadyExists { //nolint:errorlint
		info.reason = publishGithubConflictReason
		return info
	}

	statusCode := parseHTTPStatusCode(lowerMsg)
	if statusCode >= 500 {
		info.reason = publishGithub5xxReason
		info.retryable = true
		return info
	}

	if strings.Contains(lowerMsg, "refusing to allow an oauth app") {
		info.reason = publishGithubPermanentReason
		return info
	}

	if strings.Contains(lowerMsg, "rate limit") {
		info.reason = publishGithubRateLimitReason
		info.retryable = true
		return info
	}

	if strings.Contains(lowerMsg, "conflict") || strings.Contains(lowerMsg, "merge") || strings.Contains(lowerMsg, "conflicting files") {
		info.reason = publishGithubConflictReason
		return info
	}

	if strings.Contains(lowerMsg, "timeout") || strings.Contains(lowerMsg, "deadline") {
		info.reason = publishGithubTimeoutReason
		info.retryable = true
		return info
	}

	if strings.Contains(lowerMsg, "not found") {
		info.reason = publishGithubPermanentReason
		return info
	}

	if strings.Contains(lowerMsg, "connection reset") || strings.Contains(lowerMsg, "connection refused") || strings.Contains(lowerMsg, "network is unreachable") {
		info.reason = publishGithubNetworkReason
		info.retryable = true
		return info
	}

	return info
}

func parseHTTPStatusCode(msg string) int {
	matches := httpStatusPattern.FindStringSubmatch(msg)
	if len(matches) < 2 {
		return 0
	}

	code, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return code
}

func (a *Activities) fail(ctx context.Context, documentID, reason string) error {
	_, err := a.repo.UpdateStatus(ctx, documentID, model.StatusFailed, reason, reason, nil)
	return err
}

func (a *Activities) progressStatus(ctx context.Context, doc *model.Document) (model.Status, error) {
	if doc.Status != model.StatusFailed {
		return doc.Status, nil
	}

	statusVal, err := a.repo.LastNonFailedStatus(ctx, doc.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.StatusUploaded, nil
		}
		return "", err
	}

	if statusVal == "" {
		return model.StatusUploaded, nil
	}

	return statusVal, nil
}

func latestVersion(doc *model.Document) model.DocumentVersion {
	var latest model.DocumentVersion
	for i := range doc.Versions {
		if doc.Versions[i].Version > latest.Version {
			latest = doc.Versions[i]
		}
	}
	return latest
}

func resolvedMarkdownKey(doc *model.Document) string {
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

var httpStatusPattern = regexp.MustCompile(`http status code: (\d{3})`)
