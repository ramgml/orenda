package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 28.19: agents.type backfilled into a JSON-array column.
//
// We pin three contracts:
//  1. Empty type becomes the JSON empty array ('[]').
//  2. A single string becomes a singleton JSON array.
//  3. Idempotency — re-applying the migration leaves a row that already
//     carries a JSON array unchanged.
//
// Down is lossy (multi-label rows collapse to first label); we pin the
// round-trip on a synthetic fixture but document the loss in the file.
func TestMigrate_021AgentTypeLabels(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "020_columns_status")

	// Minimal fixture: an owner user + three agents with different
	// pre-migration `type` values. We bypass the repository on purpose
	// to seed the raw column shapes this migration needs to translate.
	const ownerID = "u-021"
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "u@021.local", "x", "U")
	require.NoError(t, err)

	const tokID = "t-021"
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, ?, ?, '[]')`,
		tokID, ownerID, "seed", "h")
	require.NoError(t, err)

	rows := []struct{ id, typ string }{
		{"a-empty", ""},
		{"a-qwen", "qwen"},
		{"a-claude", "claude"},
		{"a-custom", "experimental-pipeline"},
	}
	for _, r := range rows {
		_, err = db.ExecContext(ctx,
			`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
			r.id, r.id, r.typ, tokID)
		require.NoError(t, err)
	}

	// Apply the migration under test.
	body, err := MigrationsFS.ReadFile("migrations/021_agent_type_labels.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)

	// 1+2. Backfill produces JSON arrays.
	type pair struct{ id, typ string }
	want := []pair{
		{"a-claude", `["claude"]`},
		{"a-custom", `["experimental-pipeline"]`},
		{"a-empty", "[]"},
		{"a-qwen", `["qwen"]`},
	}
	rs, err := db.QueryContext(ctx, `SELECT id, type FROM agents ORDER BY id`)
	require.NoError(t, err)
	var got []pair
	for rs.Next() {
		var p pair
		require.NoError(t, rs.Scan(&p.id, &p.typ))
		got = append(got, p)
	}
	require.NoError(t, rs.Close())
	assert.Equal(t, want, got)

	// 3. Idempotency — re-applying the migration must not corrupt
	// rows that already carry a JSON array.
	_, err = db.ExecContext(ctx, string(body))
	require.NoError(t, err)
	rs, err = db.QueryContext(ctx, `SELECT id, type FROM agents ORDER BY id`)
	require.NoError(t, err)
	var got2 []pair
	for rs.Next() {
		var p pair
		require.NoError(t, rs.Scan(&p.id, &p.typ))
		got2 = append(got2, p)
	}
	require.NoError(t, rs.Close())
	assert.Equal(t, want, got2, "re-apply must be idempotent on JSON-array rows")

	// Down — collapses to first element (lossy by design). After
	// down the rows that came in as '' or a single value should be
	// back to that scalar; the multi-label round-trip is not asserted
	// here because we never seeded >1 label.
	downBody, err := MigrationsFS.ReadFile("migrations/021_agent_type_labels.down.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)

	rs, err = db.QueryContext(ctx, `SELECT id, type FROM agents ORDER BY id`)
	require.NoError(t, err)
	var gotDown []pair
	for rs.Next() {
		var p pair
		require.NoError(t, rs.Scan(&p.id, &p.typ))
		gotDown = append(gotDown, p)
	}
	require.NoError(t, rs.Close())
	assert.Equal(t, []pair{
		{"a-claude", "claude"},
		{"a-custom", "experimental-pipeline"},
		{"a-empty", ""},
		{"a-qwen", "qwen"},
	}, gotDown, "down must restore the original scalar value")

	// Lossy path: seed a multi-label JSON array manually, run down,
	// expect only the first label survives.
	_, err = db.ExecContext(ctx,
		`UPDATE agents SET type = '["qwen","installer"]' WHERE id = 'a-qwen'`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)
	var multiDown string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT type FROM agents WHERE id = 'a-qwen'`).Scan(&multiDown))
	assert.Equal(t, "qwen", multiDown,
		"down collapses multi-label to first element — documented lossy behaviour")

	// Down idempotency: rows are now plain scalars; re-applying the
	// down must leave them untouched (no malformed-JSON error from
	// json_extract on a non-JSON string).
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err, "down must be idempotent on already-scalar rows")
	rs, err = db.QueryContext(ctx, `SELECT id, type FROM agents ORDER BY id`)
	require.NoError(t, err)
	var gotDown2 []pair
	for rs.Next() {
		var p pair
		require.NoError(t, rs.Scan(&p.id, &p.typ))
		gotDown2 = append(gotDown2, p)
	}
	require.NoError(t, rs.Close())
	assert.Equal(t, []pair{
		{"a-claude", "claude"},
		{"a-custom", "experimental-pipeline"},
		{"a-empty", ""},
		{"a-qwen", "qwen"},
	}, gotDown2, "down-on-scalars is a no-op")
}
