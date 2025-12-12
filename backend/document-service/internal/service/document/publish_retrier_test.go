package document

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"

	"google.golang.org/grpc"
)

func TestMapPublishErrorCode(t *testing.T) {
	cases := []struct {
		name string
		code githubpublisherv1.PublishErrorCode
		want string
	}{
		{name: "timeout", code: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT, want: publishGithubTimeoutReason},
		{name: "server", code: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_SERVER_ERROR, want: publishGithub5xxReason},
		{name: "rate limit", code: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_RATE_LIMIT, want: publishGithubRateLimitReason},
		{name: "conflict", code: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_CONFLICT, want: publishGithubConflictReason},
		{name: "network", code: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_NETWORK, want: publishGithubNetworkReason},
		{name: "unknown", code: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_UNKNOWN, want: publishGithubUnknownReason},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapPublishErrorCode(tc.code); got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

func TestPublishRetrierRetriesAndPublishes(t *testing.T) {
	doc := &model.Document{
		ID:                "doc-1",
		Name:              "doc.md",
		Owner:             "owner",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "doc.md",
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "doc.md",
		Status:            model.StatusFailed,
		ErrorReason:       publishGithub5xxReason + ": timeout",
		UpdatedAt:         time.Now().Add(-time.Hour),
		Versions: []model.DocumentVersion{{
			Version:           1,
			MarkdownObjectKey: "doc.md",
		}},
	}
	repo := newRetryRepo(doc)
	repo.logs[doc.ID] = []model.DocumentStatusLog{{ToStatus: model.StatusConvertedToMarkdown}}

	publisher := &retryPublisher{}
	activities := NewActivities(repo, &stubStorageClient{}, nil, publisher, "owner", "repo", "main", "main", RetryPolicy{InitialBackoff: time.Second, MaxBackoff: time.Minute})
	retrier := NewPublishRetrier(repo, activities, 10, 3, time.Second, time.Minute)
	retrier.now = func() time.Time { return time.Now().Add(time.Minute) }
	retrier.logf = func(string, ...interface{}) {}

	retrier.reconcile(context.Background())

	updated, _ := repo.GetDocument(context.Background(), doc.ID)
	if updated.Status != model.StatusPublishedToGitHub {
		t.Fatalf("expected published status, got %v", updated.Status)
	}
	if publisher.calls == 0 {
		t.Fatal("publish was not retried")
	}
}

func TestPublishRetrierStopsAfterMaxAttempts(t *testing.T) {
	doc := &model.Document{
		ID:                "doc-2",
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "doc.md",
		Status:            model.StatusFailed,
		ErrorReason:       publishGithub5xxReason,
		UpdatedAt:         time.Now().Add(-time.Hour),
	}
	repo := newRetryRepo(doc)
	repo.logs[doc.ID] = []model.DocumentStatusLog{{ToStatus: model.StatusFailed, Reason: publishGithub5xxReason}, {ToStatus: model.StatusFailed, Reason: publishGithub5xxReason}, {ToStatus: model.StatusFailed, Reason: publishGithub5xxReason}}

	retrier := NewPublishRetrier(repo, nil, 10, 3, time.Second, time.Minute)
	retrier.logf = func(string, ...interface{}) {}

	retrier.reconcile(context.Background())

	updated, _ := repo.GetDocument(context.Background(), doc.ID)
	if updated.ErrorReason == publishGithub5xxReason {
		t.Fatalf("expected permanent reason, got %s", updated.ErrorReason)
	}
	if updated.ErrorReason == "" || !strings.HasPrefix(updated.ErrorReason, publishGithubPermanentReason) {
		t.Fatalf("unexpected error reason: %s", updated.ErrorReason)
	}
}

type retryRepo struct {
	docs      map[string]*model.Document
	logs      map[string][]model.DocumentStatusLog
	nonFailed map[string]model.Status
	now       func() time.Time
	jobs      map[string]model.ProcessingJob
}

func newRetryRepo(doc *model.Document) *retryRepo {
	return &retryRepo{
		docs:      map[string]*model.Document{doc.ID: doc},
		logs:      make(map[string][]model.DocumentStatusLog),
		nonFailed: make(map[string]model.Status),
		now:       time.Now,
	}
}

func (r *retryRepo) CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error) {
	r.docs[doc.ID] = doc
	return doc, nil
}

func (r *retryRepo) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	d, ok := r.docs[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	copyDoc := *d
	return &copyDoc, nil
}

func (r *retryRepo) ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error) {
	var filtered []model.Document
	for _, doc := range r.docs {
		if status != nil && doc.Status != *status {
			continue
		}
		filtered = append(filtered, *doc)
	}

	if offset >= len(filtered) {
		return nil, nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], nil
}

func (r *retryRepo) CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status model.Status, reasonPrefix string) (int, error) {
	var count int
	for _, log := range r.logs[id] {
		if log.ToStatus == status && strings.HasPrefix(log.Reason, reasonPrefix) {
			count++
		}
	}
	return count, nil
}

func (r *retryRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	doc, ok := r.docs[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	prevStatus := doc.Status
	if version != nil && version.MarkdownObjectKey != "" {
		doc.MarkdownObjectKey = version.MarkdownObjectKey
	}
	if errorReason != "" {
		doc.ErrorReason = errorReason
	}
	doc.Status = to
	doc.UpdatedAt = r.now()
	r.logs[id] = append(r.logs[id], model.DocumentStatusLog{FromStatus: prevStatus, ToStatus: to, Reason: reason, CreatedAt: doc.UpdatedAt})
	return doc, nil
}

func (r *retryRepo) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	doc, ok := r.docs[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	doc.WorkflowID = workflowID
	return doc, nil
}

func (r *retryRepo) CreateProcessingJob(ctx context.Context, job *model.ProcessingJob, event *model.JobEvent) (*model.ProcessingJob, error) {
	if job.TaskID == "" {
		job.TaskID = uuid.NewString()
	}
	if r.jobs == nil {
		r.jobs = make(map[string]model.ProcessingJob)
	}
	if job.Status == "" {
		job.Status = model.JobStatusPending
	}
	r.jobs[job.TaskID] = *job
	return job, nil
}

func (r *retryRepo) UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error) {
	job := model.ProcessingJob{TaskID: taskID, Status: status}
	if payload != nil {
		job.Payload = payload
	}
	if nextRunAt != nil {
		job.NextRunAt = *nextRunAt
	}
	if r.jobs == nil {
		r.jobs = make(map[string]model.ProcessingJob)
	}
	r.jobs[taskID] = job
	return &job, nil
}

func (r *retryRepo) ActiveJob(ctx context.Context, documentID string) (*model.ProcessingJob, error) {
	for _, job := range r.jobs {
		if job.DocumentID != documentID {
			continue
		}
		if job.Status == model.JobStatusPending || job.Status == model.JobStatusInProgress {
			copy := job
			return &copy, nil
		}
	}
	return nil, nil
}

func (r *retryRepo) LastNonFailedStatus(ctx context.Context, id string) (model.Status, error) {
	logs := r.logs[id]
	for i := len(logs) - 1; i >= 0; i-- {
		if logs[i].ToStatus != model.StatusFailed {
			return logs[i].ToStatus, nil
		}
	}
	if status, ok := r.nonFailed[id]; ok {
		return status, nil
	}
	return "", sql.ErrNoRows
}

type retryPublisher struct {
	calls int
	fail  error
}

func (r *retryPublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	r.calls++
	if r.fail != nil {
		return nil, r.fail
	}
	return &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "sha"}, nil
}
