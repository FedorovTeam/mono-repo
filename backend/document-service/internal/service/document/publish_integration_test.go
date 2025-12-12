package document

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"backend-document-service/internal/domain"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc"
)

func TestPublishMarkdownWithStoredContentAndGithubResponses(t *testing.T) {
	t.Parallel()

	t.Run("publishes markdown via publisher client", func(t *testing.T) {
		t.Parallel()

		repo := &integrationRepo{}
		publisher := &recordingPublisher{}
		activities := NewActivities(repo, nil, publisher)

		_, err := activities.PublishMarkdown(context.Background(), publishInput{
			DocumentID:        "doc-1",
			MarkdownBucket:    "bucket",
			MarkdownObjectKey: "object-key",
			Path:              "docs/doc-1.md",
			Version:           2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if repo.lastStatus != domain.StatusPublishedToGitHub {
			t.Fatalf("expected status %v, got %v", domain.StatusPublishedToGitHub, repo.lastStatus)
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
		activities := NewActivities(repo, nil, publisher)

		_, err := activities.PublishMarkdown(context.Background(), publishInput{
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

		if repo.lastStatus != domain.StatusFailed {
			t.Fatalf("expected failed status, got %v", repo.lastStatus)
		}
		if repo.lastError == "" || !strings.Contains(repo.lastError, "timeout") {
			t.Fatalf("expected error to contain publisher message, got %q", repo.lastError)
		}
	})
}

type integrationRepo struct {
	lastStatus  domain.Status
	lastVersion *domain.DocumentVersion
	lastError   string
	jobs        map[string]domain.ProcessingJob
}

func (r *integrationRepo) CreateDocument(context.Context, *domain.Document, *domain.DocumentVersion, *domain.DocumentStatusLog) (*domain.Document, error) {
	return nil, nil
}

func (r *integrationRepo) GetDocument(_ context.Context, id string) (*domain.Document, error) {
	return &domain.Document{ID: id, Status: domain.StatusStoredOriginal, MarkdownBucket: "bucket", MarkdownObjectKey: "object-key", Versions: []domain.DocumentVersion{{Version: 1, MarkdownObjectKey: "object-key"}}}, nil
}

func (r *integrationRepo) ListDocuments(context.Context, int, int, *domain.Status) ([]domain.Document, error) {
	return nil, nil
}

func (r *integrationRepo) CountStatusLogsWithReasonPrefix(context.Context, string, domain.Status, string) (int, error) {
	return 0, nil
}

func (r *integrationRepo) LastNonFailedStatus(context.Context, string) (domain.Status, error) {
	return domain.Status(""), nil
}

func (r *integrationRepo) UpdateStatus(ctx context.Context, id string, to domain.Status, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	r.lastStatus = to
	r.lastVersion = version
	r.lastError = errorReason
	return &domain.Document{ID: id, Status: to}, nil
}

func (r *integrationRepo) UpdateWorkflowID(context.Context, string, string) (*domain.Document, error) {
	return nil, nil
}

func (r *integrationRepo) CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	if job.TaskID == "" {
		job.TaskID = uuid.NewString()
	}
	if r.jobs == nil {
		r.jobs = make(map[string]domain.ProcessingJob)
	}
	if job.Status == "" {
		job.Status = domain.JobStatusPending
	}
	r.jobs[job.TaskID] = *job
	return job, nil
}

func (r *integrationRepo) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	job := domain.ProcessingJob{TaskID: taskID, Status: status}
	if nextRunAt != nil {
		job.NextRunAt = *nextRunAt
	}
	if payload != nil {
		job.Payload = payload
	}
	if r.jobs == nil {
		r.jobs = make(map[string]domain.ProcessingJob)
	}
	r.jobs[taskID] = job
	return &job, nil
}

func (r *integrationRepo) ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error) {
	for _, job := range r.jobs {
		if job.DocumentID != documentID {
			continue
		}
		if job.Status == domain.JobStatusPending || job.Status == domain.JobStatusInProgress {
			copy := job
			return &copy, nil
		}
	}
	return nil, nil
}

func (r *integrationRepo) RequeueStaleProcessingJobs(context.Context, time.Time, int, time.Time, string) ([]domain.ProcessingJob, error) {
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
