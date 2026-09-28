package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countFKViolations runs PRAGMA foreign_key_check and returns how many
// violation rows come back (0 = clean).
func countFKViolations(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		n++
	}
	require.NoError(t, rows.Err())
	return n
}

// TestMigrate_050OrphanBoardCleanup pins the T369 repair: migration 015
// removed the system-Inbox project under foreign_keys=OFF, so its
// `DELETE FROM projects` never fired the boards→projects ON DELETE
// CASCADE and upgraded installs kept a bootstrap board (plus columns)
// pointing at the missing ...cafe project. 050 must delete those orphan
// rows while leaving boards of real projects untouched, and afterwards
// PRAGMA foreign_key_check must come back empty.
func TestMigrate_050OrphanBoardCleanup(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "orenda.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	// 1. Everything up to the cleanup migration simulates a database
	//    upgraded from the legacy era.
	applyUpTo(t, ctx, db, "049_chat_messages_user_idx")

	const cafeProject = "00000000-0000-0000-0000-00000000cafe" // removed by 015
	const orphanBoard = "70194de4-ac95-48ca-818c-000000000001" // legacy bootstrap shape
	const liveProject = "p-050-live"
	const liveBoard = "b-050-live"

	// 2. A real project with a real board must survive the cleanup.
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, display_name)
		VALUES ('u-050', 'owner@050.local', 'x', 'Owner')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO projects (id, name, owner_id) VALUES (?, 'live', 'u-050')`, liveProject)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO boards (id, project_id, name, position) VALUES (?, ?, 'Main', 0)`, liveBoard, liveProject)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO columns (id, board_id, name, position) VALUES ('col-050-live', ?, 'todo', 0)`, liveBoard)
	require.NoError(t, err)

	// 3. Reproduce the legacy damage: seed the orphan board and its
	//    column with foreign_keys OFF — exactly the condition under
	//    which 015 ran (orenda:foreign_keys_off) and left them behind.
	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO boards (id, project_id, name, position) VALUES (?, ?, 'Main', 0);
		INSERT INTO columns (id, board_id, name, position) VALUES ('col-050-orphan', ?, 'todo', 0);
	`, orphanBoard, cafeProject, orphanBoard)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	require.NoError(t, err)

	// Sanity: the damage is visible to foreign_key_check before 050.
	require.Positive(t, countFKViolations(t, ctx, db),
		"orphan board must be reported before the cleanup migration")

	// 4. Apply the remaining migrations (050 among them).
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	// 5. The violation is gone and only the live board remains.
	require.Equal(t, 0, countFKViolations(t, ctx, db),
		"foreign_key_check must be empty after 050")

	var boards, columns int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM boards`).Scan(&boards))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM columns`).Scan(&columns))
	assert.Equal(t, 1, boards, "only the live project's board must remain")
	assert.Equal(t, 1, columns, "only the live board's column must remain")

	var name string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT name FROM boards WHERE id = ?`, liveBoard).Scan(&name))
	assert.Equal(t, "Main", name)
}
