package sqlrepo_test

import (
	"context"
	"database/sql"
	"path"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/mono-repo/table-extractor-service/db/migrations"
	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/repository"
	"github.com/mono-repo/table-extractor-service/internal/repository/sqlrepo"
	"github.com/mono-repo/table-extractor-service/internal/storage/database"
)

func TestUpsertOrGetActiveIdempotent(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	job := newJob(t, "doc-1")

	created, existed, err := repo.UpsertOrGetActive(ctx, job, 10)
	require.NoError(t, err)
	require.False(t, existed)
	require.Equal(t, job.DocumentID, created.DocumentID)
	require.Equal(t, domain.StatusPending, created.Status)
	require.Equal(t, job.Artifacts.RawJSON.ObjectKey, created.Artifacts.RawJSON.ObjectKey)
	require.Equal(t, 0, created.Attempts)

	secondJob := job
	secondJob.ID = uuid.New()
	secondJob.ArtifactsPrefix = "other/prefix"
	secondJob.Artifacts = domain.BuildArtifacts(secondJob.ArtifactsBucket, secondJob.ArtifactsPrefix)

	reused, existed, err := repo.UpsertOrGetActive(ctx, secondJob, 10)
	require.NoError(t, err)
	require.True(t, existed)
	require.Equal(t, created.ID, reused.ID)
	require.Equal(t, created.Artifacts.RawJSON.ObjectKey, reused.Artifacts.RawJSON.ObjectKey)
}

func TestUpsertOrGetActiveQueueLimit(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	_, existed, err := repo.UpsertOrGetActive(ctx, newJob(t, "doc-1"), 1)
	require.NoError(t, err)
	require.False(t, existed)

	_, _, err = repo.UpsertOrGetActive(ctx, newJob(t, "doc-2"), 1)
	require.ErrorIs(t, err, repository.ErrQueueLimitReached)
}

func TestAcquirePendingBatch(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	job1 := newJob(t, "doc-1")
	job1.NextRunAt = nil
	job2 := newJob(t, "doc-2")
	job2.NextRunAt = ptrTime(now.Add(-time.Minute))
	job3 := newJob(t, "doc-3")
	job3.NextRunAt = ptrTime(now.Add(time.Hour))

	for _, job := range []domain.TableExtractionJob{job1, job2, job3} {
		_, _, err := repo.UpsertOrGetActive(ctx, job, 10)
		require.NoError(t, err)
	}

	acquired, err := repo.AcquirePendingBatch(ctx, 2, now)
	require.NoError(t, err)
	require.Len(t, acquired, 2)
	require.Equal(t, domain.StatusPending, acquired[0].Status)
	require.Equal(t, domain.StatusPending, acquired[1].Status)
	require.Equal(t, 0, acquired[0].Attempts)
	require.Equal(t, 0, acquired[1].Attempts)

	storedFirst, ok, err := repo.GetByID(ctx, acquired[0].ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.StatusPending, storedFirst.Status)

	stillPending, ok, err := repo.GetByID(ctx, job3.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.StatusPending, stillPending.Status)
}

func TestSaveAppliesAggregateChanges(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	job := newJob(t, "doc-1")
	created, _, err := repo.UpsertOrGetActive(ctx, job, 10)
	require.NoError(t, err)

	aggregate := domain.NewTableExtractionJobAggregate(created)
	updatedAgg, _, err := aggregate.MarkDone("finished", time.Now().UTC())
	require.NoError(t, err)

	err = repo.Save(ctx, updatedAgg.Job())
	require.NoError(t, err)

	updated, ok, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.StatusDone, updated.Status)
	require.Equal(t, "finished", updated.StatusReason)
	require.NotNil(t, updated.FinishedAt)
}

func TestSaveRestoresAfterTimeout(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	job := newJob(t, "doc-1")
	job.Status = domain.StatusProcessing
	job.Attempts = 2
	created, _, err := repo.UpsertOrGetActive(ctx, job, 10)
	require.NoError(t, err)

	nextRun := time.Now().UTC().Add(time.Hour)
	aggregate := domain.NewTableExtractionJobAggregate(created)
	restoredAgg, _, err := aggregate.RestoreAfterTimeout(nextRun, "retry later", time.Now().UTC())
	require.NoError(t, err)

	err = repo.Save(ctx, restoredAgg.Job())
	require.NoError(t, err)

	updated, ok, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.StatusPending, updated.Status)
	require.NotNil(t, updated.NextRunAt)
	require.Equal(t, "retry later", updated.StatusReason)
}

func TestListProcessingBefore(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	active := newJob(t, "doc-processing")
	active.Status = domain.StatusProcessing
	active.Attempts = 1
	active.CreatedAt = now.Add(-2 * time.Hour)
	active.UpdatedAt = now.Add(-2 * time.Hour)

	recent := newJob(t, "doc-recent")
	recent.Status = domain.StatusProcessing
	recent.Attempts = 2
	recent.CreatedAt = now.Add(-time.Hour)
	recent.UpdatedAt = now.Add(-30 * time.Minute)

	for _, job := range []domain.TableExtractionJob{active, recent} {
		_, _, err := repo.UpsertOrGetActive(ctx, job, 10)
		require.NoError(t, err)
	}

	stuck, err := repo.ListProcessingBefore(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, stuck, 1)
	require.Equal(t, active.ID, stuck[0].ID)
}

func TestCleanupAndRetryFailed(t *testing.T) {
	repo, db := setupRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()

	doneJob := newJob(t, "doc-done")
	doneJob.Status = domain.StatusDone
	doneJob.FinishedAt = ptrTime(now.Add(-48 * time.Hour))
	_, _, err := repo.UpsertOrGetActive(ctx, doneJob, 10)
	require.NoError(t, err)
	storedDone, ok, err := repo.GetByID(ctx, doneJob.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotNil(t, storedDone.FinishedAt)
	require.True(t, storedDone.FinishedAt.Before(now.Add(-24*time.Hour)))

	failedJob := newJob(t, "doc-failed")
	failedJob.Status = domain.StatusFailed
	failedJob.Attempts = 1
	failedJob.FinishedAt = ptrTime(now.Add(-time.Hour))
	createdFailed, _, err := repo.UpsertOrGetActive(ctx, failedJob, 10)
	require.NoError(t, err)

	retryable := newJob(t, "doc-retryable")
	retryable.Status = domain.StatusFailedRetryable
	retryable.Attempts = 2
	retryable.FinishedAt = ptrTime(now.Add(-2 * time.Hour))
	retryableJob, _, err := repo.UpsertOrGetActive(ctx, retryable, 10)
	require.NoError(t, err)

	cleaned, err := repo.Cleanup(ctx, now.Add(-24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, cleaned)
	_, ok, err = repo.GetByID(ctx, doneJob.ID)
	require.NoError(t, err)
	require.False(t, ok)
	var remaining int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM table_extraction_queue WHERE status IN ('done','failed')").Scan(&remaining))
	require.Equal(t, 1, remaining)
	rows, err := db.QueryContext(ctx, "SELECT status, finished_at FROM table_extraction_queue")
	require.NoError(t, err)
	defer rows.Close()
	statuses := make([]string, 0)
	for rows.Next() {
		var status string
		var finished time.Time
		require.NoError(t, rows.Scan(&status, &finished))
		statuses = append(statuses, status)
	}
	require.NoError(t, rows.Err())
	require.ElementsMatch(t, []string{
		string(domain.StatusFailed),
		string(domain.StatusFailedRetryable),
	}, statuses)

	retired, err := repo.RetryFailed(ctx, 3, time.Minute, now)
	require.NoError(t, err)
	require.Equal(t, 2, retired)

	updated, ok, err := repo.GetByID(ctx, createdFailed.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.StatusPending, updated.Status)
	require.Nil(t, updated.FinishedAt)
	require.NotNil(t, updated.NextRunAt)

	retryableUpdated, ok, err := repo.GetByID(ctx, retryableJob.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.StatusPending, retryableUpdated.Status)
}

func TestCountActive(t *testing.T) {
	repo, _ := setupRepo(t)
	ctx := context.Background()

	pending := newJob(t, "doc-pending")
	processing := newJob(t, "doc-processing")
	processing.Status = domain.StatusProcessing
	done := newJob(t, "doc-done")
	startedDone, _, err := domain.NewTableExtractionJobAggregate(done).StartProcessing(done.CreatedAt)
	require.NoError(t, err)
	doneAgg, _, err := startedDone.MarkDone("finished", done.CreatedAt)
	require.NoError(t, err)
	done = doneAgg.Job()

	for _, job := range []domain.TableExtractionJob{pending, processing, done} {
		_, _, err := repo.UpsertOrGetActive(ctx, job, 10)
		require.NoError(t, err)
	}

	active, err := repo.CountActive(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, active)
}

func setupRepo(t *testing.T) (repository.TableExtractionRepository, *sql.DB) {
	t.Helper()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
	})

	ctx := context.Background()
	require.NoError(t, migrations.Apply(ctx, db, zap.NewNop()))

	return sqlrepo.NewSQLRepository(db, database.DialectSQLite), db
}

func newJob(t *testing.T, documentID string) domain.TableExtractionJob {
	t.Helper()
	jobID := uuid.New()
	prefix := path.Join("tables", documentID, jobID.String())
	job, err := domain.NewTableExtractionJob(domain.NewTableExtractionJobParams{
		ID:              jobID,
		DocumentID:      documentID,
		Original:        domain.ArtifactLocation{Bucket: "originals", ObjectKey: documentID + ".pdf"},
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: prefix,
		CorrelationID:   "corr-" + documentID,
		CreatedAt:       time.Now().UTC(),
	})
	require.NoError(t, err)
	return job
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
