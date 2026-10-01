package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// master-agent-role plan (step 1): migration 004 gives agents a `role`
// column defaulting to 'project' — the postgres counterpart of the sqlite
// 052 contract. Pinned here:
//
//  1. Fresh rows backfill to 'project' via NOT NULL DEFAULT.
//  2. The up body is idempotent-safe to re-run is NOT claimed (plain
//     ALTER TABLE); instead the down file drops what up added.
func TestMigrate_004AgentRole(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	downBody, err := MigrationsFS.ReadFile("migrations/004_agent_role.down.sql")
	require.NoError(t, err)

	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ('u-004', 'p@004.local', 'x', 'P')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES ('tok-004', 'u-004', 'pre', 'h', '[]')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, description, token_id, status)
		 VALUES ('a-004', 'pre-004', '[]', '', 'tok-004', 'offline')`)
	require.NoError(t, err)

	var role string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT role FROM agents WHERE id = 'a-004'`).Scan(&role))
	assert.Equal(t, "project", role, "agents default to project scope")

	// Down: the column drops; re-up restores it.
	_, err = db.ExecContext(ctx, string(downBody))
	require.NoError(t, err)
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT role FROM agents WHERE id = 'a-004'`).Scan(&role))
	assert.Equal(t, "project", role)
}
