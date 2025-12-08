package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func (r *Repository) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	row := r.db.QueryRowContext(ctx, `
        SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at
        FROM documents WHERE id=$1
    `, id)

	var doc model.Document
	if err := row.Scan(&doc.ID, &doc.Name, &doc.Owner, &doc.Repository, &doc.Branch, &doc.BaseBranch, &doc.Path, &doc.Status, &doc.WorkflowID, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.ErrorReason, &doc.CreatePR, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
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

	var result []model.Document
	for rows.Next() {
		var doc model.Document
		if err := rows.Scan(&doc.ID, &doc.Name, &doc.Owner, &doc.Repository, &doc.Branch, &doc.BaseBranch, &doc.Path, &doc.Status, &doc.WorkflowID, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.ErrorReason, &doc.CreatePR, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, doc)
	}
	return result, nil
}

func (r *Repository) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var doc model.Document
	row := tx.QueryRowContext(ctx, `
        SELECT id, name, owner, repository, branch, base_branch, path, status, workflow_id, original_bucket, original_object_key, markdown_bucket, markdown_object_key, error_reason, create_pr, created_at, updated_at
        FROM documents WHERE id=$1 FOR UPDATE
    `, id)
	if err := row.Scan(&doc.ID, &doc.Name, &doc.Owner, &doc.Repository, &doc.Branch, &doc.BaseBranch, &doc.Path, &doc.Status, &doc.WorkflowID, &doc.OriginalBucket, &doc.OriginalObjectKey, &doc.MarkdownBucket, &doc.MarkdownObjectKey, &doc.ErrorReason, &doc.CreatePR, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
		return nil, err
	}

	if err := model.ValidateStatus(to); err != nil {
		return nil, err
	}
	if err := model.ValidateTransition(doc.Status, to); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	statusChanged := doc.Status != to
	docUpdated := statusChanged
	versionInserted := false

	if version != nil {
		existing, err := r.getVersionTx(ctx, tx, id, version.Version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if existing != nil {
			if (version.MarkdownObjectKey != "" && existing.MarkdownObjectKey != version.MarkdownObjectKey) ||
				(version.OriginalObjectKey != "" && existing.OriginalObjectKey != version.OriginalObjectKey) {
				return nil, fmt.Errorf("version %d already stored with different object keys", version.Version)
			}
			version = existing
		} else {
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
			log := model.DocumentStatusLog{
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
	return &doc, nil
}

// UpdateWorkflowID сохраняет идентификатор запущенного workflow и возвращает актуальное состояние документа.
func (r *Repository) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var doc model.Document
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

func (r *Repository) getVersionTx(ctx context.Context, tx *sql.Tx, documentID string, version int32) (*model.DocumentVersion, error) {
	row := tx.QueryRowContext(ctx, `
        SELECT id, document_id, version, original_object_key, markdown_object_key, created_at, updated_at
        FROM document_versions WHERE document_id=$1 AND version=$2
    `, documentID, version)
	var v model.DocumentVersion
	if err := row.Scan(&v.ID, &v.DocumentID, &v.Version, &v.OriginalObjectKey, &v.MarkdownObjectKey, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	return &v, nil
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
