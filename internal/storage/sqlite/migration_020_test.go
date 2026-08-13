package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 27.8: columns.status migration.
//
// We pin three contracts:
//  1. The five canonical column names keep their lowercase form verbatim
//     (backlog / todo / in_progress / review / done).
//  2. Custom column names get slugified (lowercase, non-alnum → '_'),
//     and collisions within a board are de-duplicated with _2 / _3.
//  3. After backfill, the UNIQUE(board_id, status) index holds —
//     re-applying inserts of duplicate statuses fails.
//
// Down: drops the column and index; previous data loses the canonical
// mapping but is otherwise intact (no destructive rewrite).
func TestMigrate_020ColumnsStatus(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "019_courses")

	// Set up a board with the canonical five columns + two custom
	// columns that would collide under naive slugification.
	const projectID = "p-020"
	const ownerID = "u-020"
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "p@020.local", "x", "P")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO projects (id, name, owner_id) VALUES (?, 'p', ?)`,
		projectID, ownerID)
	require.NoError(t, err)
	const boardID = "b-020"
	_, err = db.ExecContext(ctx,
		`INSERT INTO boards (id, project_id, name) VALUES (?, ?, 'main')`,
		boardID, projectID)
	require.NoError(t, err)
	// Canonical five in the same order as project.DefaultColumns.
	defaults := []struct {
		id, name string
		pos      float64
	}{
		{"c-back", "backlog", 1},
		{"c-todo", "todo", 2},
		{"c-prog", "in_progress", 3},
		{"c-rev", "review", 4},
		{"c-done", "done", 5},
	}
	for _, c := range defaults {
		_, err = db.ExecContext(ctx,
			`INSERT INTO columns (id, board_id, name, position) VALUES (?, ?, ?, ?)`,
			c.id, boardID, c.name, c.pos)
		require.NoError(t, err)
	}
	// Two custom columns whose names slugify to the same value:
	// "QA / In Progress" → "qa_in_progress", "QA-In Progress" →
	// "qa_in_progress". Phase 27.8 unique-by-board rules turn the
	// second one into "qa_in_progress_1" (first duplicate — suffix
	// '_N' starts at 1, not 2).
	_, err = db.ExecContext(ctx,
		`INSERT INTO columns (id, board_id, name, position) VALUES (?, ?, ?, ?)`,
		"c-q1", boardID, "QA / In Progress", 6)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO columns (id, board_id, name, position) VALUES (?, ?, ?, ?)`,
		"c-q2", boardID, "QA-In Progress", 7)
	require.NoError(t, err)

	// Now apply the migration under test.
	body, err := MigrationsFS.ReadFile("migrations/020_columns_status.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	// 1. Canonical five stay verbatim.
	rows, err := db.QueryContext(ctx,
		`SELECT id, status FROM columns WHERE board_id = ? ORDER BY position`, boardID)
	require.NoError(t, err)
	type pair struct{ id, status string }
	var got []pair
	for rows.Next() {
		var p pair
		require.NoError(t, rows.Scan(&p.id, &p.status))
		got = append(got, p)
	}
	require.NoError(t, rows.Close())
	assert.Equal(t, []pair{
		{"c-back", "backlog"},
		{"c-todo", "todo"},
		{"c-prog", "in_progress"},
		{"c-rev", "review"},
		{"c-done", "done"},
		{"c-q1", "qa_in_progress"},
		{"c-q2", "qa_in_progress_1"},
	}, got)

	// 2. UNIQUE(board_id, status) holds — a duplicate insert fails.
	_, err = db.ExecContext(ctx,
		`INSERT INTO columns (id, board_id, name, position, status) VALUES (?, ?, ?, ?, ?)`,
		"c-dup", boardID, "anything", 99, "todo")
	require.Error(t, err, "UNIQUE(board_id, status) must reject the second 'todo' column")

	// 3. Down: drop is clean (column gone, index gone, table usable).
	downBody, err := MigrationsFS.ReadFile("migrations/020_columns_status.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	var hasStatusCol int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('columns') WHERE name='status'`).Scan(&hasStatusCol))
	assert.Equal(t, 0, hasStatusCol, "status column must be dropped")

	var hasIdx int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_columns_board_status'`).Scan(&hasIdx))
	assert.Equal(t, 0, hasIdx, "unique index must be dropped")
}
