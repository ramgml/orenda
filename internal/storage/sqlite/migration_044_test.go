package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/user"
)

// Migration 044: synthetic agent-owner user gets role='system' (Task 171,
// security hardening).
//
// The agent service lazily creates agent-owner@orenda.local with the
// constant "unusable" password; before T171 it was created without a
// role and domain/user.Validate defaulted it to 'owner' — a full owner
// session for anyone who knew the constant. Fresh databases now get
// role='system' directly from ensureOwner; this migration heals
// instances whose synthetic user already exists with role='owner'.
//
// Contracts:
//  1. Up migrates the synthetic user from any non-system role to
//     'system'.
//  2. Other users (the real owner) are untouched.
//  3. The UPDATE is idempotent: re-running affects 0 rows and leaves
//     the state unchanged.
//  4. Down restores the historical role='owner' for the synthetic user.
func TestMigrate_044AgentOwnerSystemRole(t *testing.T) {
	ctx := context.Background()

	t.Run("heals legacy owner role, leaves others untouched, idempotent", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "orenda.db")
		db, err := Open(ctx, dbPath, OpenConfig{
			WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		// Legacy state: schema up to 043 (no seed exists for the
		// synthetic user — ensureOwner creates it lazily — so we
		// simulate a pre-hardening instance by creating the row via
		// the same repository path the service used, which defaults
		// the role to owner exactly like the old ensureOwner did).
		applyUpTo(t, ctx, db, "043_project_agent_access")

		users := NewUserRepository(db)
		legacy := &user.User{
			ID:           "00000000-0000-7000-8000-0000000000aa",
			Email:        "agent-owner@orenda.local",
			PasswordHash: "unusable",
			DisplayName:  "Agent Owner",
			// Role empty → repo Create → Validate → defaults to owner,
			// the same path the pre-T171 service exercised.
		}
		require.NoError(t, users.Create(ctx, legacy))
		require.Equal(t, user.RoleOwner, legacy.Role, "fixture must reproduce the legacy owner default")

		// A real human owner must NOT be touched by the migration.
		human := &user.User{
			ID:           "00000000-0000-7000-8000-0000000000bb",
			Email:        "human-owner@orenda.local",
			PasswordHash: "x",
			DisplayName:  "Human Owner",
			Role:         user.RoleOwner,
		}
		require.NoError(t, users.Create(ctx, human))

		// Apply 044 the way the runner would.
		body, err := MigrationsFS.ReadFile("migrations/044_agent_owner_system_role.sql")
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err)

		roleOf := func(id string) string {
			t.Helper()
			var role string
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT role FROM users WHERE id = ?`, id).Scan(&role))
			return role
		}

		// Contract 1: synthetic user healed to system.
		assert.Equal(t, "system", roleOf(legacy.ID), "synthetic agent-owner must become role='system'")
		// Contract 2: the real owner is untouched.
		assert.Equal(t, "owner", roleOf(human.ID), "real owner must keep role='owner'")

		// Contract 3: idempotent — re-run affects 0 rows and state holds.
		res, err := db.ExecContext(ctx, string(body))
		require.NoError(t, err)
		affected, err := res.RowsAffected()
		require.NoError(t, err)
		assert.Equal(t, int64(0), affected, "re-run must affect 0 rows")
		assert.Equal(t, "system", roleOf(legacy.ID))
		assert.Equal(t, "owner", roleOf(human.ID))

		// Contract 4: down restores the historical owner role for the
		// synthetic user only.
		downBody, err := MigrationsFS.ReadFile("migrations/044_agent_owner_system_role.down.sql")
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(downBody))
		require.NoError(t, err)
		assert.Equal(t, "owner", roleOf(legacy.ID), "down must restore the pre-hardening role")
		assert.Equal(t, "owner", roleOf(human.ID))

		// Down is also idempotent (0 rows on re-run).
		res, err = db.ExecContext(ctx, string(downBody))
		require.NoError(t, err)
		affected, err = res.RowsAffected()
		require.NoError(t, err)
		assert.Equal(t, int64(0), affected, "down re-run must affect 0 rows")
	})

	t.Run("full chain applies cleanly on fresh database", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "orenda.db")
		db, err := Open(ctx, dbPath, OpenConfig{
			WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		require.NoError(t, Migrate(ctx, db, MigrationsFS, "migrations"))
		versions, err := AppliedVersions(ctx, db)
		require.NoError(t, err)
		assert.Contains(t, versions, "044_agent_owner_system_role")
	})
}
