package document

import (
	"context"
	"testing"
	"time"

	"backend-document-service/internal/model"
)

type followerRepo struct {
	jobs          []model.ProcessingJob
	createdJobs   []model.ProcessingJob
	updatedStatus []model.Status
}

func (r *followerRepo) ListFinishedJobs(ctx context.Context, limit int, postpone time.Duration) ([]model.ProcessingJob, error) {
	return r.jobs, nil
}

func (r *followerRepo) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	return &model.Document{
		ID:                id,
		Status:            model.StatusStoredOriginal,
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

func (r *followerRepo) CreateProcessingJob(ctx context.Context, job *model.ProcessingJob, event *model.JobEvent) (*model.ProcessingJob, error) {
	r.createdJobs = append(r.createdJobs, *job)
	return job, nil
}

func (r *followerRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	r.updatedStatus = append(r.updatedStatus, to)
	return &model.Document{ID: id, Status: to}, nil
}

func (r *followerRepo) UpdateProcessingJob(ctx context.Context, taskID string, status model.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *model.JobEvent) (*model.ProcessingJob, error) {
	return &model.ProcessingJob{TaskID: taskID, Status: status, Payload: payload}, nil
}

func TestJobFollowerSchedulesNextStages(t *testing.T) {
	repo := &followerRepo{
		jobs: []model.ProcessingJob{
			{TaskID: "job-1", DocumentID: "doc-1", Stage: model.JobStageStoreOriginal, Status: model.JobStatusCompleted},
			{TaskID: "job-2", DocumentID: "doc-1", Stage: model.JobStageConvertToMarkdown, Status: model.JobStatusCompleted},
		},
	}

	follower := NewJobFollower(repo, 10, time.Minute)
	follower.reconcile(context.Background())

	if len(repo.createdJobs) != 2 {
		t.Fatalf("expected 2 scheduled jobs, got %d", len(repo.createdJobs))
	}

	if repo.createdJobs[0].Stage != model.JobStageConvertToMarkdown {
		t.Fatalf("expected convert stage, got %s", repo.createdJobs[0].Stage)
	}
	if repo.createdJobs[0].Payload["original_bucket"] != "orig-bucket" {
		t.Fatalf("unexpected payload for convert: %+v", repo.createdJobs[0].Payload)
	}

	if repo.createdJobs[1].Stage != model.JobStagePublishToGitHub {
		t.Fatalf("expected publish stage, got %s", repo.createdJobs[1].Stage)
	}
	if repo.createdJobs[1].Payload["repository"] != "org/repo" {
		t.Fatalf("unexpected repository in payload: %+v", repo.createdJobs[1].Payload)
	}
}

func TestJobFollowerMarksFailed(t *testing.T) {
	repo := &followerRepo{
		jobs: []model.ProcessingJob{{
			TaskID:     "job-err",
			DocumentID: "doc-1",
			Stage:      model.JobStageConvertToMarkdown,
			Status:     model.JobStatusFailed,
			Payload:    map[string]any{"last_error": "boom"},
		}},
	}

	follower := NewJobFollower(repo, 10, time.Minute)
	follower.reconcile(context.Background())

	if len(repo.updatedStatus) != 1 || repo.updatedStatus[0] != model.StatusFailed {
		t.Fatalf("document status was not marked as failed: %+v", repo.updatedStatus)
	}
}
