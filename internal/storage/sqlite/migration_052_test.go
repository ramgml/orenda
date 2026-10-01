package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/user"
)

// master-agent-role plan (step 1): migration 052 gives agents a `role`
// column defaulting to 'project'. Pinned here:
//
//  1. Pre-existing (and fresh) rows backfill to 'project' via NOT NULL
//     DEFAULT — pre-052 behaviour is exactly project-scoped.
//  2. The repo round-trips the role through Create → GetByID /
//     GetByTokenID / List, including role='master'.
//  3. MigrateDown (052) drops the column again; re-up restores the chain.
func TestMigrate_052AgentRole(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := Open(context.Background(), dbPath, OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	applyUpTo(t, ctx, db, "051_rejected_column")

	// A pre-052 agent row: no role column yet, so insert without it.
	const ownerID = "u-052"
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "p@052.local", "x", "P")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES ('tok-052', ?, 'pre', 'h', '[]')`,
		ownerID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, description, token_id, status)
		 VALUES ('a-052-pre', 'pre-052', '[]', '', 'tok-052', 'offline')`)
	require.NoError(t, err)

	// Bring the chain to 052 through the runner so schema_migrations
	// bookkeeping stays truthful for the MigrateDown below.
	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))

	// 1. The pre-existing row backfills to 'project'.
	var preRole string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT role FROM agents WHERE id = 'a-052-pre'`).Scan(&preRole))
	assert.Equal(t, "project", preRole, "existing agents default to project scope")

	// 2. Repo round-trip: default (empty role) and explicit master.
	// Each agent gets its own token row — GetByTokenID resolves 1:1.
	repo := NewAgentRepository(db)
	users := NewUserRepository(db)
	owner := &user.User{Email: "role-" + newUUID() + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(ctx, owner))
	for _, tokID := range []string{"tok-052-def", "tok-052-master"} {
		_, err = db.ExecContext(ctx,
			`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, 't', 'h', '[]')`,
			tokID, owner.ID)
		require.NoError(t, err)
	}

	def := &agent.Agent{Name: "role-default-" + newUUID()[:8], Type: []string{"test"}, TokenID: "tok-052-def"}
	require.NoError(t, repo.Create(ctx, def))
	assert.Equal(t, agent.RoleProject, def.Role, "empty role normalises to project")

	master := &agent.Agent{Name: "role-master-" + newUUID()[:8], Type: []string{"test"}, TokenID: "tok-052-master", Role: agent.RoleMaster}
	require.NoError(t, repo.Create(ctx, master))

	byID, err := repo.GetByID(ctx, master.ID)
	require.NoError(t, err)
	assert.Equal(t, agent.RoleMaster, byID.Role)

	byToken, err := repo.GetByTokenID(ctx, master.TokenID)
	require.NoError(t, err)
	assert.Equal(t, agent.RoleMaster, byToken.Role, "GetByTokenID (the RequireUser/RequireAgent lookup) carries the role")

	list, err := repo.List(ctx)
	require.NoError(t, err)
	roles := map[string]agent.Role{}
	for _, a := range list {
		roles[a.Name] = a.Role
	}
	assert.Equal(t, agent.RoleProject, roles[def.Name])
	assert.Equal(t, agent.RoleMaster, roles[master.Name])

	// 3. Down drops the column; re-up restores the chain.
	require.NoError(t, MigrateDown(ctx, db, MigrationsFS, "migrations"))
	_, err = db.QueryContext(ctx, `SELECT role FROM agents LIMIT 1`)
	require.Error(t, err, "052 down must remove the role column")

	require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
	var restored string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT role FROM agents WHERE id = 'a-052-pre'`).Scan(&restored))
	assert.Equal(t, "project", restored)
}
