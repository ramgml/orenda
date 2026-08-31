// Package sqlite — Comment repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/ramgml/orenda/internal/domain/comment"
)

// commentRepo persists comments and extracted mentions.
type commentRepo struct {
	db *sql.DB
}

// NewCommentRepository returns the Phase 3 comment repo.
func NewCommentRepository(db *sql.DB) comment.Repository {
	return &commentRepo{db: db}
}

var mentionRE = regexp.MustCompile(`@(user|agent):([A-Za-z0-9_-]+)`)

func (r *commentRepo) Create(ctx context.Context, c *comment.Comment) (*comment.Comment, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.ID == "" {
		c.ID = newUUID()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("comment.Create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const q = `
		INSERT INTO comments (id, target_type, target_id, author_type, author_id, body_md, created_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
	`
	if _, err := tx.ExecContext(ctx, q,
		c.ID, string(c.TargetType), c.TargetID, string(c.AuthorType), c.AuthorID, c.BodyMD,
	); err != nil {
		return nil, fmt.Errorf("comment.Create: insert: %w", err)
	}

	// Extract mentions.
	for _, m := range extractMentions(c.BodyMD) {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mentions (comment_id, target_type, target_id) VALUES (?, ?, ?)`,
			c.ID, m.TargetType, m.TargetID,
		); err != nil {
			return nil, fmt.Errorf("comment.Create: mention: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("comment.Create: commit: %w", err)
	}
	return r.GetByID(ctx, c.ID)
}

// extractMentions finds @user:<id> and @agent:<id> tokens in body.
//
// The regex is intentionally simple — markdown body content may also
// contain other @ signs (emails, etc.), but only the typed prefix counts.
func extractMentions(body string) []comment.Mention {
	matches := mentionRE.FindAllStringSubmatch(body, -1)
	out := make([]comment.Mention, 0)
	for _, m := range matches {
		// Persisted as a free-form "user" or "agent" string in the mentions table.
		out = append(out, comment.Mention{
			TargetType: comment.TargetType(m[1]),
			TargetID:   m[2],
		})
	}
	return out
}

func (r *commentRepo) GetByID(ctx context.Context, id string) (*comment.Comment, error) {
	const q = commentSelectColumns + " WHERE id = ?"
	row := r.db.QueryRowContext(ctx, q, id)
	return scanComment(row)
}

func (r *commentRepo) ListByTarget(ctx context.Context, targetType comment.TargetType, targetID string) ([]*comment.Comment, error) {
	const q = commentSelectColumns + " WHERE target_type = ? AND target_id = ? ORDER BY created_at ASC"
	rows, err := r.db.QueryContext(ctx, q, string(targetType), targetID)
	if err != nil {
		return nil, fmt.Errorf("comment.ListByTarget: %w", err)
	}
	defer rows.Close()

	out := make([]*comment.Comment, 0)
	for rows.Next() {
		c, err := scanCommentRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *commentRepo) MentionsForComment(ctx context.Context, commentID string) ([]*comment.Mention, error) {
	// Order by rowid so the slice matches the body order — important
	// for callers that want to render mentions in the order they appear.
	const q = `SELECT comment_id, target_type, target_id FROM mentions WHERE comment_id = ? ORDER BY rowid ASC`
	rows, err := r.db.QueryContext(ctx, q, commentID)
	if err != nil {
		return nil, fmt.Errorf("comment.MentionsForComment: %w", err)
	}
	defer rows.Close()
	out := make([]*comment.Mention, 0)
	for rows.Next() {
		var m comment.Mention
		var t string
		if err := rows.Scan(&m.CommentID, &t, &m.TargetID); err != nil {
			return nil, err
		}
		m.TargetType = comment.TargetType(t)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// Update overwrites the comment body and stamps edited_at.
//
// Task 112: comments were immutable before this; edited_at is set to
// datetime('now') on every successful update so the UI can badge
// edited comments. RowsAffected==0 maps to comment.ErrNotFound.
func (r *commentRepo) Update(ctx context.Context, id string, bodyMd string) (*comment.Comment, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE comments SET body_md = ?, edited_at = datetime('now') WHERE id = ?`,
		bodyMd, id)
	if err != nil {
		return nil, fmt.Errorf("comment.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("comment.Update: %w", err)
	}
	if n == 0 {
		return nil, comment.ErrNotFound
	}
	return r.GetByID(ctx, id)
}

const commentSelectColumns = `
SELECT id, target_type, target_id, author_type, author_id, body_md, created_at, edited_at
FROM comments
`

func scanComment(row *sql.Row) (*comment.Comment, error) {
	var (
		c     comment.Comment
		tType string
		aType string
		cAt   string
		eAt   sql.NullString
	)
	err := row.Scan(&c.ID, &tType, &c.TargetID, &aType, &c.AuthorID, &c.BodyMD, &cAt, &eAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, comment.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("comment.Scan: %w", err)
	}
	c.TargetType = comment.TargetType(tType)
	c.AuthorType = comment.AuthorType(aType)
	c.CreatedAt = parseTime(cAt)
	if eAt.Valid {
		t := parseTime(eAt.String)
		c.EditedAt = &t
	}
	return &c, nil
}

func scanCommentRows(rows *sql.Rows) (*comment.Comment, error) {
	var (
		c     comment.Comment
		tType string
		aType string
		cAt   string
		eAt   sql.NullString
	)
	if err := rows.Scan(&c.ID, &tType, &c.TargetID, &aType, &c.AuthorID, &c.BodyMD, &cAt, &eAt); err != nil {
		return nil, fmt.Errorf("comment.ScanRows: %w", err)
	}
	c.TargetType = comment.TargetType(tType)
	c.AuthorType = comment.AuthorType(aType)
	c.CreatedAt = parseTime(cAt)
	if eAt.Valid {
		t := parseTime(eAt.String)
		c.EditedAt = &t
	}
	return &c, nil
}
