package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartProcessingTransitionsFromPending(t *testing.T) {
	now := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	aggregate := NewTableExtractionJobAggregate(newAggregateJob(t, now.Add(-time.Minute)))

	updated, meta, err := aggregate.StartProcessing(now)
	require.NoError(t, err)
	job := updated.Job()

	require.Equal(t, StatusProcessing, job.Status)
	require.Equal(t, 1, job.Attempts)
	require.Equal(t, now, job.UpdatedAt)
	require.Equal(t, StatusPending, meta.FromStatus)
	require.Equal(t, StatusProcessing, meta.ToStatus)
}

func TestMarkFailedSupportsRetryableFlag(t *testing.T) {
	now := time.Date(2024, 6, 1, 11, 0, 0, 0, time.UTC)
	processing := newProcessingJob(t, now.Add(-time.Hour), now.Add(-time.Hour))

	aggregate := NewTableExtractionJobAggregate(processing)
	retryableAgg, meta, err := aggregate.MarkFailed("upload failed", "upload_error", "timeout", true, now)
	require.NoError(t, err)
	retryable := retryableAgg.Job()
	require.Equal(t, StatusFailedRetryable, retryable.Status)
	require.Equal(t, "upload_error", retryable.ErrorCode)
	require.Equal(t, StatusProcessing, meta.FromStatus)
	require.Equal(t, StatusFailedRetryable, meta.ToStatus)
	require.False(t, meta.Finished)
	require.Nil(t, retryable.FinishedAt)

	nonRetryableAgg := NewTableExtractionJobAggregate(processing)
	nonRetryableAgg, meta, err = nonRetryableAgg.MarkFailed("fatal", "fatal_error", "boom", false, now)
	require.NoError(t, err)
	failed := nonRetryableAgg.Job()
	require.Equal(t, StatusFailed, failed.Status)
	require.Equal(t, "fatal_error", failed.ErrorCode)
	require.Equal(t, StatusFailed, meta.ToStatus)
	require.True(t, meta.Finished)
	require.NotNil(t, failed.FinishedAt)
}

func TestMarkDoneAndRestoreAndRetry(t *testing.T) {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	aggregate := NewTableExtractionJobAggregate(newProcessingJob(t, now.Add(-time.Minute), now.Add(-time.Minute)))

	doneAgg, meta, err := aggregate.MarkDone("completed", now)
	require.NoError(t, err)
	done := doneAgg.Job()
	require.Equal(t, StatusDone, done.Status)
	require.Equal(t, "completed", done.StatusReason)
	require.NotNil(t, done.FinishedAt)
	require.True(t, meta.Finished)

	nextRun := now.Add(time.Minute)
	restoreAgg := NewTableExtractionJobAggregate(newProcessingJob(t, now.Add(-2*time.Minute), now.Add(-2*time.Minute)))
	restoredAgg, meta, err := restoreAgg.RestoreAfterTimeout(nextRun, "restore", now)
	require.NoError(t, err)
	restored := restoredAgg.Job()
	require.Equal(t, StatusPending, restored.Status)
	require.NotNil(t, restored.NextRunAt)
	require.WithinDuration(t, nextRun, *restored.NextRunAt, time.Millisecond)
	require.Equal(t, "restore", restored.StatusReason)
	require.Equal(t, StatusProcessing, meta.FromStatus)
	require.Equal(t, StatusPending, meta.ToStatus)

	failedJob := newFailedJob(t, now.Add(-time.Hour), now.Add(-time.Hour), now.Add(-30*time.Minute), false)
	retryAgg := NewTableExtractionJobAggregate(failedJob)
	retriedAgg, meta, err := retryAgg.Retry(now, time.Second)
	require.NoError(t, err)
	retried := retriedAgg.Job()
	require.Equal(t, StatusPending, retried.Status)
	require.NotNil(t, retried.NextRunAt)
	require.WithinDuration(t, now.Add(time.Second), *retried.NextRunAt, time.Millisecond)
	require.Equal(t, StatusFailed, meta.FromStatus)
	require.Equal(t, StatusPending, meta.ToStatus)
}

func TestInvalidTransitions(t *testing.T) {
	now := time.Date(2024, 6, 1, 13, 0, 0, 0, time.UTC)
	doneJob := newDoneJob(t, now.Add(-time.Hour), now.Add(-time.Hour), now.Add(-30*time.Minute))
	aggregate := NewTableExtractionJobAggregate(doneJob)

	_, _, err := aggregate.StartProcessing(now)
	require.ErrorIs(t, err, ErrInvalidTransition)

	secondDone := newDoneJob(t, now.Add(-2*time.Hour), now.Add(-2*time.Hour), now.Add(-90*time.Minute))
	aggregate = NewTableExtractionJobAggregate(secondDone)

	_, _, err = aggregate.MarkFailed("fail", "code", "msg", false, now)
	require.ErrorIs(t, err, ErrInvalidTransition)
}

func newAggregateJob(t *testing.T, createdAt time.Time) TableExtractionJob {
	t.Helper()

	job, err := NewTableExtractionJob(NewTableExtractionJobParams{
		DocumentID:      "doc-aggregate",
		Original:        ArtifactLocation{Bucket: "orig", ObjectKey: "doc-aggregate.pdf"},
		ArtifactsBucket: "artifacts",
		ArtifactsPrefix: "tables/doc-aggregate",
		CorrelationID:   "corr-aggregate",
		CreatedAt:       createdAt,
	})
	require.NoError(t, err)
	return job
}

func newProcessingJob(t *testing.T, createdAt, startedAt time.Time) TableExtractionJob {
	job := newAggregateJob(t, createdAt)
	aggregate := NewTableExtractionJobAggregate(job)
	started, _, err := aggregate.StartProcessing(startedAt)
	require.NoError(t, err)
	return started.Job()
}

func newFailedJob(t *testing.T, createdAt, startedAt, failedAt time.Time, retryable bool) TableExtractionJob {
	processing := newProcessingJob(t, createdAt, startedAt)
	aggregate := NewTableExtractionJobAggregate(processing)
	failedAgg, _, err := aggregate.MarkFailed("failed", "failed_error", "failed message", retryable, failedAt)
	require.NoError(t, err)
	return failedAgg.Job()
}

func newDoneJob(t *testing.T, createdAt, startedAt, finishedAt time.Time) TableExtractionJob {
	processing := newProcessingJob(t, createdAt, startedAt)
	aggregate := NewTableExtractionJobAggregate(processing)
	doneAgg, _, err := aggregate.MarkDone("done", finishedAt)
	require.NoError(t, err)
	return doneAgg.Job()
}
