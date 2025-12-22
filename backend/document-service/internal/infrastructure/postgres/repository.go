package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"

	"backend-document-service/internal/domain"
)

type Repository struct {
	db *sql.DB
}

func (r *Repository) withTxRetry(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error {
	const maxAttempts = 3

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		tx, err := r.db.BeginTx(ctx, opts)
		if err != nil {
			lastErr = err
			if r.retryAfterConnectionError(ctx, attempt, maxAttempts, err) {
				continue
			}
			return err
		}

		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			lastErr = err
			if r.retryAfterConnectionError(ctx, attempt, maxAttempts, err) {
				continue
			}
			return err
		}

		if err := tx.Commit(); err != nil {
			_ = tx.Rollback()
			lastErr = err
			if r.retryAfterConnectionError(ctx, attempt, maxAttempts, err) {
				continue
			}
			return err
		}

		return nil
	}

	return lastErr
}

func (r *Repository) retryAfterConnectionError(ctx context.Context, attempt, maxAttempts int, err error) bool {
	if !isConnectionError(err) || attempt+1 >= maxAttempts {
		return false
	}

	if pingErr := r.db.PingContext(ctx); pingErr != nil {
		return false
	}

	return true
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	return errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) || errors.As(err, &netErr)
}

// AddJobEvent добавляет запись о событии фоновой задачи.
func (r *Repository) AddJobEvent(ctx context.Context, event *domain.JobEvent) error {
	if event == nil {
		return errors.New("event is required")
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}

	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}

	_, err = r.db.ExecContext(ctx, `
        INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
    `, event.ID, event.JobID, event.Stage, event.Status, event.EventType, event.Message, payload, event.CreatedAt)
	return err
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateDocument(ctx context.Context, doc *domain.Document, version *domain.DocumentVersion, log *domain.DocumentStatusLog) (*domain.Document, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if doc.ID == "" {
		doc.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now

	_, err = tx.ExecContext(ctx, `
        INSERT INTO documents (id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
    `, doc.ID, doc.Name, doc.Owner, doc.Repository, doc.Branch, doc.BaseBranch, doc.Path, string(doc.Status), doc.WorkflowID, doc.OriginalBucket, doc.OriginalObjectKey, doc.MarkdownBucket, doc.MarkdownObjectKey, doc.ErrorReason, doc.CreatePR, doc.CreatedAt, doc.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if version != nil {
		version.ID = uuid.NewString()
		version.DocumentID = doc.ID
		version.CreatedAt = now
		version.UpdatedAt = now
		if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_versions (id, document_id, version, original_object_key, original_etag, markdown_object_key, markdown_queue_id, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
        `, version.ID, version.DocumentID, version.Version, version.OriginalObjectKey, version.OriginalETag, version.MarkdownObjectKey, version.MarkdownQueueID, version.CreatedAt, version.UpdatedAt); err != nil {
			return nil, err
		}
		doc.Versions = []domain.DocumentVersion{*version}
	}

	if log != nil {
		log.ID = uuid.NewString()
		log.DocumentID = doc.ID
		log.WorkflowID = doc.WorkflowID
		log.CreatedAt = now

		var fromStatus sql.NullString
		if log.FromStatus != "" {
			fromStatus.Valid = true
			fromStatus.String = string(log.FromStatus)
		} // иначе Valid=false → NULL в БД

		if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_status_logs (id, document_id, from_status, to_status, workflow_id, reason, created_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7)
        `, log.ID, log.DocumentID, fromStatus, string(log.ToStatus), log.WorkflowID, log.Reason, log.CreatedAt); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return doc, nil
}

func (r *Repository) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at
FROM documents WHERE id=$1
`, id)

	var doc domain.Document
	if err := row.Scan(&doc.ID, &doc.Name, &doc.Owner, &doc.Repository, &doc.Branch, &doc.BaseBranch, &doc.Path, &doc.Status, &doc.WorkflowID, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.ErrorReason, &doc.CreatePR, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
		return nil, err
	}
	versions, err := r.fetchVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	doc.Versions = versions
	if err := r.attachActiveJob(ctx, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r *Repository) LastNonFailedStatus(ctx context.Context, id string) (domain.DocumentStatus, error) {
	row := r.db.QueryRowContext(ctx, `
        SELECT to_status FROM document_status_logs WHERE document_id=$1 AND to_status <> $2 ORDER BY created_at DESC LIMIT 1
    `, id, string(domain.DocumentStatusFailed))

	var status sql.NullString
	if err := row.Scan(&status); err != nil {
		return "", err
	}
	if !status.Valid {
		return "", sql.ErrNoRows
	}

	return domain.DocumentStatus(strings.TrimSpace(status.String)), nil
}

func (r *Repository) CountStatusLogsWithReasonPrefix(ctx context.Context, id string, status domain.DocumentStatus, reasonPrefix string) (int, error) {
	row := r.db.QueryRowContext(ctx, `
        SELECT count(*) FROM document_status_logs WHERE document_id=$1 AND to_status=$2 AND reason LIKE $3
    `, id, string(status), reasonPrefix+"%")

	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *Repository) ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) ([]domain.Document, error) {
	base := `SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at FROM documents`
	args := []any{}
	if status != nil {
		base += " WHERE status=$1"
		args = append(args, string(*status))
	}
	base += " ORDER BY created_at DESC"
	if limit > 0 {
		args = append(args, limit)
		base += " LIMIT $" + itoa(len(args))
	}
	if offset > 0 {
		args = append(args, offset)
		base += " OFFSET $" + itoa(len(args))
	}

	rows, err := r.db.QueryContext(ctx, base, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []domain.Document
	for rows.Next() {
		var doc domain.Document
		if err := rows.Scan(&doc.ID, &doc.Name, &doc.Owner, &doc.Repository, &doc.Branch, &doc.BaseBranch, &doc.Path, &doc.Status, &doc.WorkflowID, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.ErrorReason, &doc.CreatePR, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		if err := r.attachActiveJob(ctx, &doc); err != nil {
			return nil, err
		}
		result = append(result, doc)
	}
	return result, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var doc domain.Document
	row := tx.QueryRowContext(ctx, `
        SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at
        FROM documents WHERE id=$1 FOR UPDATE
    `, id)
	if err := row.Scan(&doc.ID, &doc.Name, &doc.Owner, &doc.Repository, &doc.Branch, &doc.BaseBranch, &doc.Path, &doc.Status, &doc.WorkflowID, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.ErrorReason, &doc.CreatePR, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
		return nil, err
	}

	if err := domain.ValidateDocumentStatus(to); err != nil {
		return nil, err
	}
	if err := domain.ValidateDocumentStatusTransition(doc.Status, to); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	statusChanged := doc.Status != to
	docUpdated := statusChanged
	versionInserted := false

	if version != nil {
		inputOriginalETag := version.OriginalETag
		existing, err := r.getVersionTx(ctx, tx, id, version.Version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if existing != nil {
			if (version.MarkdownObjectKey != "" && existing.MarkdownObjectKey != version.MarkdownObjectKey) ||
				(version.OriginalObjectKey != "" && existing.OriginalObjectKey != version.OriginalObjectKey) ||
				(version.OriginalETag != "" && existing.OriginalETag != "" && existing.OriginalETag != version.OriginalETag) {
				return nil, fmt.Errorf("version %d already stored with different object keys", version.Version)
			}
			version = existing
			if version.OriginalETag == "" {
				version.OriginalETag = inputOriginalETag
			}
			if inputOriginalETag != "" && existing.OriginalETag == "" {
				version.UpdatedAt = now
				if _, err = tx.ExecContext(ctx, `
            UPDATE document_versions SET original_etag=$1, updated_at=$2 WHERE id=$3
        `, inputOriginalETag, version.UpdatedAt, version.ID); err != nil {
					return nil, err
				}
			}
		} else {
			version.ID = uuid.NewString()
			version.DocumentID = id
			version.CreatedAt = now
			version.UpdatedAt = now
			if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_versions (id, document_id, version, original_object_key, original_etag, markdown_object_key, markdown_queue_id, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
        `, version.ID, version.DocumentID, version.Version, version.OriginalObjectKey, version.OriginalETag, version.MarkdownObjectKey, version.MarkdownQueueID, version.CreatedAt, version.UpdatedAt); err != nil {
				return nil, err
			}
			versionInserted = true
		}
		if version.OriginalObjectKey != "" && doc.OriginalObjectKey != version.OriginalObjectKey {
			doc.OriginalObjectKey = version.OriginalObjectKey
			docUpdated = true
		}
		if version.MarkdownObjectKey != "" && doc.MarkdownObjectKey != version.MarkdownObjectKey {
			doc.MarkdownObjectKey = version.MarkdownObjectKey
			docUpdated = true
		}
		if versionInserted && !docUpdated {
			docUpdated = true
		}
	}

	if errorReason != "" && doc.ErrorReason != errorReason {
		doc.ErrorReason = errorReason
		docUpdated = true
	}

	if docUpdated {
		previousStatus := doc.Status
		doc.Status = to
		doc.UpdatedAt = now
		if _, err = tx.ExecContext(ctx, `
            UPDATE documents SET status=$1, original_object_key=$2, markdown_object_key=$3, error_reason=$4, updated_at=$5 WHERE id=$6
        `, string(doc.Status), doc.OriginalObjectKey, doc.MarkdownObjectKey, doc.ErrorReason, doc.UpdatedAt, id); err != nil {
			return nil, err
		}
		if statusChanged || versionInserted || reason != "" {
			log := domain.DocumentStatusLog{
				ID:         uuid.NewString(),
				DocumentID: id,
				FromStatus: previousStatus,
				ToStatus:   to,
				WorkflowID: doc.WorkflowID,
				Reason:     reason,
				CreatedAt:  now,
			}

			var fromStatus sql.NullString
			if log.FromStatus != "" {
				fromStatus.Valid = true
				fromStatus.String = string(log.FromStatus)
			}

			if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_status_logs (id, document_id, from_status, to_status, workflow_id, reason, created_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7)
        `, log.ID, log.DocumentID, fromStatus, string(log.ToStatus), log.WorkflowID, log.Reason, log.CreatedAt); err != nil {
				return nil, err
			}
		}
	}

	versions, err := r.fetchVersionsTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	doc.Versions = versions

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	if err := r.attachActiveJob(ctx, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// UpdateWorkflowID сохраняет идентификатор запущенного workflow и возвращает актуальное состояние документа.
func (r *Repository) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*domain.Document, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var doc domain.Document
	row := tx.QueryRowContext(ctx, `
        SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at
        FROM documents WHERE id=$1 FOR UPDATE
    `, id)
	if err := row.Scan(&doc.ID, &doc.Name, &doc.Owner, &doc.Repository, &doc.Branch, &doc.BaseBranch, &doc.Path, &doc.Status, &doc.WorkflowID, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.ErrorReason, &doc.CreatePR, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `
        UPDATE documents SET workflow_id=$1, updated_at=$2 WHERE id=$3
    `, workflowID, now, id); err != nil {
		return nil, err
	}

	doc.WorkflowID = workflowID
	doc.UpdatedAt = now

	versions, err := r.fetchVersionsTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	doc.Versions = versions

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	if err := r.attachActiveJob(ctx, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ActiveJob возвращает активную задачу для документа, если она есть.
func (r *Repository) ActiveJob(ctx context.Context, documentID string) (*domain.ProcessingJob, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE document_id=$1 AND status IN ($2,$3)
ORDER BY created_at DESC
LIMIT 1
`, documentID, domain.JobStatusPending, domain.JobStatusInProgress)

	var job domain.ProcessingJob
	var payloadRaw json.RawMessage
	if err := row.Scan(&job.TaskID, &job.DocumentID, &job.Stage, &job.Status, &job.Attempts, &job.NextRunAt, &payloadRaw, &job.CreatedAt, &job.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	_ = json.Unmarshal(payloadRaw, &job.Payload)
	return &job, nil
}

// ListFinishedJobs возвращает завершённые или окончательно упавшие задачи и откладывает их повторную обработку.
func (r *Repository) ListFinishedJobs(ctx context.Context, limit int, postpone time.Duration) ([]domain.ProcessingJob, error) {
	if limit <= 0 {
		limit = 100
	}
	if postpone <= 0 {
		postpone = time.Minute
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `
SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE status IN ($1,$2) AND next_run_at <= $3
ORDER BY next_run_at
LIMIT $4
FOR UPDATE SKIP LOCKED
`, domain.JobStatusCompleted, domain.JobStatusFailed, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []domain.ProcessingJob
	for rows.Next() {
		var job domain.ProcessingJob
		var payloadRaw json.RawMessage
		if err := rows.Scan(&job.TaskID, &job.DocumentID, &job.Stage, &job.Status, &job.Attempts, &job.NextRunAt, &payloadRaw, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payloadRaw, &job.Payload)
		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	nextRun := now.Add(postpone)
	for _, job := range jobs {
		if _, err := tx.ExecContext(ctx, `
        UPDATE processing_jobs SET next_run_at=$1, updated_at=$2 WHERE task_id=$3
    `, nextRun, now, job.TaskID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return jobs, nil
}

// AcquirePendingJobs выбирает pending-задачи для указанной стадии и помечает их in_progress.
func (r *Repository) AcquirePendingJobs(ctx context.Context, stage domain.JobStage, limit int) ([]domain.ProcessingJob, error) {
	if err := domain.ValidateJobStage(stage); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `
SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE stage=$1 AND status=$2 AND next_run_at <= $3
ORDER BY next_run_at
LIMIT $4
FOR UPDATE SKIP LOCKED
`, stage, domain.JobStatusPending, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []domain.ProcessingJob
	for rows.Next() {
		var job domain.ProcessingJob
		var payloadRaw json.RawMessage
		if err := rows.Scan(&job.TaskID, &job.DocumentID, &job.Stage, &job.Status, &job.Attempts, &job.NextRunAt, &payloadRaw, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payloadRaw, &job.Payload)
		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range jobs {
		jobs[i].Status = domain.JobStatusInProgress
		jobs[i].Attempts++
		jobs[i].UpdatedAt = now
		payloadBytes, err := json.Marshal(jobs[i].Payload)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
        UPDATE processing_jobs SET status=$1, attempts=$2, updated_at=$3, payload=$4 WHERE task_id=$5
    `, jobs[i].Status, jobs[i].Attempts, jobs[i].UpdatedAt, payloadBytes, jobs[i].TaskID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return jobs, nil
}

// RequeueStaleProcessingJobs возвращает зависшие in_progress задачи в pending и фиксирует причину восстановления.
func (r *Repository) RequeueStaleProcessingJobs(ctx context.Context, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error) {
	if limit <= 0 {
		limit = 100
	}

	var jobs []domain.ProcessingJob
	err := r.withTxRetry(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(tx *sql.Tx) error {
		var innerErr error
		jobs, innerErr = r.requeueStaleProcessingJobsTx(ctx, tx, staleBefore, limit, nextRunAt, reason)
		return innerErr
	})

	if err != nil {
		return nil, err
	}

	return jobs, nil
}

func (r *Repository) requeueStaleProcessingJobsTx(ctx context.Context, tx *sql.Tx, staleBefore time.Time, limit int, nextRunAt time.Time, reason string) ([]domain.ProcessingJob, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
FROM processing_jobs
WHERE status=$1 AND updated_at <= $2
ORDER BY updated_at
LIMIT $3
FOR UPDATE SKIP LOCKED
`, domain.JobStatusInProgress, staleBefore.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	var jobs []domain.ProcessingJob
	for rows.Next() {
		var job domain.ProcessingJob
		var payloadRaw json.RawMessage
		if err := rows.Scan(&job.TaskID, &job.DocumentID, &job.Stage, &job.Status, &job.Attempts, &job.NextRunAt, &payloadRaw, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(payloadRaw, &job.Payload)

		job.Status = domain.JobStatusPending
		job.Attempts++
		job.NextRunAt = nextRunAt
		job.UpdatedAt = now
		if job.Payload == nil {
			job.Payload = map[string]any{}
		}

		payloadBytes, err := json.Marshal(job.Payload)
		if err != nil {
			return nil, err
		}

		if _, err := tx.ExecContext(ctx, `
        UPDATE processing_jobs SET status=$1, attempts=$2, next_run_at=$3, payload=$4, updated_at=$5 WHERE task_id=$6
    `, job.Status, job.Attempts, job.NextRunAt, payloadBytes, job.UpdatedAt, job.TaskID); err != nil {
			return nil, err
		}

		eventPayload := map[string]any{"reason": reason, "previous_status": string(domain.JobStatusInProgress)}
		payloadEventBytes, err := json.Marshal(eventPayload)
		if err != nil {
			return nil, err
		}

		if _, err := tx.ExecContext(ctx, `
        INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
    `, uuid.NewString(), job.TaskID, job.Stage, job.Status, "recovered", reason, payloadEventBytes, now); err != nil {
			return nil, err
		}

		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return jobs, nil
}

func (r *Repository) attachActiveJob(ctx context.Context, doc *domain.Document) error {
	if doc == nil {
		return nil
	}
	job, err := r.ActiveJob(ctx, doc.ID)
	if err != nil {
		return err
	}
	if job != nil {
		doc.ActiveJobID = job.TaskID
	}
	return nil
}

func (r *Repository) CreateProcessingJob(ctx context.Context, job *domain.ProcessingJob, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	if job == nil {
		return nil, errors.New("job is required")
	}
	if err := domain.ValidateJobStage(job.Stage); err != nil {
		return nil, err
	}
	if job.Status == "" {
		job.Status = domain.JobStatusPending
	}
	if err := domain.ValidateJobStatus(job.Status); err != nil {
		return nil, err
	}
	if job.DocumentID == "" {
		return nil, errors.New("document_id is required")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	if job.TaskID == "" {
		job.TaskID = uuid.NewString()
	}
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.Payload == nil {
		job.Payload = map[string]any{}
	}

	payloadBytes, err := json.Marshal(job.Payload)
	if err != nil {
		return nil, err
	}

	row := tx.QueryRowContext(ctx, `
        INSERT INTO processing_jobs (task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
        ON CONFLICT (document_id, stage) DO UPDATE
        SET status = EXCLUDED.status,
            attempts = EXCLUDED.attempts,
            next_run_at = EXCLUDED.next_run_at,
            payload = EXCLUDED.payload,
            updated_at = EXCLUDED.updated_at
        RETURNING task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
    `, job.TaskID, job.DocumentID, job.Stage, job.Status, job.Attempts, now, payloadBytes, job.CreatedAt, job.UpdatedAt)

	var payload json.RawMessage
	if err := row.Scan(&job.TaskID, &job.DocumentID, &job.Stage, &job.Status, &job.Attempts, &job.NextRunAt, &payload, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(payload, &job.Payload)

	if event != nil {
		if event.ID == "" {
			event.ID = uuid.NewString()
		}
		event.JobID = job.TaskID
		event.Stage = job.Stage
		if event.Status == "" {
			event.Status = job.Status
		}
		event.CreatedAt = now
		if event.Payload == nil {
			event.Payload = map[string]any{}
		}
		eventPayload, err := json.Marshal(event.Payload)
		if err != nil {
			return nil, err
		}

		if _, err := tx.ExecContext(ctx, `
            INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        `, event.ID, event.JobID, event.Stage, event.Status, event.EventType, event.Message, eventPayload, event.CreatedAt); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *Repository) UpdateProcessingJob(ctx context.Context, taskID string, status domain.JobStatus, nextRunAt *time.Time, incrementAttempt bool, payload map[string]any, event *domain.JobEvent) (*domain.ProcessingJob, error) {
	if taskID == "" {
		return nil, errors.New("task_id is required")
	}
	if err := domain.ValidateJobStatus(status); err != nil {
		return nil, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var job domain.ProcessingJob
	row := tx.QueryRowContext(ctx, `
        SELECT task_id, document_id, stage, status, attempts, next_run_at, payload, created_at, updated_at
        FROM processing_jobs WHERE task_id=$1 FOR UPDATE
    `, taskID)
	var payloadRaw json.RawMessage
	if err := row.Scan(&job.TaskID, &job.DocumentID, &job.Stage, &job.Status, &job.Attempts, &job.NextRunAt, &payloadRaw, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(payloadRaw, &job.Payload)

	now := time.Now().UTC()
	if incrementAttempt {
		job.Attempts++
	}
	job.Status = status
	job.UpdatedAt = now
	if nextRunAt != nil {
		job.NextRunAt = *nextRunAt
	}
	if payload != nil {
		job.Payload = payload
	}

	payloadBytes, err := json.Marshal(job.Payload)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
        UPDATE processing_jobs SET status=$1, attempts=$2, next_run_at=$3, payload=$4, updated_at=$5 WHERE task_id=$6
    `, job.Status, job.Attempts, job.NextRunAt, payloadBytes, job.UpdatedAt, job.TaskID); err != nil {
		return nil, err
	}

	if event != nil {
		if event.ID == "" {
			event.ID = uuid.NewString()
		}
		event.JobID = job.TaskID
		event.Stage = job.Stage
		if event.Status == "" {
			event.Status = job.Status
		}
		event.CreatedAt = now
		if event.Payload == nil {
			event.Payload = map[string]any{}
		}
		eventPayload, err := json.Marshal(event.Payload)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO job_events (id, job_id, stage, status, event_type, message, payload, created_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
        `, event.ID, event.JobID, event.Stage, event.Status, event.EventType, event.Message, eventPayload, event.CreatedAt); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return &job, nil
}

func (r *Repository) fetchVersions(ctx context.Context, documentID string) ([]domain.DocumentVersion, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, document_id, version, original_object_key, original_etag, markdown_object_key, markdown_queue_id, created_at, updated_at
        FROM document_versions WHERE document_id=$1 ORDER BY version
    `, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []domain.DocumentVersion
	for rows.Next() {
		var v domain.DocumentVersion
		if err := rows.Scan(&v.ID, &v.DocumentID, &v.Version, &v.OriginalObjectKey, &v.OriginalETag, &v.MarkdownObjectKey, &v.MarkdownQueueID, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

func (r *Repository) getVersionTx(ctx context.Context, tx *sql.Tx, documentID string, version int32) (*domain.DocumentVersion, error) {
	row := tx.QueryRowContext(ctx, `
        SELECT id, document_id, version, original_object_key, original_etag, markdown_object_key, markdown_queue_id, created_at, updated_at
        FROM document_versions WHERE document_id=$1 AND version=$2
    `, documentID, version)
	var v domain.DocumentVersion
	if err := row.Scan(&v.ID, &v.DocumentID, &v.Version, &v.OriginalObjectKey, &v.OriginalETag, &v.MarkdownObjectKey, &v.MarkdownQueueID, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repository) fetchVersionsTx(ctx context.Context, tx *sql.Tx, documentID string) ([]domain.DocumentVersion, error) {
	rows, err := tx.QueryContext(ctx, `
        SELECT id, document_id, version, original_object_key, original_etag, markdown_object_key, markdown_queue_id, created_at, updated_at
        FROM document_versions WHERE document_id=$1 ORDER BY version
    `, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []domain.DocumentVersion
	for rows.Next() {
		var v domain.DocumentVersion
		if err := rows.Scan(&v.ID, &v.DocumentID, &v.Version, &v.OriginalObjectKey, &v.OriginalETag, &v.MarkdownObjectKey, &v.MarkdownQueueID, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

func itoa(v int) string {
	const digits = "0123456789"
	if v == 0 {
		return "0"
	}
	res := ""
	for v > 0 {
		res = string(digits[v%10]) + res
		v /= 10
	}
	return res
}

var ErrNotFound = errors.New("not found")
