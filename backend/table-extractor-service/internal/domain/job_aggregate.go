package domain

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidTransition = errors.New("invalid status transition")

// TransitionMetadata содержит информацию об изменении статуса агрегата.
type TransitionMetadata struct {
	FromStatus TableExtractionStatus
	ToStatus   TableExtractionStatus

	StatusReason string
	ErrorCode    string
	ErrorMessage string

	Attempts  int
	Finished  bool
	NextRunAt *time.Time
}

// TableExtractionJobAggregate инкапсулирует доменные переходы для задачи извлечения таблиц.
type TableExtractionJobAggregate struct {
	job TableExtractionJob
}

func NewTableExtractionJobAggregate(job TableExtractionJob) TableExtractionJobAggregate {
	if job.Artifacts == (TableArtifacts{}) {
		job.Artifacts = BuildArtifacts(job.ArtifactsBucket, job.ArtifactsPrefix)
	}
	return TableExtractionJobAggregate{job: job}
}

func (a TableExtractionJobAggregate) Job() TableExtractionJob {
	return a.job
}

func (a TableExtractionJobAggregate) StartProcessing(now time.Time) (TableExtractionJobAggregate, TransitionMetadata, error) {
	if a.job.Status != StatusPending {
		return a, TransitionMetadata{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, a.job.Status, StatusProcessing)
	}

	from := a.job.Status
	a.job.Status = StatusProcessing
	a.job.Attempts++
	a.job.NextRunAt = nil
	a.job.UpdatedAt = now

	meta := TransitionMetadata{
		FromStatus: from,
		ToStatus:   StatusProcessing,
		Attempts:   a.job.Attempts,
	}
	return a, meta, nil
}

func (a TableExtractionJobAggregate) MarkFailed(reason, code, message string, retryable bool, now time.Time) (TableExtractionJobAggregate, TransitionMetadata, error) {
	targetStatus := StatusFailed
	if retryable {
		targetStatus = StatusFailedRetryable
	}

	if a.job.Status != StatusProcessing && a.job.Status != StatusPending && a.job.Status != StatusFailedRetryable {
		return a, TransitionMetadata{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, a.job.Status, targetStatus)
	}

	from := a.job.Status
	var finishedAt *time.Time
	if !retryable {
		finished := now
		finishedAt = &finished
	}
	a.job.Status = targetStatus
	a.job.StatusReason = reason
	a.job.ErrorCode = code
	a.job.ErrorMessage = message
	a.job.FinishedAt = finishedAt
	a.job.NextRunAt = nil
	a.job.UpdatedAt = now

	meta := TransitionMetadata{
		FromStatus:   from,
		ToStatus:     a.job.Status,
		StatusReason: reason,
		ErrorCode:    code,
		ErrorMessage: message,
		Attempts:     a.job.Attempts,
		Finished:     !retryable,
	}
	return a, meta, nil
}

func (a TableExtractionJobAggregate) MarkDone(reason string, now time.Time) (TableExtractionJobAggregate, TransitionMetadata, error) {
	if a.job.Status != StatusProcessing && a.job.Status != StatusPending {
		return a, TransitionMetadata{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, a.job.Status, StatusDone)
	}

	from := a.job.Status
	finishedAt := now
	a.job.Status = StatusDone
	a.job.StatusReason = reason
	a.job.ErrorCode = ""
	a.job.ErrorMessage = ""
	a.job.FinishedAt = &finishedAt
	a.job.NextRunAt = nil
	a.job.UpdatedAt = now

	meta := TransitionMetadata{
		FromStatus:   from,
		ToStatus:     StatusDone,
		StatusReason: reason,
		Attempts:     a.job.Attempts,
		Finished:     true,
	}
	return a, meta, nil
}

func (a TableExtractionJobAggregate) RestoreAfterTimeout(nextRunAt time.Time, reason string, now time.Time) (TableExtractionJobAggregate, TransitionMetadata, error) {
	if a.job.Status != StatusProcessing {
		return a, TransitionMetadata{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, a.job.Status, StatusPending)
	}

	from := a.job.Status
	a.job.Status = StatusPending
	a.job.StatusReason = reason
	a.job.NextRunAt = &nextRunAt
	a.job.FinishedAt = nil
	a.job.UpdatedAt = now

	meta := TransitionMetadata{
		FromStatus:   from,
		ToStatus:     StatusPending,
		StatusReason: reason,
		Attempts:     a.job.Attempts,
		NextRunAt:    &nextRunAt,
	}
	return a, meta, nil
}

func (a TableExtractionJobAggregate) Retry(now time.Time, retryDelay time.Duration) (TableExtractionJobAggregate, TransitionMetadata, error) {
	if a.job.Status != StatusFailed && a.job.Status != StatusFailedRetryable {
		return a, TransitionMetadata{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, a.job.Status, StatusPending)
	}

	from := a.job.Status
	nextRun := now.Add(retryDelay)
	a.job.Status = StatusPending
	a.job.StatusReason = ""
	a.job.NextRunAt = &nextRun
	a.job.FinishedAt = nil
	a.job.UpdatedAt = now

	meta := TransitionMetadata{
		FromStatus: from,
		ToStatus:   StatusPending,
		Attempts:   a.job.Attempts,
		NextRunAt:  &nextRun,
	}
	return a, meta, nil
}
