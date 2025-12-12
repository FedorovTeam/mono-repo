package document

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc"
)

func TestPublishMarkdownWithStoredContentAndGithubResponses(t *testing.T) {
	t.Parallel()

	t.Run("publishes markdown via publisher client", func(t *testing.T) {
		t.Parallel()

		repo := &integrationRepo{}
		publisher := &recordingPublisher{}
		activities := NewActivities(repo, nil, publisher, "owner", "repo", "main", "main", RetryPolicy{InitialBackoff: time.Second, MaxBackoff: time.Minute})

		err := activities.PublishMarkdown(context.Background(), publishInput{
			DocumentID:        "doc-1",
			MarkdownBucket:    "bucket",
			MarkdownObjectKey: "object-key",
			Path:              "docs/doc-1.md",
			Version:           2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if repo.lastStatus != model.StatusPublishedToGitHub {
			t.Fatalf("expected status %v, got %v", model.StatusPublishedToGitHub, repo.lastStatus)
		}
		if repo.lastVersion == nil || repo.lastVersion.Version != 2 {
			t.Fatalf("unexpected version saved: %+v", repo.lastVersion)
		}

		if !publisher.called {
			t.Fatal("publisher was not called")
		}
		if publisher.lastReq == nil {
			t.Fatal("publisher request was not recorded")
		}
	})

	t.Run("marks document failed when publisher returns failure", func(t *testing.T) {
		t.Parallel()

		repo := &integrationRepo{}
		publisher := &recordingPublisher{response: &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_TEMPORARY_FAILURE, ErrorCode: githubpublisherv1.PublishErrorCode_PUBLISH_ERROR_CODE_TIMEOUT, ErrorMessage: "timeout"}}
		activities := NewActivities(repo, nil, publisher, "owner", "repo", "main", "main", RetryPolicy{InitialBackoff: time.Second, MaxBackoff: time.Minute})

		err := activities.PublishMarkdown(context.Background(), publishInput{
			DocumentID:        "doc-missing",
			MarkdownBucket:    "bucket",
			MarkdownObjectKey: "missing.md",
			Path:              "docs/missing.md",
			Version:           3,
			CorrelationID:     "corr-1",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if repo.lastStatus != model.StatusFailed {
			t.Fatalf("expected failed status, got %v", repo.lastStatus)
		}
		if repo.lastError == "" || !strings.Contains(repo.lastError, "timeout") {
			t.Fatalf("expected error to contain publisher message, got %q", repo.lastError)
		}
	})
}

type integrationRepo struct {
	lastStatus  model.Status
	lastVersion *model.DocumentVersion
	lastError   string
	jobs        map[string]model.ProcessingJob
}

func (r *integrationRepo) CreateDocument(context.Context, *model.Document, *model.DocumentVersion, *model.DocumentStatusLog) (*model.Document, error) {
	return nil, nil
}

func (r *integrationRepo) GetDocument(_ context.Context, id string) (*model.Document, error) {
	return &model.Document{ID: id, Status: model.StatusStoredOriginal, MarkdownBucket: "bucket", MarkdownObjectKey: "object-key", Versions: []model.DocumentVersion{{Version: 1, MarkdownObjectKey: "object-key"}}}, nil
}

func (r *integrationRepo) ListDocuments(context.Context, int, int, *model.Status) ([]model.Document, error) {
	return nil, nil
}

func (r *integrationRepo) CountStatusLogsWithReasonPrefix(context.Context, string, model.Status, string) (int, error) {
	return 0, nil
}

func (r *integrationRepo) LastNonFailedStatus(context.Context, string) (model.Status, error) {
	return model.Status(""), nil
}

func (r *integrationRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	r.lastStatus = to
	r.lastVersion = version
	r.lastError = errorReason
	return &model.Document{ID: id, Status: to}, nil
}

func (r *integrationRepo) UpdateWorkflowID(context.Context, string, string) (*model.Document, error) {
	return nil, nil
}

func (r *integrationRepo) CreateProcessingJob(ctx context.Context, job *model.ProcessingJob, event *model.JobEvent) (*model.ProcessingJob, error) {
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

func (r *integrationRepo) UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error) {
	job := model.ProcessingJob{TaskID: taskID, Status: status}
	if nextRunAt != nil {
		job.NextRunAt = *nextRunAt
	}
	if payload != nil {
		job.Payload = payload
	}
	if r.jobs == nil {
		r.jobs = make(map[string]model.ProcessingJob)
	}
	r.jobs[taskID] = job
	return &job, nil
}

func (r *integrationRepo) ActiveJob(ctx context.Context, documentID string) (*model.ProcessingJob, error) {
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

type recordingPublisher struct {
	called   bool
	lastReq  *githubpublisherv1.PublishMarkdownRequest
	err      error
	response *githubpublisherv1.PublishMarkdownResponse
}

func (p *recordingPublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	p.called = true
	p.lastReq = in
	if p.err != nil {
		return nil, p.err
	}
	if p.response != nil {
		return p.response, nil
	}
	return &githubpublisherv1.PublishMarkdownResponse{Status: githubpublisherv1.PublishResultStatus_PUBLISH_RESULT_STATUS_SUCCESS, CommitSha: "integration-sha"}, nil
}
