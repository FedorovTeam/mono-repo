package sqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mono-repo/table-extractor-service/internal/domain"
	"github.com/mono-repo/table-extractor-service/internal/repository"
	"github.com/mono-repo/table-extractor-service/internal/storage/database"
)

type SQLRepository struct {
	db      *sql.DB
	dialect database.Dialect
}

func NewSQLRepository(db *sql.DB, dialect database.Dialect) repository.TableExtractionRepository {
	return &SQLRepository{
		db:      db,
		dialect: dialect,
	}
}

func (r *SQLRepository) UpsertOrGetActive(ctx context.Context, job domain.TableExtractionJob, queueLimit int) (domain.TableExtractionJob, bool, error) {
	if err := job.Validate(); err != nil {
		return domain.TableExtractionJob{}, false, fmt.Errorf("validate job: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.TableExtractionJob{}, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	activeJob, ok, err := r.getActiveByDocumentID(ctx, tx, job.DocumentID)
	if err != nil {
		return domain.TableExtractionJob{}, false, err
	}
	if ok {
		if err := tx.Commit(); err != nil {
			return domain.TableExtractionJob{}, false, fmt.Errorf("commit transaction: %w", err)
		}
		return activeJob, true, nil
	}

	if queueLimit > 0 {
		count, err := r.countActiveTx(ctx, tx)
		if err != nil {
			return domain.TableExtractionJob{}, false, err
		}
		if count >= queueLimit {
			return domain.TableExtractionJob{}, false, repository.ErrQueueLimitReached
		}
	}

	var nextRunAt any
	if job.NextRunAt != nil {
		nextRunAt = *job.NextRunAt
	}

	var finishedAt any
	if job.FinishedAt != nil {
		finishedAt = *job.FinishedAt
	}

	insertQuery := fmt.Sprintf(`INSERT INTO table_extraction_queue (
		id, document_id, original_bucket, original_object_key,
		artifacts_bucket, artifacts_prefix,
		status, status_reason, attempts, next_run_at, finished_at,
		error_code, error_message, correlation_id, created_at, updated_at,
		source_url, local_path
	) VALUES (%s)`, r.placeholders(18))

	args := []any{
		job.ID.String(),
		job.DocumentID,
		job.Original.Bucket,
		job.Original.ObjectKey,
		job.ArtifactsBucket,
		job.ArtifactsPrefix,
		string(job.Status),
		job.StatusReason,
		job.Attempts,
		nextRunAt,
		finishedAt,
		job.ErrorCode,
		job.ErrorMessage,
		job.CorrelationID,
		job.CreatedAt,
		job.UpdatedAt,
		job.SourceURL,
		job.LocalPath,
	}

	if _, err := tx.ExecContext(ctx, insertQuery, args...); err != nil {
		if r.isUniqueViolation(err) {
			conflictJob, ok, selectErr := r.getActiveByDocumentID(ctx, tx, job.DocumentID)
			if selectErr != nil {
				return domain.TableExtractionJob{}, false, selectErr
			}
			if ok {
				if err := tx.Commit(); err != nil {
					return domain.TableExtractionJob{}, false, fmt.Errorf("commit transaction: %w", err)
				}
				return conflictJob, true, nil
			}
		}
		return domain.TableExtractionJob{}, false, fmt.Errorf("insert job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return domain.TableExtractionJob{}, false, fmt.Errorf("commit transaction: %w", err)
	}

	return job, false, nil
}

func (r *SQLRepository) AcquirePendingBatch(ctx context.Context, limit int, now time.Time) ([]domain.TableExtractionJob, error) {
	return r.selectPendingBatch(ctx, limit, now, false)
}

func (r *SQLRepository) SelectPendingBatchForUpdate(ctx context.Context, limit int, now time.Time) ([]domain.TableExtractionJob, error) {
	return r.selectPendingBatch(ctx, limit, now, true)
}

func (r *SQLRepository) selectPendingBatch(ctx context.Context, limit int, now time.Time, lockForUpdate bool) ([]domain.TableExtractionJob, error) {
	if limit <= 0 {
		return nil, nil
	}

	selectQuery := fmt.Sprintf(`SELECT 
		id, document_id, original_bucket, original_object_key,
		artifacts_bucket, artifacts_prefix, source_url, local_path,
		status, status_reason, attempts, next_run_at, finished_at,
		error_code, error_message, correlation_id, created_at, updated_at
		FROM table_extraction_queue
		WHERE status = %s AND (next_run_at IS NULL OR next_run_at <= %s)
		ORDER BY (next_run_at IS NOT NULL), next_run_at, created_at
		LIMIT %s`, r.placeholder(1), r.placeholder(2), r.placeholder(3))

	if lockForUpdate && r.dialect == database.DialectPostgres {
		selectQuery += " FOR UPDATE SKIP LOCKED"
	}

	scanRows := func(rows *sql.Rows) ([]domain.TableExtractionJob, error) {
		defer rows.Close()

		var jobs []domain.TableExtractionJob
		for rows.Next() {
			job, ok, err := r.scanJob(rows)
			if err != nil {
				return nil, err
			}
			if ok {
				jobs = append(jobs, job)
			}
		}

		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate pending: %w", err)
		}

		if len(jobs) == 0 {
			return nil, nil
		}
		return jobs, nil
	}

	if lockForUpdate {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("begin transaction: %w", err)
		}
		defer tx.Rollback()

		rows, err := tx.QueryContext(ctx, selectQuery, string(domain.StatusPending), now, limit)
		if err != nil {
			return nil, fmt.Errorf("select pending: %w", err)
		}

		jobs, err := scanRows(rows)
		if err != nil {
			return nil, err
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit transaction: %w", err)
		}
		return jobs, nil
	}

	rows, err := r.db.QueryContext(ctx, selectQuery, string(domain.StatusPending), now, limit)
	if err != nil {
		return nil, fmt.Errorf("select pending: %w", err)
	}

	return scanRows(rows)
}

func (r *SQLRepository) Save(ctx context.Context, job domain.TableExtractionJob) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save: %w", err)
	}
	defer tx.Rollback()

	if err := r.saveTx(ctx, tx, job); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save: %w", err)
	}
	return nil
}

func (r *SQLRepository) ListProcessingBefore(ctx context.Context, updatedBefore time.Time) ([]domain.TableExtractionJob, error) {
	query := fmt.Sprintf(`SELECT 
		id, document_id, original_bucket, original_object_key,
		artifacts_bucket, artifacts_prefix, source_url, local_path,
		status, status_reason, attempts, next_run_at, finished_at,
		error_code, error_message, correlation_id, created_at, updated_at
		FROM table_extraction_queue WHERE status = %s AND updated_at <= %s`,
		r.placeholder(1),
		r.placeholder(2),
	)

	rows, err := r.db.QueryContext(ctx, query, string(domain.StatusProcessing), updatedBefore)
	if err != nil {
		return nil, fmt.Errorf("list processing: %w", err)
	}
	defer rows.Close()

	var jobs []domain.TableExtractionJob
	for rows.Next() {
		job, ok, err := r.scanJob(rows)
		if err != nil {
			return nil, err
		}
		if ok {
			jobs = append(jobs, job)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate processing: %w", err)
	}

	return jobs, nil
}

func (r *SQLRepository) Cleanup(ctx context.Context, finishedBefore time.Time) (int, error) {
	query := fmt.Sprintf(`DELETE FROM table_extraction_queue WHERE status IN (%s, %s) AND finished_at IS NOT NULL AND finished_at <= %s`,
		r.placeholder(1),
		r.placeholder(2),
		r.placeholder(3),
	)

	result, err := r.db.ExecContext(ctx, query, string(domain.StatusDone), string(domain.StatusFailed), finishedBefore)
	if err != nil {
		return 0, fmt.Errorf("cleanup queue: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cleanup rows affected: %w", err)
	}

	return int(rows), nil
}

func (r *SQLRepository) RetryFailed(ctx context.Context, maxAttempts int, retryDelay time.Duration, now time.Time) ([]domain.TableExtractionJob, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin retry: %w", err)
	}
	defer tx.Rollback()

	query := fmt.Sprintf(`SELECT 
		id, document_id, original_bucket, original_object_key,
		artifacts_bucket, artifacts_prefix, source_url, local_path,
		status, status_reason, attempts, next_run_at, finished_at,
		error_code, error_message, correlation_id, created_at, updated_at
		FROM table_extraction_queue
		WHERE status IN (%s, %s)`,
		r.placeholder(1),
		r.placeholder(2),
	)

	args := []any{string(domain.StatusFailed), string(domain.StatusFailedRetryable)}
	if maxAttempts > 0 {
		query += fmt.Sprintf(" AND attempts < %s", r.placeholder(len(args)+1))
		args = append(args, maxAttempts)
	}

	if r.dialect == database.DialectPostgres {
		query += " FOR UPDATE SKIP LOCKED"
	}

	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select failed jobs: %w", err)
	}
	defer rows.Close()

	var jobs []domain.TableExtractionJob
	for rows.Next() {
		job, ok, err := r.scanJob(rows)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate failed jobs: %w", err)
	}

	if len(jobs) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit retry: %w", err)
		}
		return nil, nil
	}

	updated := make([]domain.TableExtractionJob, 0, len(jobs))
	for _, job := range jobs {
		aggregate := domain.NewTableExtractionJobAggregate(job)
		updatedAgg, _, err := aggregate.Retry(now, retryDelay)
		if err != nil {
			return updated, err
		}
		updatedJob := updatedAgg.Job()
		if err := r.saveTx(ctx, tx, updatedJob); err != nil {
			return updated, err
		}
		updated = append(updated, updatedJob)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit retry: %w", err)
	}

	return updated, nil
}

func (r *SQLRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, bool, error) {
	query := fmt.Sprintf(`SELECT 
		id, document_id, original_bucket, original_object_key,
		artifacts_bucket, artifacts_prefix, source_url, local_path,
		status, status_reason, attempts, next_run_at, finished_at,
		error_code, error_message, correlation_id, created_at, updated_at
		FROM table_extraction_queue WHERE id = %s LIMIT 1`, r.placeholder(1))

	row := r.db.QueryRowContext(ctx, query, id.String())
	return r.scanJob(row)
}

func (r *SQLRepository) CountActive(ctx context.Context) (int, error) {
	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM table_extraction_queue WHERE status IN (%s, %s)`,
		r.placeholder(1),
		r.placeholder(2),
	)

	var count int
	if err := r.db.QueryRowContext(ctx, query, string(domain.StatusPending), string(domain.StatusProcessing)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active jobs: %w", err)
	}

	return count, nil
}

func (r *SQLRepository) saveTx(ctx context.Context, tx *sql.Tx, job domain.TableExtractionJob) error {
	var nextRun any
	if job.NextRunAt != nil {
		nextRun = *job.NextRunAt
	}

	var finished any
	if job.FinishedAt != nil {
		finished = *job.FinishedAt
	}

	query := fmt.Sprintf(`UPDATE table_extraction_queue SET
		status = %s,
		status_reason = %s,
		attempts = %s,
		next_run_at = %s,
		finished_at = %s,
		error_code = %s,
		error_message = %s,
		updated_at = %s
		WHERE id = %s`,
		r.placeholder(1),
		r.placeholder(2),
		r.placeholder(3),
		r.placeholder(4),
		r.placeholder(5),
		r.placeholder(6),
		r.placeholder(7),
		r.placeholder(8),
		r.placeholder(9),
	)

	_, err := tx.ExecContext(ctx, query, string(job.Status), job.StatusReason, job.Attempts, nextRun, finished, job.ErrorCode, job.ErrorMessage, job.UpdatedAt, job.ID.String())
	if err != nil {
		return fmt.Errorf("update job %s: %w", job.ID, err)
	}
	return nil
}

func (r *SQLRepository) getActiveByDocumentID(ctx context.Context, tx *sql.Tx, documentID string) (domain.TableExtractionJob, bool, error) {
	query := fmt.Sprintf(`SELECT 
		id, document_id, original_bucket, original_object_key,
		artifacts_bucket, artifacts_prefix, source_url, local_path,
		status, status_reason, attempts, next_run_at, finished_at,
		error_code, error_message, correlation_id, created_at, updated_at
		FROM table_extraction_queue WHERE document_id = %s AND status IN (%s, %s) LIMIT 1`,
		r.placeholder(1),
		r.placeholder(2),
		r.placeholder(3),
	)

	row := tx.QueryRowContext(ctx, query, documentID, string(domain.StatusPending), string(domain.StatusProcessing))
	return r.scanJob(row)
}

func (r *SQLRepository) countActiveTx(ctx context.Context, tx *sql.Tx) (int, error) {
	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM table_extraction_queue WHERE status IN (%s, %s)`,
		r.placeholder(1),
		r.placeholder(2),
	)

	var count int
	if err := tx.QueryRowContext(ctx, query, string(domain.StatusPending), string(domain.StatusProcessing)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active jobs: %w", err)
	}

	return count, nil
}

func (r *SQLRepository) scanJob(row interface{ Scan(dest ...any) error }) (domain.TableExtractionJob, bool, error) {
	var (
		id                               string
		documentID                       string
		status                           string
		statusReason                     string
		originalBucket, originalKey      string
		artifactsBucket, artifactsPrefix string
		sourceURL, localPath             string
		attempts                         int
		nextRunAt                        sql.NullTime
		finishedAt                       sql.NullTime
		errorCode                        string
		errorMessage                     string
		correlationID                    string
		createdAt                        time.Time
		updatedAt                        time.Time
	)

	if err := row.Scan(
		&id,
		&documentID,
		&originalBucket,
		&originalKey,
		&artifactsBucket,
		&artifactsPrefix,
		&sourceURL,
		&localPath,
		&status,
		&statusReason,
		&attempts,
		&nextRunAt,
		&finishedAt,
		&errorCode,
		&errorMessage,
		&correlationID,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.TableExtractionJob{}, false, nil
		}
		return domain.TableExtractionJob{}, false, fmt.Errorf("scan job: %w", err)
	}

	jobID, err := uuid.Parse(id)
	if err != nil {
		return domain.TableExtractionJob{}, false, fmt.Errorf("parse job id: %w", err)
	}

	job := domain.TableExtractionJob{
		ID:              jobID,
		DocumentID:      documentID,
		SourceURL:       sourceURL,
		LocalPath:       localPath,
		Original:        domain.ArtifactLocation{Bucket: originalBucket, ObjectKey: originalKey},
		ArtifactsBucket: artifactsBucket,
		ArtifactsPrefix: artifactsPrefix,
		Artifacts:       domain.BuildArtifacts(artifactsBucket, artifactsPrefix),
		Status:          domain.TableExtractionStatus(status),
		StatusReason:    statusReason,
		Attempts:        attempts,
		ErrorCode:       errorCode,
		ErrorMessage:    errorMessage,
		CorrelationID:   correlationID,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}

	if nextRunAt.Valid {
		job.NextRunAt = &nextRunAt.Time
	}
	if finishedAt.Valid {
		job.FinishedAt = &finishedAt.Time
	}

	return job, true, nil
}

func (r *SQLRepository) placeholder(index int) string {
	if r.dialect == database.DialectPostgres {
		return fmt.Sprintf("$%d", index)
	}
	return "?"
}

func (r *SQLRepository) placeholders(count int) string {
	placeholders := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		placeholders = append(placeholders, r.placeholder(i))
	}
	return strings.Join(placeholders, ", ")
}

func (r *SQLRepository) placeholdersFrom(start, count int) string {
	placeholders := make([]string, 0, count)
	for i := 0; i < count; i++ {
		placeholders = append(placeholders, r.placeholder(start+i))
	}
	return strings.Join(placeholders, ", ")
}

func (r *SQLRepository) isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return true
	}
	return false
}
