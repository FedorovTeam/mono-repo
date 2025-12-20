package sqlrepo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

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

func (r *SQLRepository) Create(ctx context.Context, job domain.TableExtractionJob) (domain.TableExtractionJob, error) {
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}

	query := fmt.Sprintf(`INSERT INTO table_extraction_jobs (
		id, document_id, status, status_reason,
		raw_json_bucket, raw_json_key,
		normalized_json_bucket, normalized_json_key,
		markdown_bucket, markdown_key,
		manifest_bucket, manifest_key,
		debug_bucket, debug_key,
		correlation_id, created_at, updated_at
	) VALUES (%s)`,
		r.placeholders(17),
	)

	args := []any{
		job.ID.String(),
		job.DocumentID,
		string(job.Status),
		job.StatusReason,
		job.Artifacts.RawJSON.Bucket,
		job.Artifacts.RawJSON.ObjectKey,
		job.Artifacts.NormalizedJSON.Bucket,
		job.Artifacts.NormalizedJSON.ObjectKey,
		job.Artifacts.Markdown.Bucket,
		job.Artifacts.Markdown.ObjectKey,
		job.Artifacts.Manifest.Bucket,
		job.Artifacts.Manifest.ObjectKey,
		job.Artifacts.DebugPrefix.Bucket,
		job.Artifacts.DebugPrefix.ObjectKey,
		job.CorrelationID,
		job.CreatedAt,
		job.UpdatedAt,
	}

	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return domain.TableExtractionJob{}, fmt.Errorf("insert job: %w", err)
	}

	return job, nil
}

func (r *SQLRepository) GetByDocumentID(ctx context.Context, documentID string) (domain.TableExtractionJob, bool, error) {
	query := fmt.Sprintf(`SELECT 
		id, document_id, status, status_reason,
		raw_json_bucket, raw_json_key,
		normalized_json_bucket, normalized_json_key,
		markdown_bucket, markdown_key,
		manifest_bucket, manifest_key,
		debug_bucket, debug_key,
		correlation_id, created_at, updated_at
		FROM table_extraction_jobs WHERE document_id = %s LIMIT 1`, r.placeholder(1))

	row := r.db.QueryRowContext(ctx, query, documentID)
	return r.scanJob(row)
}

func (r *SQLRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, bool, error) {
	query := fmt.Sprintf(`SELECT 
		id, document_id, status, status_reason,
		raw_json_bucket, raw_json_key,
		normalized_json_bucket, normalized_json_key,
		markdown_bucket, markdown_key,
		manifest_bucket, manifest_key,
		debug_bucket, debug_key,
		correlation_id, created_at, updated_at
		FROM table_extraction_jobs WHERE id = %s LIMIT 1`, r.placeholder(1))

	row := r.db.QueryRowContext(ctx, query, id.String())
	return r.scanJob(row)
}

func (r *SQLRepository) CountActive(ctx context.Context) (int, error) {
	query := fmt.Sprintf(
		`SELECT COUNT(*) FROM table_extraction_jobs WHERE status IN (%s, %s)`,
		r.placeholder(1),
		r.placeholder(2),
	)

	var count int
	if err := r.db.QueryRowContext(ctx, query, string(domain.StatusPending), string(domain.StatusProcessing)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active jobs: %w", err)
	}

	return count, nil
}

func (r *SQLRepository) scanJob(row interface{ Scan(dest ...any) error }) (domain.TableExtractionJob, bool, error) {
	var (
		id                          string
		documentID                  string
		status                      string
		statusReason                string
		rawBucket, rawKey           string
		normalizedBucket, normKey   string
		markdownBucket, markdownKey string
		manifestBucket, manifestKey string
		debugBucket, debugKey       string
		correlationID               string
		createdAt                   time.Time
		updatedAt                   time.Time
	)

	if err := row.Scan(
		&id,
		&documentID,
		&status,
		&statusReason,
		&rawBucket, &rawKey,
		&normalizedBucket, &normKey,
		&markdownBucket, &markdownKey,
		&manifestBucket, &manifestKey,
		&debugBucket, &debugKey,
		&correlationID,
		&createdAt,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return domain.TableExtractionJob{}, false, nil
		}
		return domain.TableExtractionJob{}, false, fmt.Errorf("scan job: %w", err)
	}

	jobID, err := uuid.Parse(id)
	if err != nil {
		return domain.TableExtractionJob{}, false, fmt.Errorf("parse job id: %w", err)
	}

	job := domain.TableExtractionJob{
		ID:           jobID,
		DocumentID:   documentID,
		Status:       domain.TableExtractionStatus(status),
		StatusReason: statusReason,
		Artifacts: domain.TableArtifacts{
			RawJSON: domain.ArtifactLocation{
				Bucket:    rawBucket,
				ObjectKey: rawKey,
			},
			NormalizedJSON: domain.ArtifactLocation{
				Bucket:    normalizedBucket,
				ObjectKey: normKey,
			},
			Markdown: domain.ArtifactLocation{
				Bucket:    markdownBucket,
				ObjectKey: markdownKey,
			},
			Manifest: domain.ArtifactLocation{
				Bucket:    manifestBucket,
				ObjectKey: manifestKey,
			},
			DebugPrefix: domain.ArtifactLocation{
				Bucket:    debugBucket,
				ObjectKey: debugKey,
			},
		},
		CorrelationID: correlationID,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
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
