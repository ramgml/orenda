// Package sqlite — Attachment repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

	const q = `
		INSERT INTO attachments (id, target_type, target_id, filename, mime, size, path, sha256,
		                        uploaded_by_type, uploaded_by_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q,
		a.ID, string(a.TargetType), a.TargetID, a.Filename, a.Mime, a.Size, a.Path, a.SHA256,
		string(a.UploadedByType), a.UploadedByID,
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
