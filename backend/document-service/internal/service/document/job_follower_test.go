package document

import (
	"context"
	"testing"
	"time"

	"backend-document-service/internal/domain"
)

type followerRepo struct {
	jobs          []domain.ProcessingJob
	createdJobs   []domain.ProcessingJob
	updatedStatus []domain.DocumentStatus
	document      *domain.Document
}

func (r *followerRepo) ListFinishedJobs(ctx context.Context, limit int, postpone time.Duration) ([]domain.ProcessingJob, error) {
	return r.jobs, nil
}

func (r *followerRepo) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	if r.document != nil {
		return r.document, nil
	}
	return &domain.Document{
		ID:                id,
		Status:            domain.DocumentStatusStoredOriginal,
		Owner:             "org",
		Repository:        "repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/path.md",
		OriginalBucket:    "orig-bucket",
		OriginalObjectKey: "orig/key",
		MarkdownBucket:    "md-bucket",
		MarkdownObjectKey: "md/key",
		WorkflowID:        "corr-1",
	}, nil
}

func (r *followerRepo) CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	r.createdJobs = append(r.createdJobs, *job)
	return job, nil
}

func (r *followerRepo) UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	r.updatedStatus = append(r.updatedStatus, to)
	return &domain.Document{ID: id, Status: to}, nil
}

func (r *followerRepo) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	return &domain.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func TestJobFollowerSchedulesNextStages(t *testing.T) {
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{TaskID: "job-1", DocumentID: "doc-1", Stage: domain.JobStageStoreOriginal, Status: domain.JobStatusCompleted},
			{TaskID: "job-2", DocumentID: "doc-1", Stage: domain.JobStageConvertToMarkdown, Status: domain.JobStatusCompleted},
		},
	}

	follower := NewJobFollower(repo, 10, time.Minute, PublishDefaults{
		CommitMessage: "commit message",
		PRTitle:       "PR title",
		PRBody:        "PR body",
		AuthorName:    "Author",
		AuthorEmail:   "author@example.com",
	}, domain.NewStageRouter(domain.RouterRules{}))
	if err := follower.reconcile(context.Background()); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	if len(repo.createdJobs) != 2 {
		t.Fatalf("expected 2 scheduled jobs, got %d", len(repo.createdJobs))
	}

	if repo.createdJobs[0].Stage != domain.JobStageConvertToMarkdown {
		t.Fatalf("expected convert stage, got %s", repo.createdJobs[0].Stage)
	}
	convertPayload := domain.ConvertPayloadFromMap(repo.createdJobs[0].Payload)
	if convertPayload.OriginalBucket != "orig-bucket" {
		t.Fatalf("unexpected payload for convert: %+v", repo.createdJobs[0].Payload)
	}

	if repo.createdJobs[1].Stage != domain.JobStagePublishToGitHub {
		t.Fatalf("expected publish stage, got %s", repo.createdJobs[1].Stage)
	}
	publishPayload := domain.PublishPayloadFromMap(repo.createdJobs[1].Payload)
	if publishPayload.Repository != "org/repo" {
		t.Fatalf("unexpected repository in payload: %+v", repo.createdJobs[1].Payload)
	}
	if publishPayload.CommitMessage != "commit message" {
		t.Fatalf("unexpected commit_message in payload: %+v", repo.createdJobs[1].Payload)
	}
	if publishPayload.AuthorEmail != "author@example.com" {
		t.Fatalf("author fields were not propagated: %+v", repo.createdJobs[1].Payload)
	}
}

func TestJobFollowerMarksFailed(t *testing.T) {
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{{
			TaskID:     "job-err",
			DocumentID: "doc-1",
			Stage:      domain.JobStageConvertToMarkdown,
			Status:     domain.JobStatusFailed,
			Payload:    map[string]any{"last_error": "boom"},
		}},
	}

	follower := NewJobFollower(repo, 10, time.Minute, PublishDefaults{}, domain.NewStageRouter(domain.RouterRules{}))
	if err := follower.reconcile(context.Background()); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	if len(repo.updatedStatus) != 1 || repo.updatedStatus[0] != domain.DocumentStatusFailed {
		t.Fatalf("document status was not marked as failed: %+v", repo.updatedStatus)
	}
}

func TestJobFollowerSchedulesPreprocessWhenEnabled(t *testing.T) {
	router := domain.NewStageRouter(domain.RouterRules{PreprocessEnabled: true})
	repo := &followerRepo{
		jobs: []domain.ProcessingJob{
			{TaskID: "job-1", DocumentID: "doc-1", Stage: domain.JobStageStoreOriginal, Status: domain.JobStatusCompleted},
		},
		document: &domain.Document{
			ID:                "doc-1",
			Status:            domain.DocumentStatusStoredOriginal,
			OriginalBucket:    "orig-bucket",
			OriginalObjectKey: "incoming/file.doc",
		},
	}

	follower := NewJobFollower(repo, 5, time.Minute, PublishDefaults{}, router)
	if err := follower.reconcile(context.Background()); err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}

	if len(repo.createdJobs) != 1 {
		t.Fatalf("expected 1 scheduled job, got %d", len(repo.createdJobs))
	}
	if repo.createdJobs[0].Stage != domain.JobStagePreprocessToDocx {
		t.Fatalf("expected preprocess_to_docx stage, got %s", repo.createdJobs[0].Stage)
	}
	payload := domain.PreprocessPayloadFromMap(repo.createdJobs[0].Payload)
	if payload.OriginalObjectKey != "incoming/file.doc" || payload.OriginalBucket != "orig-bucket" {
		t.Fatalf("unexpected preprocess payload: %+v", payload)
	}
}
