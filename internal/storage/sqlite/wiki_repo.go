// Package sqlite — WikiPage repository.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// wikiRepo persists wiki_pages and wiki_links.
type wikiRepo struct {
	db *sql.DB
}

// NewWikiRepository returns the Phase 5 wiki repo.
func NewWikiRepository(db *sql.DB) wiki.Repository {
	return &wikiRepo{db: db}
}

func (r *wikiRepo) Create(ctx context.Context, p *wiki.Page) (*wiki.Page, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.ID == "" {
		p.ID = newUUID()
	}
	const q = `
		INSERT INTO wiki_pages (id, parent_id, slug, title, content_md, position, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q,
		p.ID, nullString(p.ParentID), p.Slug, p.Title, p.ContentMD, p.Position,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, wiki.ErrSlugTaken
		}
		return nil, fmt.Errorf("wiki.Create: %w", err)
	}
	return r.GetByID(ctx, p.ID)
}

func (r *wikiRepo) GetByID(ctx context.Context, id string) (*wiki.Page, error) {
	const q = wikiSelectColumns + " WHERE id = ?"
	return scanWikiPage(r.db.QueryRowContext(ctx, q, id))
}

func (r *wikiRepo) GetBySlug(ctx context.Context, slug string) (*wiki.Page, error) {
	const q = wikiSelectColumns + " WHERE slug = ?"
	return scanWikiPage(r.db.QueryRowContext(ctx, q, slug))
}

func (r *wikiRepo) List(ctx context.Context) ([]*wiki.Page, error) {
	const q = wikiSelectColumns + " ORDER BY parent_id, position"
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("wiki.List: %w", err)
	}
	defer rows.Close()
	out := make([]*wiki.Page, 0)
	for rows.Next() {
		p, err := scanWikiPageRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *wikiRepo) Update(ctx context.Context, p *wiki.Page) error {
	if err := p.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE wiki_pages
		SET parent_id = ?, slug = ?, title = ?, content_md = ?, position = ?,
		    updated_at = datetime('now')
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q,
		nullString(p.ParentID), p.Slug, p.Title, p.ContentMD, p.Position, p.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return wiki.ErrSlugTaken
		}
		return fmt.Errorf("wiki.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return wiki.ErrNotFound
	}
	return nil
}

func (r *wikiRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM wiki_pages WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("wiki.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return wiki.ErrNotFound
	}
	return nil
}

func (r *wikiRepo) SetLinks(ctx context.Context, fromPageID string, toPageIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("wiki.SetLinks: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM wiki_links WHERE from_page_id = ?`, fromPageID); err != nil {
		return fmt.Errorf("wiki.SetLinks: clear: %w", err)
	}
	for _, toID := range toPageIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO wiki_links (from_page_id, to_page_id) VALUES (?, ?)`,
			fromPageID, toID,
		); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return fmt.Errorf("wiki.SetLinks: insert: %w", err)
		}
	}
	return tx.Commit()
}

func (r *wikiRepo) Backlinks(ctx context.Context, pageID string) ([]*wiki.Page, error) {
	const q = `
		SELECT p.id, p.parent_id, p.slug, p.title, p.content_md, p.position,
		       p.created_at, p.updated_at
		FROM wiki_links l
		JOIN wiki_pages p ON p.id = l.from_page_id
		WHERE l.to_page_id = ?
		ORDER BY p.updated_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q, pageID)
	if err != nil {
		return nil, fmt.Errorf("wiki.Backlinks: %w", err)
	}
	defer rows.Close()
	out := make([]*wiki.Page, 0)
	for rows.Next() {
		p, err := scanWikiPageRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const wikiSelectColumns = `
SELECT id, parent_id, slug, title, content_md, position, created_at, updated_at
FROM wiki_pages
`

func scanWikiPage(row *sql.Row) (*wiki.Page, error) {
	var (
		p      wiki.Page
		parent sql.NullString
		cAt    string
		uAt    string
	)
	err := row.Scan(&p.ID, &parent, &p.Slug, &p.Title, &p.ContentMD, &p.Position, &cAt, &uAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, wiki.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("wiki.Scan: %w", err)
	}
	p.ParentID = parent.String
	p.CreatedAt = parseTime(cAt)
	p.UpdatedAt = parseTime(uAt)
	return &p, nil
}

func scanWikiPageRows(rows *sql.Rows) (*wiki.Page, error) {
	var (
		p      wiki.Page
		parent sql.NullString
		cAt    string
		uAt    string
	)
	if err := rows.Scan(&p.ID, &parent, &p.Slug, &p.Title, &p.ContentMD, &p.Position, &cAt, &uAt); err != nil {
		return nil, fmt.Errorf("wiki.ScanRows: %w", err)
	}
	p.ParentID = parent.String
	p.CreatedAt = parseTime(cAt)
	p.UpdatedAt = parseTime(uAt)
	return &p, nil
}
