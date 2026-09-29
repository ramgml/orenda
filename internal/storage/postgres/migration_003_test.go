package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task 376 (PRD F-T-3): migration 003 gives every existing board the
// canonical `rejected` parking column — the postgres counterpart of the
// sqlite 051 contract. Pinned here:
//
//  1. Every board gets exactly one column with machine key `rejected`,
//     the signature color, appended AFTER `done` (no position shifts).
//  2. Existing tasks are untouched.
//  3. A board that already carries a Phase 27.8 custom column with the
//     machine key `rejected` is skipped — no duplicate.
//  4. Re-running the migration body is a clean no-op; the down file
//     drops what the up file added.
func TestMigrate_003RejectedColumn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// Fresh database, full chain — 003 runs against empty boards here;
	// the seeded-board scenarios below re-run its body directly.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	upBody, err := MigrationsFS.ReadFile("migrations/003_rejected_column.up.sql")
	require.NoError(t, err)
	downBody, err := MigrationsFS.ReadFile("migrations/003_rejected_column.down.sql")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ('u-003', 'p@003.local', 'x', 'P')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO projects (id, name, owner_id, number) VALUES ('p-003', 'p', 'u-003', 1)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO boards (id, project_id, name) VALUES ('b-003-plain', 'p-003', 'main')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO boards (id, project_id, name) VALUES ('b-003-custom', 'p-003', 'main')`)
	require.NoError(t, err)

	for i, name := range []string{"backlog", "todo", "in_progress", "review", "done"} {
		_, err = db.ExecContext(ctx,
			`INSERT INTO columns (id, board_id, name, position, status)
			 VALUES ($1, 'b-003-plain', $2, $3, $2)`, "c003-"+name, name, i*1024)
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, column_id, title, status, position)
		 VALUES ('t-003', 'p-003', 'c003-todo', 'survivor', 'todo', 1)`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO columns (id, board_id, name, position, status)
		 VALUES ('c003-nope', 'b-003-custom', 'Отклонено', 4096, 'rejected')`)
	require.NoError(t, err)

	countRejected := func(t *testing.T, board string) []struct {
		id       string
		name     string
		color    string
		position float64
	} {
		t.Helper()
		rows, err := db.QueryContext(ctx,
			`SELECT id, name, COALESCE(color, ''), position
			 FROM columns WHERE board_id = $1 AND status = 'rejected' ORDER BY position`, board)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		var out []struct {
			id       string
			name     string
			color    string
			position float64
		}
		for rows.Next() {
			var c struct {
				id       string
				name     string
				color    string
				position float64
			}
			require.NoError(t, rows.Scan(&c.id, &c.name, &c.color, &c.position))
			out = append(out, c)
		}
		require.NoError(t, rows.Err())
		return out
	}

	_, err = db.ExecContext(ctx, string(upBody))
	require.NoError(t, err)

	plain := countRejected(t, "b-003-plain")
	require.Len(t, plain, 1, "default board gets exactly one rejected column")
	assert.Equal(t, "rejected", plain[0].name)
	assert.Equal(t, "#ef4444", plain[0].color)

	var donePosition, rejectedPosition float64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT position FROM columns WHERE id = 'c003-done'`).Scan(&donePosition))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT position FROM columns WHERE id = $1`, plain[0].id).Scan(&rejectedPosition))
	assert.Greater(t, rejectedPosition, donePosition,
		"rejected appends right of done (parking after the active pipeline)")

	var taskColumn, taskStatus string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT column_id, status FROM tasks WHERE id = 't-003'`).Scan(&taskColumn, &taskStatus))
	assert.Equal(t, "c003-todo", taskColumn, "existing tasks keep their column")
	assert.Equal(t, "todo", taskStatus, "existing tasks keep their status")

	assert.Len(t, countRejected(t, "b-003-custom"), 1,
		"board with a pre-existing rejected machine key keeps exactly its own column")
	var customName string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT name FROM columns WHERE id = 'c003-nope'`).Scan(&customName))
	assert.Equal(t, "Отклонено", customName, "the custom column's identity is untouched")

	// Idempotence: the up body again is a no-op.
	_, err = db.ExecContext(ctx, string(upBody))
	require.NoError(t, err)
	assert.Len(t, countRejected(t, "b-003-plain"), 1, "re-run must not duplicate")

	// Down: the migration-added (and ours-named) rejected columns drop.
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)
	assert.Empty(t, countRejected(t, "b-003-plain"), "down removes the seeded parking column")
}
