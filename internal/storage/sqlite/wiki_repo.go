// Package sqlite — WikiPage repository.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
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

	// number comes from the wiki_page_number_seq high-watermark, not from
	// MAX(wiki_pages.number): a MAX+1 would re-issue the newest page's
	// number after that page is deleted, and a "W42" reference in a
	// commit message, branch name or PR title must keep pointing at
	// the same page forever. The watermark UPDATE...RETURNING and the
	// INSERT share one transaction, so the draw is atomic and the
	// sequence can never run backwards.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("wiki.Create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var number int
	if err := tx.QueryRowContext(ctx,
		`UPDATE wiki_page_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&number); err != nil {
		return nil, fmt.Errorf("wiki.Create: draw number: %w", err)
	}

	// Include content_format only when set; omitting it lets the column
	// default ("markdown") apply and keeps the repo compatible with
	// schemas where migration 040 hasn't run yet.
	if p.ContentFormat != "" {
		const q = `
			INSERT INTO wiki_pages (id, parent_id, slug, title, content_md, content_format, position, number, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		`
		_, err = tx.ExecContext(ctx, q,
			p.ID, nullString(p.ParentID), p.Slug, p.Title, p.ContentMD, p.ContentFormat, p.Position, number,
		)
	} else {
		const q = `
			INSERT INTO wiki_pages (id, parent_id, slug, title, content_md, position, number, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		`
		_, err = tx.ExecContext(ctx, q,
			p.ID, nullString(p.ParentID), p.Slug, p.Title, p.ContentMD, p.Position, number,
		)
	}
	if err != nil {
		if isUniqueViolation(err) {
			return nil, wiki.ErrSlugTaken
		}
		return nil, fmt.Errorf("wiki.Create: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("wiki.Create: commit: %w", err)
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

// GetByNumber resolves the human-readable "W<N>" reference to a page.
// The UNIQUE index idx_wiki_pages_number (migration 037) makes this an
// index point lookup.
func (r *wikiRepo) GetByNumber(ctx context.Context, number int) (*wiki.Page, error) {
	const q = wikiSelectColumns + " WHERE number = ?"
	return scanWikiPage(r.db.QueryRowContext(ctx, q, number))
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
		SET parent_id = ?, slug = ?, title = ?, content_md = ?, content_format = ?, position = ?,
		    updated_at = datetime('now')
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q,
		nullString(p.ParentID), p.Slug, p.Title, p.ContentMD, p.ContentFormat, p.Position, p.ID,
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

// UpdateParent moves a page under a new parent (or the root when
// newParentID is empty). Used by the "Move to…" UI action.
func (r *wikiRepo) UpdateParent(ctx context.Context, id, newParentID string) error {
	if id == "" {
		return wiki.ErrInvalidInput
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE wiki_pages SET parent_id = ?, updated_at = datetime('now') WHERE id = ?`,
		nullString(newParentID), id,
	)
	if err != nil {
		return fmt.Errorf("wiki.UpdateParent: %w", err)
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

// DescendantIDs walks the tree under id and returns every descendant's
// id (not including id itself). Used to reject moves that would
// create a cycle.
func (r *wikiRepo) DescendantIDs(ctx context.Context, id string) ([]string, error) {
	out := make([]string, 0)
	stack := []string{id}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		rows, err := r.db.QueryContext(ctx,
			`SELECT id FROM wiki_pages WHERE parent_id = ?`, top)
		if err != nil {
			return nil, fmt.Errorf("wiki.DescendantIDs: %w", err)
		}
		for rows.Next() {
			var child string
			if err := rows.Scan(&child); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, child)
			stack = append(stack, child)
		}
		rows.Close()
	}
	return out, nil
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
		SELECT p.id, p.parent_id, p.slug, p.title, p.content_md, p.content_format, p.position,
		       p.number, p.created_at, p.updated_at
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

// GetBlocks returns all blocks for a page, ordered by parent_block_id, position.
func (r *wikiRepo) GetBlocks(ctx context.Context, pageID string) ([]*wiki.Block, error) {
	const q = `
		SELECT id, page_id, parent_block_id, position, type, data, created_at, updated_at
		FROM wiki_blocks
		WHERE page_id = ?
		ORDER BY parent_block_id, position
	`
	rows, err := r.db.QueryContext(ctx, q, pageID)
	if err != nil {
		return nil, fmt.Errorf("wiki.GetBlocks: %w", err)
	}
	defer rows.Close()

	var blocks []*wiki.Block
	for rows.Next() {
		var (
			b       wiki.Block
			parent  sql.NullString
			rawData []byte
			cAt     string
			uAt     string
		)
		if err := rows.Scan(&b.ID, &b.PageID, &parent, &b.Position, &b.Type, &rawData, &cAt, &uAt); err != nil {
			return nil, fmt.Errorf("wiki.GetBlocks scan: %w", err)
		}
		b.ParentBlockID = parent.String
		b.CreatedAt = parseTime(cAt)
		b.UpdatedAt = parseTime(uAt)

		// Unmarshal data into Props + Content.
		var envelope struct {
			Props   json.RawMessage `json:"props"`
			Content json.RawMessage `json:"content"`
		}
		if len(rawData) > 0 {
			if err := json.Unmarshal(rawData, &envelope); err != nil {
				return nil, fmt.Errorf("wiki.GetBlocks unmarshal data: %w", err)
			}
		}
		b.Props = envelope.Props
		b.Content = envelope.Content

		blocks = append(blocks, &b)
	}
	return blocks, rows.Err()
}

// ReplaceBlocks atomically deletes all blocks for a page and inserts
// the provided set. An empty slice deletes all blocks.
func (r *wikiRepo) ReplaceBlocks(ctx context.Context, pageID string, blocks []*wiki.Block) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("wiki.ReplaceBlocks: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM wiki_blocks WHERE page_id = ?`, pageID); err != nil {
		return fmt.Errorf("wiki.ReplaceBlocks: delete: %w", err)
	}

	for _, b := range blocks {
		// Marshal Props + Content back into data JSON.
		data, err := marshalBlockData(b)
		if err != nil {
			return fmt.Errorf("wiki.ReplaceBlocks marshal block %s: %w", b.ID, err)
		}
		const q = `
			INSERT INTO wiki_blocks (id, page_id, parent_block_id, position, type, data, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
		`
		if _, err := tx.ExecContext(ctx, q,
			b.ID, pageID, nullString(b.ParentBlockID), b.Position, b.Type, data,
		); err != nil {
			return fmt.Errorf("wiki.ReplaceBlocks insert %s: %w", b.ID, err)
		}
	}

	return tx.Commit()
}

const wikiSelectColumns = `
SELECT id, parent_id, slug, title, content_md, content_format, position, number, created_at, updated_at
FROM wiki_pages
`

func scanWikiPage(row *sql.Row) (*wiki.Page, error) {
	var (
		p      wiki.Page
		parent sql.NullString
		cAt    string
		uAt    string
	)
	err := row.Scan(&p.ID, &parent, &p.Slug, &p.Title, &p.ContentMD, &p.ContentFormat, &p.Position, &p.Number, &cAt, &uAt)
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
	if err := rows.Scan(&p.ID, &parent, &p.Slug, &p.Title, &p.ContentMD, &p.ContentFormat, &p.Position, &p.Number, &cAt, &uAt); err != nil {
		return nil, fmt.Errorf("wiki.ScanRows: %w", err)
	}
	p.ParentID = parent.String
	p.CreatedAt = parseTime(cAt)
	p.UpdatedAt = parseTime(uAt)
	return &p, nil
}

// marshalBlockData packs Props and Content into the {"props":...,"content":...}
// envelope stored in the data column.
func marshalBlockData(b *wiki.Block) ([]byte, error) {
	envelope := struct {
		Props   json.RawMessage `json:"props,omitempty"`
		Content json.RawMessage `json:"content,omitempty"`
	}{
		Props:   b.Props,
		Content: b.Content,
	}
	return json.Marshal(envelope)
}
