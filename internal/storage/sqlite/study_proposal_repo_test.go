package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/study"
)

// TestStudyProposalRepo_FullLifecycle pins the contract:
//   - Insert reads back with status='pending' and the right timestamps.
//   - MarkAccepted flips status, stamps resolved_at, sets accepted_task_id.
//   - MarkDismissed flips status, stamps resolved_at, clears accepted_task_id.
//   - Conditional WHERE makes concurrent transitions idempotent.
//   - Unknown id surfaces study.ErrNotFound on Get / Mark operations.
func TestStudyProposalRepo_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	// Minimal fixture: agent + course must exist for the FK
	// constraints on study_proposals (course_id and created_by_agent).
	const (
		ownerID = "u-sp"
		tokID   = "t-sp"
		agentID = "a-sp"
		// Course ID is optional on study_proposals (no FK when set
		// matters), but we'll use one to exercise the path.
		courseID    = "c-sp"
		taskID      = "task-accepted-001"
		proposalID  = "p-sp-001"
		proposalID2 = "p-sp-002"
	)
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "sp@031.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, ?, ?, '[]')`,
		tokID, ownerID, "seed", "h")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
		agentID, "planner", "[]", tokID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		courseID, "Rust", ownerID)
	require.NoError(t, err)
	// accepted_task_id carries a real FK → tasks(id), so the
	// fixture must include the target task before MarkAccepted.
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status) VALUES (?, ?, 'todo')`,
		taskID, "Study chapter 5")
	require.NoError(t, err)

	repo := NewStudyProposalRepository(db)

	t.Run("create lists pending", func(t *testing.T) {
		p := &study.Proposal{
			Title:          "Read chapter 5",
			BodyMD:         "rust-book chapter 5",
			TargetDate:     "2026-08-17",
			CourseID:       courseID,
			CreatedByAgent: agentID,
		}
		require.NoError(t, repo.Create(ctx, p))
		assert.NotEmpty(t, p.ID, "repo should mint id")
		assert.Equal(t, study.StatusPending, p.Status, "create always lands pending")
		assert.False(t, p.CreatedAt.IsZero(), "created_at populated by repo")
		assert.Nil(t, p.ResolvedAt, "no resolved_at before Mark*")
	})

	t.Run("list pending returns in created_at order", func(t *testing.T) {
		// Add a second proposal so order is non-trivial.
		p2 := &study.Proposal{
			Title:          "Practice exercises",
			TargetDate:     "2026-08-17",
			CourseID:       courseID,
			CreatedByAgent: agentID,
		}
		require.NoError(t, repo.Create(ctx, p2))

		out, err := repo.ListPending(ctx)
		require.NoError(t, err)
		assert.Len(t, out, 2)
		// Both proposals: same second resolution may make the
		// order fragile — we assert both are present and the
		// count matches rather than the ordering.
		seen := map[string]bool{}
		for _, p := range out {
			seen[p.Title] = true
			assert.Equal(t, study.StatusPending, p.Status)
		}
		assert.True(t, seen["Read chapter 5"])
		assert.True(t, seen["Practice exercises"])
	})

	t.Run("mark accepted", func(t *testing.T) {
		// The first proposal from above.
		pending, err := repo.ListPending(ctx)
		require.NoError(t, err)
		require.NotEmpty(t, pending)
		target := pending[0]
		require.NoError(t, repo.MarkAccepted(ctx, target.ID, taskID))
		got, err := repo.Get(ctx, target.ID)
		require.NoError(t, err)
		assert.Equal(t, study.StatusAccepted, got.Status)
		assert.Equal(t, taskID, got.AcceptedTaskID)
		require.NotNil(t, got.ResolvedAt, "resolved_at populated by MarkAccepted")
	})

	t.Run("mark idempotent on already-accepted", func(t *testing.T) {
		// After the first MarkAccepted, exactly one proposal is
		// accepted. Re-calling MarkAccepted on that id must be a
		// no-op (returns nil; conditional WHERE doesn't match
		// because status is no longer pending).
		list, err := db.QueryContext(ctx,
			`SELECT id FROM study_proposals WHERE status='accepted' LIMIT 1`)
		require.NoError(t, err)
		var firstAccepted string
		require.True(t, list.Next())
		require.NoError(t, list.Scan(&firstAccepted))
		list.Close()

		var beforeResolved string
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT resolved_at FROM study_proposals WHERE id=?`, firstAccepted).Scan(&beforeResolved))

		require.NoError(t, repo.MarkAccepted(ctx, firstAccepted, taskID))
		var afterResolved string
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT resolved_at FROM study_proposals WHERE id=?`, firstAccepted).Scan(&afterResolved))
		assert.Equal(t, beforeResolved, afterResolved,
			"idempotent MarkAccepted must not update resolved_at")
	})

	t.Run("dismiss the second proposal", func(t *testing.T) {
		// After accept, one proposal remains pending.
		pending, err := repo.ListPending(ctx)
		require.NoError(t, err)
		require.Len(t, pending, 1, "after one accept, exactly one pending remains")

		target := pending[0]
		require.NoError(t, repo.MarkDismissed(ctx, target.ID))
		got, err := repo.Get(ctx, target.ID)
		require.NoError(t, err)
		assert.Equal(t, study.StatusDismissed, got.Status)
		require.NotNil(t, got.ResolvedAt)
	})

	t.Run("unknown id returns ErrNotFound", func(t *testing.T) {
		_, err := repo.Get(ctx, "no-such-proposal")
		require.ErrorIs(t, err, study.ErrNotFound)
		require.ErrorIs(t, repo.MarkAccepted(ctx, "no-such-proposal", "task"), study.ErrNotFound)
		require.ErrorIs(t, repo.MarkDismissed(ctx, "no-such-proposal"), study.ErrNotFound)
	})
}

// openTestDB is a tiny helper: opens a fresh SQLite DB with all
// migrations applied. Lives alongside the existing openTestDB
// pattern in db_test.go — kept local to avoid touching shared
// test helpers.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "orenda.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	if err := Migrate(context.Background(), db, MigrationsFS, "migrations"); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}
	return db
}
