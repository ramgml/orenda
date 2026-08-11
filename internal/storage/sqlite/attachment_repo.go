// Package sqlite — Attachment repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/attachment"
)

// attachmentRepo persists attachments.
type attachmentRepo struct {
	db *sql.DB
}

// NewAttachmentRepository returns the Phase 3 attachment repo.
func NewAttachmentRepository(db *sql.DB) attachment.Repository {
	return &attachmentRepo{db: db}
}

func (r *attachmentRepo) Create(ctx context.Context, a *attachment.Attachment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.ID == "" {
		a.ID = newUUID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}

	const q = `
		INSERT INTO attachments (id, target_type, target_id, filename, mime, size, path, sha256,
		                        uploaded_by_type, uploaded_by_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.ExecContext(ctx, q,
		a.ID, string(a.TargetType), a.TargetID, a.Filename, a.Mime, a.Size, a.Path, a.SHA256,
		string(a.UploadedByType), a.UploadedByID, a.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("attachment.Create: %w", err)
	}
	return nil
}

func (r *attachmentRepo) GetByID(ctx context.Context, id string) (*attachment.Attachment, error) {
	const q = attachmentSelectColumns + " WHERE id = ?"
	return scanAttachment(r.db.QueryRowContext(ctx, q, id))
}

func (r *attachmentRepo) ListByTarget(ctx context.Context, targetType attachment.TargetType, targetID string) ([]*attachment.Attachment, error) {
	const q = attachmentSelectColumns + " WHERE target_type = ? AND target_id = ? ORDER BY created_at DESC"
	rows, err := r.db.QueryContext(ctx, q, string(targetType), targetID)
	if err != nil {
		return nil, fmt.Errorf("attachment.ListByTarget: %w", err)
	}
	defer rows.Close()
	out := make([]*attachment.Attachment, 0)
	for rows.Next() {
		a, err := scanAttachmentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListByProject returns every attachment that belongs to a project —
// both those attached directly to the project and those attached to
// any of its tasks — newest first. Task attachments are annotated
// with the task's title so the project attachments tab can show a
// useful label.
func (r *attachmentRepo) ListByProject(ctx context.Context, projectID string) ([]*attachment.ProjectAttachment, error) {
	const q = `
		SELECT a.id, a.target_type, a.target_id, a.filename, a.mime, a.size, a.path, a.sha256,
		       a.uploaded_by_type, a.uploaded_by_id, a.created_at,
		       COALESCE(t.title, '')
		FROM attachments a
		LEFT JOIN tasks t
		  ON a.target_type = 'task' AND a.target_id = t.id
		WHERE (a.target_type = 'project' AND a.target_id = ?)
		   OR (a.target_type = 'task' AND t.project_id = ?)
		ORDER BY a.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q, projectID, projectID)
	if err != nil {
		return nil, fmt.Errorf("attachment.ListByProject: %w", err)
	}
	defer rows.Close()
	out := make([]*attachment.ProjectAttachment, 0)
	for rows.Next() {
		var (
			pa    attachment.ProjectAttachment
			tType string
			uType string
			cAt   string
		)
		if err := rows.Scan(
			&pa.ID, &tType, &pa.TargetID, &pa.Filename, &pa.Mime, &pa.Size, &pa.Path, &pa.SHA256,
			&uType, &pa.UploadedByID, &cAt, &pa.TaskTitle,
		); err != nil {
			return nil, fmt.Errorf("attachment.ListByProject: scan: %w", err)
		}
		pa.TargetType = attachment.TargetType(tType)
		pa.UploadedByType = attachment.UploaderType(uType)
		pa.CreatedAt = parseTime(cAt)
		out = append(out, &pa)
	}
	return out, rows.Err()
}

func (r *attachmentRepo) FindBySHA256(ctx context.Context, sha string) (*attachment.Attachment, error) {
	const q = attachmentSelectColumns + " WHERE sha256 = ? ORDER BY created_at DESC LIMIT 1"
	return scanAttachment(r.db.QueryRowContext(ctx, q, sha))
}

func (r *attachmentRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("attachment.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return attachment.ErrNotFound
	}
	return nil
}

const attachmentSelectColumns = `
SELECT id, target_type, target_id, filename, mime, size, path, sha256,
       uploaded_by_type, uploaded_by_id, created_at
FROM attachments
`

func scanAttachment(row *sql.Row) (*attachment.Attachment, error) {
	var (
		a     attachment.Attachment
		tType string
		uType string
		cAt   string
	)
	err := row.Scan(&a.ID, &tType, &a.TargetID, &a.Filename, &a.Mime, &a.Size, &a.Path, &a.SHA256,
		&uType, &a.UploadedByID, &cAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, attachment.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("attachment.Scan: %w", err)
	}
	a.TargetType = attachment.TargetType(tType)
	a.UploadedByType = attachment.UploaderType(uType)
	a.CreatedAt = parseTime(cAt)
	return &a, nil
}

func scanAttachmentRows(rows *sql.Rows) (*attachment.Attachment, error) {
	var (
		a     attachment.Attachment
		tType string
		uType string
		cAt   string
	)
	if err := rows.Scan(&a.ID, &tType, &a.TargetID, &a.Filename, &a.Mime, &a.Size, &a.Path, &a.SHA256,
		&uType, &a.UploadedByID, &cAt); err != nil {
		return nil, fmt.Errorf("attachment.ScanRows: %w", err)
	}
	a.TargetType = attachment.TargetType(tType)
	a.UploadedByType = attachment.UploaderType(uType)
	a.CreatedAt = parseTime(cAt)
	return &a, nil
}
