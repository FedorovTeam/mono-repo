package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"backend-document-service/internal/model"
)

type Repository struct {
	db *sql.DB
}

func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	doc.ID = uuid.NewString()
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now

	_, err = tx.ExecContext(ctx, `
        INSERT INTO documents (id, name, status, original_bucket, original_object_key, markdown_bucket, markdown_object_key, created_at, updated_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
    `, doc.ID, doc.Name, string(doc.Status), doc.OriginalBucket, doc.OriginalObjectKey, doc.MarkdownBucket, doc.MarkdownObjectKey, doc.CreatedAt, doc.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if version != nil {
		version.ID = uuid.NewString()
		version.DocumentID = doc.ID
		version.CreatedAt = now
		version.UpdatedAt = now
		if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_versions (id, document_id, version, original_object_key, markdown_object_key, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7)
        `, version.ID, version.DocumentID, version.Version, version.OriginalObjectKey, version.MarkdownObjectKey, version.CreatedAt, version.UpdatedAt); err != nil {
			return nil, err
		}
		doc.Versions = []model.DocumentVersion{*version}
	}

	if log != nil {
		log.ID = uuid.NewString()
		log.DocumentID = doc.ID
		log.CreatedAt = now
		if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_status_logs (id, document_id, from_status, to_status, reason, created_at)
            VALUES ($1,$2,$3,$4,$5,$6)
        `, log.ID, log.DocumentID, string(log.FromStatus), string(log.ToStatus), log.Reason, log.CreatedAt); err != nil {
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return doc, nil
}

func (r *Repository) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	row := r.db.QueryRowContext(ctx, `
        SELECT id, name, status, original_bucket, original_object_key, markdown_bucket, markdown_object_key, created_at, updated_at
        FROM documents WHERE id=$1
    `, id)

	var doc model.Document
	if err := row.Scan(&doc.ID, &doc.Name, &doc.Status, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
		return nil, err
	}

	versions, err := r.fetchVersions(ctx, id)
	if err != nil {
		return nil, err
	}
	doc.Versions = versions
	return &doc, nil
}

func (r *Repository) ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error) {
	base := `SELECT id, name, status, original_bucket, original_object_key, markdown_bucket, markdown_object_key, created_at, updated_at FROM documents`
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

	var result []model.Document
	for rows.Next() {
		var doc model.Document
		if err := rows.Scan(&doc.ID, &doc.Name, &doc.Status, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, doc)
	}
	return result, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id string, to model.Status, reason string, version *model.DocumentVersion) (*model.Document, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var doc model.Document
	row := tx.QueryRowContext(ctx, `
        SELECT id, name, status, original_bucket, original_object_key, markdown_bucket, markdown_object_key, created_at, updated_at
        FROM documents WHERE id=$1 FOR UPDATE
    `, id)
	if err := row.Scan(&doc.ID, &doc.Name, &doc.Status, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
		return nil, err
	}

	if err := model.ValidateStatus(to); err != nil {
		return nil, err
	}
	if err := model.ValidateTransition(doc.Status, to); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	updated := doc.Status != to

	if version != nil {
		version.ID = uuid.NewString()
		version.DocumentID = id
		version.CreatedAt = now
		version.UpdatedAt = now
		if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_versions (id, document_id, version, original_object_key, markdown_object_key, created_at, updated_at)
            VALUES ($1,$2,$3,$4,$5,$6,$7)
        `, version.ID, version.DocumentID, version.Version, version.OriginalObjectKey, version.MarkdownObjectKey, version.CreatedAt, version.UpdatedAt); err != nil {
			return nil, err
		}
		doc.MarkdownObjectKey = version.MarkdownObjectKey
		updated = true
	}

	if updated {
		if _, err = tx.ExecContext(ctx, `
            UPDATE documents SET status=$1, markdown_object_key=$2, updated_at=$3 WHERE id=$4
        `, string(to), doc.MarkdownObjectKey, now, id); err != nil {
			return nil, err
		}
		log := model.DocumentStatusLog{
			ID:         uuid.NewString(),
			DocumentID: id,
			FromStatus: doc.Status,
			ToStatus:   to,
			Reason:     reason,
			CreatedAt:  now,
		}
		if _, err = tx.ExecContext(ctx, `
            INSERT INTO document_status_logs (id, document_id, from_status, to_status, reason, created_at)
            VALUES ($1,$2,$3,$4,$5,$6)
        `, log.ID, log.DocumentID, string(log.FromStatus), string(log.ToStatus), log.Reason, log.CreatedAt); err != nil {
			return nil, err
		}
		doc.Status = to
		doc.UpdatedAt = now
	}

	versions, err := r.fetchVersionsTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	doc.Versions = versions

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (r *Repository) fetchVersions(ctx context.Context, documentID string) ([]model.DocumentVersion, error) {
	rows, err := r.db.QueryContext(ctx, `
        SELECT id, document_id, version, original_object_key, markdown_object_key, created_at, updated_at
        FROM document_versions WHERE document_id=$1 ORDER BY version
    `, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []model.DocumentVersion
	for rows.Next() {
		var v model.DocumentVersion
		if err := rows.Scan(&v.ID, &v.DocumentID, &v.Version, &v.OriginalObjectKey, &v.MarkdownObjectKey, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

func (r *Repository) fetchVersionsTx(ctx context.Context, tx *sql.Tx, documentID string) ([]model.DocumentVersion, error) {
	rows, err := tx.QueryContext(ctx, `
        SELECT id, document_id, version, original_object_key, markdown_object_key, created_at, updated_at
        FROM document_versions WHERE document_id=$1 ORDER BY version
    `, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []model.DocumentVersion
	for rows.Next() {
		var v model.DocumentVersion
		if err := rows.Scan(&v.ID, &v.DocumentID, &v.Version, &v.OriginalObjectKey, &v.MarkdownObjectKey, &v.CreatedAt, &v.UpdatedAt); err != nil {
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
