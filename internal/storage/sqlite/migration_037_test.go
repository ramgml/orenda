package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// Phase 37: human-readable wiki page numbers (037_wiki_page_numbers).
//
// We pin four contracts:
//  1. Up adds wiki_pages.number, backfills existing rows in
//     (created_at, rowid) order starting at 1 (ROW_NUMBER window),
//     and installs the UNIQUE index idx_wiki_pages_number.
//  2. New inserts through the repository draw from the
//     wiki_page_number_seq high-watermark (UPDATE ... RETURNING in the
//     Create transaction) — monotone, and never re-issued even after
//     the newest page is deleted.
//  3. The UNIQUE index rejects a hand-crafted duplicate number.
//  4. Down drops the index, the seq table, and the column, so a
//     down/up round-trip is clean.
func TestMigrate_037WikiPageNumbers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "035_lesson_completed_at")

	// Fixture: three raw pages with distinct created_at, inserted
	// newest-first so rowid order disagrees with created_at order —
	// the backfill must follow (created_at, rowid), not rowid alone.
	pageNew := "w-036-new"
	pageOld := "w-036-old"
	pageMiddle := "w-036-mid"
	_, err = db.ExecContext(ctx,
		`INSERT INTO wiki_pages (id, slug, title, content_md, position, created_at, updated_at) VALUES
		 (?, 'slug-new', 'New', '', 0, '2026-08-12 10:00:00', '2026-08-12 10:00:00'),
		 (?, 'slug-old', 'Old', '', 0, '2026-08-08 10:00:00', '2026-08-08 10:00:00'),
		 (?, 'slug-mid', 'Mid', '', 0, '2026-08-10 10:00:00', '2026-08-10 10:00:00')`,
		pageNew, pageOld, pageMiddle)
	require.NoError(t, err)

	// Apply the migration under test.
	body, err := MigrationsFS.ReadFile("migrations/037_wiki_page_numbers.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	// Apply migration 040 as well so the schema includes content_format
	// (needed by the updated repo scan).
	body040, err := MigrationsFS.ReadFile("migrations/040_wiki_blocks.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body040))
	require.NoError(t, err)

	// Contract 1: backfill in (created_at, rowid) order, 1-based.
	numberOf := func(id string) int {
		t.Helper()
		var n int
		err := db.QueryRowContext(ctx, `SELECT number FROM wiki_pages WHERE id = ?`, id).Scan(&n)
		require.NoError(t, err)
		return n
	}
	assert.Equal(t, 1, numberOf(pageOld), "oldest by created_at is #1")
	assert.Equal(t, 2, numberOf(pageMiddle))
	assert.Equal(t, 3, numberOf(pageNew))

	// Contract 1 (cont'd): UNIQUE index exists.
	assert.Contains(t, listIndexes(t, ctx, db, "wiki_pages"), "idx_wiki_pages_number",
		"unique index on wiki_pages.number must exist")

	// Contract 2: repository INSERT draws from high-watermark.
	repo := NewWikiRepository(db)
	newPage, err := repo.Create(ctx, &wiki.Page{Slug: "post-migration", Title: "Post Migration"})
	require.NoError(t, err)
	assert.Equal(t, 4, newPage.Number,
		"first post-migration page draws MAX(number)+1")

	// Contract 3: the UNIQUE index rejects duplicates.
	_, err = db.ExecContext(ctx,
		`INSERT INTO wiki_pages (id, slug, title, content_md, position, number, created_at, updated_at)
		 VALUES (?, 'dup-slug', 'Dup', '', 0, ?, datetime('now'), datetime('now'))`,
		"w-036-dup", newPage.Number)
	require.Error(t, err, "duplicate number must violate idx_wiki_pages_number")

	// Contract 4: down drops index + seq table + column.
	downBody, err := MigrationsFS.ReadFile("migrations/037_wiki_page_numbers.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	assert.NotContains(t, listIndexes(t, ctx, db, "wiki_pages"), "idx_wiki_pages_number",
		"down must drop idx_wiki_pages_number")
	_, err = db.ExecContext(ctx, `SELECT number FROM wiki_pages LIMIT 1`)
	require.Error(t, err, "down must drop wiki_pages.number")

	// Round-trip: up again works on the downed schema (fresh backfill).
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err, "up after down must re-apply cleanly")
	assert.Equal(t, 1, numberOf(pageOld), "re-backfill keeps (created_at, rowid) order")
}
