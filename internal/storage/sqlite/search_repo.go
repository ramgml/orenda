// Package sqlite — FTS5 search repository.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/service/search"
)

// searchRepo implements search.Repository using the FTS5 tables created
// in migration 008.
type searchRepo struct {
	db *sql.DB
}

// NewSearchRepository returns a Phase 5 search repo.
func NewSearchRepository(db *sql.DB) search.Repository {
	return &searchRepo{db: db}
}

// SearchPages queries pages_fts.
func (r *searchRepo) SearchPages(ctx context.Context, q string, limit int) ([]search.Hit, error) {
	const sqlQuery = `
		SELECT p.id, p.title, snippet(pages_fts, 1, '<mark>', '</mark>', '…', 30) AS snippet,
		       -bm25(pages_fts) AS score
		FROM pages_fts
		JOIN wiki_pages p ON p.rowid = pages_fts.rowid
		WHERE pages_fts MATCH ?
		ORDER BY score DESC
		LIMIT ?
	`
	return r.runQuery(ctx, sqlQuery, q, limit, search.TypePage)
}

// SearchTasks queries tasks_fts.
func (r *searchRepo) SearchTasks(ctx context.Context, q string, limit int) ([]search.Hit, error) {
	const sqlQuery = `
		SELECT t.id, t.title, snippet(tasks_fts, 1, '<mark>', '</mark>', '…', 30) AS snippet,
		       -bm25(tasks_fts) AS score
		FROM tasks_fts
		JOIN tasks t ON t.rowid = tasks_fts.rowid
		WHERE tasks_fts MATCH ?
		ORDER BY score DESC
		LIMIT ?
	`
	return r.runQuery(ctx, sqlQuery, q, limit, search.TypeTask)
}

// SearchComments queries comments_fts.
func (r *searchRepo) SearchComments(ctx context.Context, q string, limit int) ([]search.Hit, error) {
	const sqlQuery = `
		SELECT c.id, c.body_md, snippet(comments_fts, 0, '<mark>', '</mark>', '…', 30) AS snippet,
		       -bm25(comments_fts) AS score
		FROM comments_fts
		JOIN comments c ON c.rowid = comments_fts.rowid
		WHERE comments_fts MATCH ?
		ORDER BY score DESC
		LIMIT ?
	`
	return r.runQuery(ctx, sqlQuery, q, limit, search.TypeComment)
}

// runQuery executes the shared FTS5 query shape and returns hits.
func (r *searchRepo) runQuery(ctx context.Context, sqlQuery, q string, limit int, t search.Type) ([]search.Hit, error) {
	// Wrap user input in double quotes so FTS treats it as a phrase. This
	// handles reserved characters (e.g. "+", "-", ":") safely.
	safeQuery := `"` + q + `"`
	rows, err := r.db.QueryContext(ctx, sqlQuery, safeQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("search.%s: %w", t, err)
	}
	defer rows.Close()

	out := make([]search.Hit, 0)
	for rows.Next() {
		var h search.Hit
		if err := rows.Scan(&h.ID, &h.Title, &h.Snippet, &h.Score); err != nil {
			return nil, fmt.Errorf("search.%s: scan: %w", t, err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
