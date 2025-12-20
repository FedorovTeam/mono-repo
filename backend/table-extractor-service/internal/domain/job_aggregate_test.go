package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestStartProcessingTransitionsFromPending(t *testing.T) {
	now := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	aggregate := NewTableExtractionJobAggregate(TableExtractionJob{Status: StatusPending})

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
	baseJob := TableExtractionJob{Status: StatusProcessing, Attempts: 2}

	aggregate := NewTableExtractionJobAggregate(baseJob)
	retryableAgg, meta, err := aggregate.MarkFailed("upload failed", "upload_error", "timeout", true, now)
	require.NoError(t, err)
	retryable := retryableAgg.Job()
	require.Equal(t, StatusFailedRetryable, retryable.Status)
	require.Equal(t, "upload_error", retryable.ErrorCode)
	require.Equal(t, StatusProcessing, meta.FromStatus)
	require.Equal(t, StatusFailedRetryable, meta.ToStatus)
	require.False(t, meta.Finished)
	require.Nil(t, retryable.FinishedAt)

	nonRetryableAgg := NewTableExtractionJobAggregate(baseJob)
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
	aggregate := NewTableExtractionJobAggregate(TableExtractionJob{Status: StatusProcessing, Attempts: 1})

	doneAgg, meta, err := aggregate.MarkDone("completed", now)
	require.NoError(t, err)
	done := doneAgg.Job()
	require.Equal(t, StatusDone, done.Status)
	require.Equal(t, "completed", done.StatusReason)
	require.NotNil(t, done.FinishedAt)
	require.True(t, meta.Finished)

	nextRun := now.Add(time.Minute)
	restoreAgg := NewTableExtractionJobAggregate(TableExtractionJob{Status: StatusProcessing, Attempts: 1})
	restoredAgg, meta, err := restoreAgg.RestoreAfterTimeout(nextRun, "restore", now)
	require.NoError(t, err)
	restored := restoredAgg.Job()
	require.Equal(t, StatusPending, restored.Status)
	require.NotNil(t, restored.NextRunAt)
	require.WithinDuration(t, nextRun, *restored.NextRunAt, time.Millisecond)
	require.Equal(t, "restore", restored.StatusReason)
	require.Equal(t, StatusProcessing, meta.FromStatus)
	require.Equal(t, StatusPending, meta.ToStatus)

	retryAgg := NewTableExtractionJobAggregate(TableExtractionJob{Status: StatusFailed, Attempts: 2})
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
	aggregate := NewTableExtractionJobAggregate(TableExtractionJob{Status: StatusDone})

	_, _, err := aggregate.StartProcessing(now)
	require.ErrorIs(t, err, ErrInvalidTransition)

	aggregate = NewTableExtractionJobAggregate(TableExtractionJob{Status: StatusDone, ID: uuid.New()})
	_, _, err = aggregate.MarkFailed("fail", "code", "msg", false, now)
	require.ErrorIs(t, err, ErrInvalidTransition)
}
