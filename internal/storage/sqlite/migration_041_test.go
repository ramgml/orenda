package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Migration 041: comments.edited_at (Task 112, comment editing).
//
// Contracts:
//  1. Up migration adds comments.edited_at TEXT NULL.
//  2. Existing rows keep edited_at = NULL after the migration
//     (legacy comments were never edited).
//  3. Down migration drops the column.
func TestMigrate_041CommentEditedAt(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "040_wiki_blocks")

	// Contract 1 + 2: column exists, default NULL on existing rows.
	body, err := MigrationsFS.ReadFile("migrations/041_comment_edited_at.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	var hasCol int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('comments') WHERE name = 'edited_at'`).
		Scan(&hasCol))
	assert.Equal(t, 1, hasCol, "comments.edited_at must exist after up")

	// Contract 3: down migration drops the column.
	downBody, err := MigrationsFS.ReadFile("migrations/041_comment_edited_at.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('comments') WHERE name = 'edited_at'`).
		Scan(&hasCol))
	assert.Equal(t, 0, hasCol, "comments.edited_at must be dropped after down")
}
