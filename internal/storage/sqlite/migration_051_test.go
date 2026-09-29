package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/user"
)

// Task 376 (PRD F-T-3): migration 051 gives every existing board the
// canonical `rejected` parking column. We pin four contracts:
//
//  1. Every board gets exactly one column with machine key `rejected`,
//     name `rejected`, the signature color, positioned AFTER `done`
//     (append-only: no existing position shifts).
//  2. Existing tasks are untouched — rejected is reached by an explicit
//     human move, never backfilled.
//  3. A board that already carries a Phase 27.8 custom column with the
//     machine key `rejected` is skipped — no duplicate.
//  4. Re-running the migration body is a clean no-op (idempotence).
func TestMigrate_051RejectedColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "050_orphan_board_cleanup")

	// Two boards: a plain default-shaped one and one that already has a
	// Phase 27.8 custom `rejected` column (different display name, same
	// machine key).
	const ownerID = "u-051"
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "p@051.local", "x", "P")
	require.NoError(t, err)

	insertBoard := func(t *testing.T, id string, number int) {
		t.Helper()
		_, err := db.ExecContext(ctx,
			`INSERT INTO projects (id, name, owner_id, number) VALUES (?, 'p', ?, ?)`, id, ownerID, number)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`INSERT INTO boards (id, project_id, name) VALUES (?, ?, 'main')`, id, id)
		require.NoError(t, err)
	}
	insertBoard(t, "b-plain", 1)
	insertBoard(t, "b-custom", 2)

	// Default-shaped columns on b-plain (positions as CreateProject seeds
	// them: i*1024).
	for i, name := range []string{"backlog", "todo", "in_progress", "review", "done"} {
		_, err = db.ExecContext(ctx,
			`INSERT INTO columns (id, board_id, name, position, status) VALUES (?, ?, ?, ?, ?)`,
			"c051-"+name, "b-plain", name, float64(i)*1024, name)
		require.NoError(t, err)
	}
	// A live task must survive the backfill untouched.
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, project_id, column_id, title, status, position)
		 VALUES ('t-051', 'b-plain', 'c051-todo', 'survivor', 'todo', 1)`)
	require.NoError(t, err)

	// The custom-rejected board: custom display name, canonical machine key.
	_, err = db.ExecContext(ctx,
		`INSERT INTO columns (id, board_id, name, position, status) VALUES (?, ?, ?, ?, ?)`,
		"c051-nope", "b-custom", "Отклонено", 4096, "rejected")
	require.NoError(t, err)

	// Apply the migration under test.
	body, err := MigrationsFS.ReadFile("migrations/051_rejected_column.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	type colRow struct {
		id       string
		name     string
		status   string
		color    string
		position float64
	}
	countRejected := func(t *testing.T, boardID string) []colRow {
		t.Helper()
		rows, err := db.QueryContext(ctx,
			`SELECT id, name, status, COALESCE(color, ''), position
			 FROM columns WHERE board_id = ? AND status = 'rejected' ORDER BY position`, boardID)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		var out []colRow
		for rows.Next() {
			var c colRow
			require.NoError(t, rows.Scan(&c.id, &c.name, &c.status, &c.color, &c.position))
			out = append(out, c)
		}
		require.NoError(t, rows.Err())
		return out
	}

	// 1+2. Plain board: exactly one appended rejected column, red, right
	// of done; the untouched task still sits in todo.
	plain := countRejected(t, "b-plain")
	require.Len(t, plain, 1, "default board gets exactly one rejected column")
	assert.Equal(t, "rejected", plain[0].name)
	assert.Equal(t, project.RejectedColumnColor, plain[0].color)

	var rejectedPos, donePosition float64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT position FROM columns WHERE id = ?`, plain[0].id).Scan(&rejectedPos))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT position FROM columns WHERE id = 'c051-done'`).Scan(&donePosition))
	assert.Greater(t, rejectedPos, donePosition,
		"rejected appends right of done (parking after the active pipeline)")

	var taskColumn, taskStatus string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT column_id, status FROM tasks WHERE id = 't-051'`).Scan(&taskColumn, &taskStatus))
	assert.Equal(t, "c051-todo", taskColumn, "existing tasks keep their column")
	assert.Equal(t, "todo", taskStatus, "existing tasks keep their status")

	// 3. The custom-rejected board is skipped — no second rejected column.
	assert.Len(t, countRejected(t, "b-custom"), 1,
		"board with a pre-existing rejected machine key keeps exactly its own column")
	var customName string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT name FROM columns WHERE id = 'c051-nope'`).Scan(&customName))
	assert.Equal(t, "Отклонено", customName, "the custom column's identity is untouched")

	// 4. Idempotence: applying the body again must be a no-op.
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)
	assert.Len(t, countRejected(t, "b-plain"), 1, "re-run must not duplicate")
	assert.Len(t, countRejected(t, "b-custom"), 1, "re-run must not duplicate")
}

// T376: the full up chain (fresh database) ends with the rejected column
// seeded by 051 on every CreateProject board, and MigrateDown removes it
// again — the down file drops what the up file added.
func TestMigrate_051UpDownCycle(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	countRejected := func(t *testing.T) int {
		t.Helper()
		var n int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM columns WHERE status = 'rejected'`).Scan(&n))
		return n
	}

	// CreateProject (the Go seed path) must agree with the migration:
	// every fresh board carries the parking column.
	repo := NewProjectRepository(db)
	ownerRepo := NewUserRepository(db)
	owner := &user.User{Email: "cyc-" + newUUID() + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, ownerRepo.Create(ctx, owner))
	_, _, cols, err := repo.CreateProject(ctx, &project.Project{Name: "cycle", OwnerID: owner.ID})
	require.NoError(t, err)
	rejected := 0
	for _, c := range cols {
		if c.Status == "rejected" {
			rejected++
			assert.Equal(t, "rejected", c.Name)
			assert.Equal(t, project.RejectedColumnColor, c.Color)
		}
	}
	assert.Equal(t, 1, rejected, "CreateProject seeds exactly one rejected column")

	// MigrateDown steps back one version (051): the seeded column drops.
	require.NoError(t, MigrateDown(ctx, db, MigrationsFS, "migrations"))
	assert.Equal(t, rejected-1, countRejected(t),
		"051 down removes the migration-added rejected columns")

	// Re-up restores the chain cleanly.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
}
