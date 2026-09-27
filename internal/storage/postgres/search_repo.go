// Package postgres — full-text search repository (T365).
//
// The postgres counterpart of sqlite's FTS5 search_repo: it answers the
// same search.Repository surface against the generated tsvector columns
// and GIN indexes created by migration 002_search.
//
// Parity decisions (sqlite search_repo.go is the reference):
//
//   - Phrase semantics. The sqlite repo wraps the raw query in double
//     quotes, making FTS5 treat it as a phrase — tokens in order,
//     adjacent, reserved characters ("+", "-", ":") neutralized.
//     phraseto_tsquery is the exact postgres equivalent: it tokenizes
//     the input with the parser, ignores query operators entirely and
//     demands the lexemes back-to-back in order. plainto_tsquery would
//     AND unordered tokens (weaker), websearch_to_tsquery would honor
//     operators the sqlite branch treats as literal text (different) —
//     both rejected for parity.
//   - Snippets. FTS5 snippet(fts, 1, '<mark>', '</mark>', '…', 30)
//     extracts a ≤30-token window from the SECOND indexed column; the
//     equivalent here is ts_headline over that same source column
//     (content_md for pages, description for tasks, body_md for
//     comments) with StartSel/StopSel/Ellipses carried over verbatim and
//     MaxWords=30. A hit whose match lives outside the headline column
//     yields an unmarked snippet on both dialects.
//   - Ranking. -bm25 becomes ts_rank: numbers are not comparable across
//     engines (bm25 is IDF-weighted, ts_rank is plain term frequency),
//     but higher == more relevant on both, and ORDER BY score DESC LIMIT
//     keeps the shape.
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/service/search"
)

// searchRepo implements search.Repository over the tsvector columns
// from migration 002_search.
type searchRepo struct {
	db *sql.DB
}

// NewSearchRepository returns the postgres full-text search repo. The
// handle is the shim-wrapped pool the seam opens for the postgres
// dialect, so the queries use the same anonymous ? placeholders every
// other repository speaks and the shim renumbers them into $n.
func NewSearchRepository(db *sql.DB) search.Repository {
	return &searchRepo{db: db}
}

// headlineOpts mirrors the FTS5 snippet options of the sqlite repo:
// '<mark>'/'</mark>' markers, '…' as the truncation delimiter, ~30-token
// window. MaxFragments=0 keeps the single-excerpt shape of FTS5
// snippet(), ShortWord=0 stops postgres from dropping short words at the
// fragment edges, MinWords/MaxWords bound the excerpt window.
const headlineOpts = `StartSel=<mark>, StopSel=</mark>, MaxWords=30, MinWords=15, ShortWord=0, MaxFragments=0, FragmentDelimiter=…`

const searchPagesSQL = `
	SELECT id, slug, title, snippet, score::double precision
	FROM (
		SELECT p.id, p.slug, p.title,
		       ts_headline('simple', coalesce(p.content_md, ''), q, '` + headlineOpts + `') AS snippet,
		       ts_rank(p.search_vec, q) AS score
		FROM wiki_pages p
		CROSS JOIN phraseto_tsquery('simple', ?) AS q
		WHERE p.search_vec @@ q
	) hits
	ORDER BY score DESC
	LIMIT ?
`

const searchTasksSQL = `
	SELECT id, title, snippet, score::double precision
	FROM (
		SELECT t.id, t.title,
		       ts_headline('simple', coalesce(t.description, ''), q, '` + headlineOpts + `') AS snippet,
		       ts_rank(t.search_vec, q) AS score
		FROM tasks t
		CROSS JOIN phraseto_tsquery('simple', ?) AS q
		WHERE t.search_vec @@ q
	) hits
	ORDER BY score DESC
	LIMIT ?
`

const searchCommentsSQL = `
	SELECT id, body_md, snippet, score::double precision
	FROM (
		SELECT c.id, c.body_md,
		       ts_headline('simple', c.body_md, q, '` + headlineOpts + `') AS snippet,
		       ts_rank(c.search_vec, q) AS score
		FROM comments c
		CROSS JOIN phraseto_tsquery('simple', ?) AS q
		WHERE c.search_vec @@ q
	) hits
	ORDER BY score DESC
	LIMIT ?
`

// SearchPages queries wiki_pages.search_vec. Page hits carry the wiki
// slug so the frontend can link to /wiki/<slug> — same contract as the
// sqlite repo (T103).
func (r *searchRepo) SearchPages(ctx context.Context, q string, limit int) ([]search.Hit, error) {
	const sqlQuery = searchPagesSQL
	rows, err := r.db.QueryContext(ctx, sqlQuery, q, limit)
	if err != nil {
		return nil, fmt.Errorf("search.page: %w", err)
	}
	defer rows.Close()

	out := make([]search.Hit, 0)
	for rows.Next() {
		var h search.Hit
		if err := rows.Scan(&h.ID, &h.Slug, &h.Title, &h.Snippet, &h.Score); err != nil {
			return nil, fmt.Errorf("search.page: scan: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// SearchTasks queries tasks.search_vec.
func (r *searchRepo) SearchTasks(ctx context.Context, q string, limit int) ([]search.Hit, error) {
	return r.runQuery(ctx, searchTasksSQL, q, limit, search.TypeTask)
}

// SearchComments queries comments.search_vec.
func (r *searchRepo) SearchComments(ctx context.Context, q string, limit int) ([]search.Hit, error) {
	return r.runQuery(ctx, searchCommentsSQL, q, limit, search.TypeComment)
}

// runQuery executes the shared query shape and returns hits.
func (r *searchRepo) runQuery(ctx context.Context, sqlQuery, q string, limit int, t search.Type) ([]search.Hit, error) {
	rows, err := r.db.QueryContext(ctx, sqlQuery, q, limit)
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
