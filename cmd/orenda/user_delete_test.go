package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/config"
	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// runUserDeleteCLI drives `orenda user delete` with the given flags and
// captures stdout (the preview/confirmation line) for assertions.
func runUserDeleteCLI(t *testing.T, cfgPath string, args []string) (string, error) {
	t.Helper()
	cmd := newUserDeleteCmd()
	// Register --config locally so runUserDelete can read it from
	// cmd.Flags().GetString("config") — same trick as runUserCreateCLI.
	cmd.Flags().String("config", "", "config path (test only)")
	cmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	var buf strings.Builder
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// seedTwoUsers creates the standard test fixture: a primary owner, a
// second owner (so the first one is deletable), and optionally the
// synthetic system user exactly as agent ensureOwner creates it.
func seedTwoUsers(t *testing.T, dir string) (cfgPath, primaryID, secondaryID string) {
	t.Helper()
	dbPath := filepath.Join(dir, "orenda.db")
	cfgPath = filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Storage.DataDir = dir
	cfg.Storage.DBPath = dbPath
	writeConfig(t, cfgPath, cfg)

	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=alice@example.com", "--display-name=Alice"},
		"password-1\n"))
	require.NoError(t, runUserCreateCLI(t, cfgPath,
		[]string{"--email=bob@example.com", "--display-name=Bob"},
		"password-2\n"))

	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := sqlite.NewUserRepository(db)
	alice, err := repo.GetByEmail(context.Background(), "alice@example.com")
	require.NoError(t, err)
	bob, err := repo.GetByEmail(context.Background(), "bob@example.com")
	require.NoError(t, err)
	return cfgPath, alice.ID, bob.ID
}

// seedSystemUser inserts the T171 synthetic agent-owner row directly so
// tests can pin the role=system refusal without booting the agent service.
func seedSystemUser(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO users (id, email, password_hash, display_name, role)
		 VALUES ('system-owner-id', 'agent-owner@orenda.local', 'unusable', 'Agent Owner', 'system')`)
	require.NoError(t, err)
}

// seedCourseWithStatus creates a course owned by userID with the given
// status, through the same repo path the API uses.
func seedCourseWithStatus(t *testing.T, dbPath, userID string, status course.Status) string {
	t.Helper()
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := sqlite.NewCourseRepository(db)
	c := &course.Course{
		Title:   "Test course " + string(status),
		Level:   "beginner",
		Pace:    "regular",
		Status:  status,
		OwnerID: userID,
	}
	require.NoError(t, repo.CreateCourse(context.Background(), c))
	return c.ID
}

// seedProject creates a project owned by userID directly in the DB —
// projects.owner_id has no ON DELETE action (001), so its presence must
// block user deletion.
func seedProject(t *testing.T, dbPath, userID string) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO projects (id, name, owner_id) VALUES (?, 'Blocked project', ?)`, "proj-"+userID[:8], userID)
	require.NoError(t, err)
}

// seedProjectAgentGrant inserts an agent row plus a project_agents
// grant added by userID — added_by has no ON DELETE action (043), so it
// must block user deletion too.
func seedProjectAgentGrant(t *testing.T, dbPath, addedBy string) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes, expires_at)
		 VALUES ('tok-grant', ?, 'grant-token', 'h', '[]', NULL)`, addedBy)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO agents (id, name, type, token_id) VALUES ('agent-grant', 'Grant Agent', 'custom', 'tok-grant')`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO projects (id, name, owner_id) VALUES ('proj-grant', 'Grant project', ?)`, addedBy)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO project_agents (project_id, agent_id, added_by) VALUES ('proj-grant', 'agent-grant', ?)`, addedBy)
	require.NoError(t, err)
}

func TestRunUserDelete_Table(t *testing.T) {

	tests := []struct {
		name string
		args []string
		prep func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string)

		wantErr string // non-empty: command must fail with this substring
		wantOut string // non-empty: stdout must contain this substring
	}{
		{
			name:    "happy path by email",
			args:    []string{"--email=bob@example.com", "--yes"},
			wantOut: "user deleted: id=",
		},
		{
			name:    "happy path by id",
			args:    []string{"--id=BOB_ID", "--yes"},
			wantOut: "email=bob@example.com",
		},
		{
			name:    "unknown email refused",
			args:    []string{"--email=ghost@example.com", "--yes"},
			wantErr: "no such user",
		},
		{
			name:    "unknown id refused",
			args:    []string{"--id=00000000-0000-0000-0000-000000000000", "--yes"},
			wantErr: "no such user",
		},
		{
			name:    "both flags refused",
			args:    []string{"--email=bob@example.com", "--id=BOB_ID", "--yes"},
			wantErr: "exactly one of --email or --id",
		},
		{
			name:    "neither flag refused",
			args:    []string{"--yes"},
			wantErr: "exactly one of --email or --id",
		},
		{
			name:    "no --yes previews and deletes nothing",
			args:    []string{"--email=bob@example.com"},
			wantOut: "would delete user:",
			wantErr: "aborted",
		},
		{
			name: "system role refused",
			args: []string{"--email=agent-owner@orenda.local", "--yes"},
			prep: func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string) {
				seedSystemUser(t, dbPath)
			},
			wantErr: "synthetic agent-owner",
		},
		{
			name: "last non-system owner refused",
			args: []string{"--email=alice@example.com", "--yes"},
			prep: func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string) {
				// Delete bob first through the CLI so only alice remains.
				_, err := runUserDeleteCLI(t, cfgPath, []string{"--email=bob@example.com", "--yes"})
				require.NoError(t, err)
			},
			wantErr: "last non-system owner",
		},
		{
			name: "active course refuses without --force",
			args: []string{"--email=bob@example.com", "--yes"},
			prep: func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string) {
				seedCourseWithStatus(t, dbPath, bobID, course.StatusActive)
			},
			wantErr: "active course",
		},
		{
			name: "force deletes user together with all courses",
			args: []string{"--email=bob@example.com", "--yes", "--force"},
			prep: func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string) {
				seedCourseWithStatus(t, dbPath, bobID, course.StatusActive)
				seedCourseWithStatus(t, dbPath, bobID, course.StatusDraft)
			},
			wantOut: "user deleted: id=",
		},
		{
			name: "project owner refused, nothing deleted",
			args: []string{"--email=bob@example.com", "--yes"},
			prep: func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string) {
				seedProject(t, dbPath, bobID)
				seedCourseWithStatus(t, dbPath, bobID, course.StatusActive)
			},
			wantErr: "delete or reassign the projects first",
		},
		{
			name: "project owner refused even with --force",
			args: []string{"--email=bob@example.com", "--yes", "--force"},
			prep: func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string) {
				seedProject(t, dbPath, bobID)
				seedCourseWithStatus(t, dbPath, bobID, course.StatusActive)
			},
			wantErr: "delete or reassign the projects first",
		},
		{
			name: "project grant adder refused",
			args: []string{"--email=bob@example.com", "--yes"},
			prep: func(t *testing.T, dir, cfgPath, dbPath, aliceID, bobID string) {
				seedProjectAgentGrant(t, dbPath, bobID)
			},
			wantErr: "project-agent grants",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			cfgPath, aliceID, bobID := seedTwoUsers(t, dir)
			if tt.prep != nil {
				tt.prep(t, dir, cfgPath, filepath.Join(dir, "orenda.db"), aliceID, bobID)
			}

			args := make([]string, len(tt.args))
			for i, a := range tt.args {
				args[i] = strings.ReplaceAll(a, "BOB_ID", bobID)
			}
			out, err := runUserDeleteCLI(t, cfgPath, args)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.wantOut != "" {
				assert.Contains(t, out, tt.wantOut)
			}

			// Post-state: verify DB contents through the repos.
			db, err := sqlite.Open(context.Background(), filepath.Join(dir, "orenda.db"), sqlite.OpenConfig{
				WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
			})
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			users := sqlite.NewUserRepository(db)
			coursesRepo := sqlite.NewCourseRepository(db)

			bobGone := true
			if _, gerr := users.GetByEmail(context.Background(), "bob@example.com"); gerr == nil {
				bobGone = false
			}
			coursesLeft, cerr := coursesRepo.ListCourses(context.Background(), "")
			require.NoError(t, cerr)
			switch tt.name {
			case "happy path by email", "happy path by id", "force deletes user together with all courses":
				assert.True(t, bobGone, "bob must be gone from users")
				assert.Empty(t, coursesLeft, "bob's courses must be gone")
			case "no --yes previews and deletes nothing":
				assert.False(t, bobGone, "preview must not delete bob")
			case "active course refuses without --force":
				assert.False(t, bobGone, "refused delete must not remove bob")
				assert.Len(t, coursesLeft, 1, "refused delete must not touch courses")
			case "project owner refused, nothing deleted", "project owner refused even with --force":
				assert.False(t, bobGone, "project refusal must not remove bob")
				assert.Len(t, coursesLeft, 1, "project refusal must not touch courses (atomicity)")
				var projects int
				require.NoError(t, db.QueryRowContext(context.Background(),
					`SELECT COUNT(*) FROM projects WHERE owner_id = ?`, bobID).Scan(&projects))
				assert.Equal(t, 1, projects, "project must survive the refusal")
			case "project grant adder refused":
				assert.False(t, bobGone, "grant refusal must not remove bob")
			case "last non-system owner refused":
				assert.True(t, bobGone, "bob was deleted by prep")
			}
		})
	}
}

// TestRunUserDelete_DeletedUserVanishesFromList pins the end-to-end
// observable: after `user delete --yes`, `orenda user list` no longer
// shows the deleted account.
func TestRunUserDelete_DeletedUserVanishesFromList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath, _, _ := seedTwoUsers(t, dir)

	out, err := runUserListCLI(t, cfgPath)
	require.NoError(t, err)
	assert.Contains(t, out, "bob@example.com")

	_, err = runUserDeleteCLI(t, cfgPath, []string{"--email=bob@example.com", "--yes"})
	require.NoError(t, err)

	out, err = runUserListCLI(t, cfgPath)
	require.NoError(t, err)
	assert.NotContains(t, out, "bob@example.com")
	assert.Contains(t, out, "alice@example.com")
}

// TestRunUserDelete_SystemUserNotInLastOwnerCount pins the guard edge:
// the synthetic system user does NOT count as a surviving owner, so a
// lone human owner stays protected even on a system-seeded database.
func TestRunUserDelete_SystemUserNotInLastOwnerCount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath, _, _ := seedTwoUsers(t, dir)
	seedSystemUser(t, filepath.Join(dir, "orenda.db"))

	// Bob can go (system user present, alice remains).
	_, err := runUserDeleteCLI(t, cfgPath, []string{"--email=bob@example.com", "--yes"})
	require.NoError(t, err)

	// Alice cannot: only the system user would remain.
	_, err = runUserDeleteCLI(t, cfgPath, []string{"--email=alice@example.com", "--yes"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last non-system owner")
}

// TestRunUserDelete_ForceWithoutActiveCourses pins that --force also
// sweeps non-active (draft) courses — all courses, not only active ones.
func TestRunUserDelete_ForceWithoutActiveCourses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath, _, bobID := seedTwoUsers(t, dir)
	courseID := seedCourseWithStatus(t, filepath.Join(dir, "orenda.db"), bobID, course.StatusDraft)

	// Draft-only: no --force needed.
	_, err := runUserDeleteCLI(t, cfgPath, []string{"--email=bob@example.com", "--yes"})
	require.NoError(t, err)

	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "orenda.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	users := sqlite.NewUserRepository(db)
	_, err = users.GetByID(context.Background(), bobID)
	assert.ErrorIs(t, err, user.ErrNotFound)

	coursesRepo := sqlite.NewCourseRepository(db)
	_, err = coursesRepo.GetCourse(context.Background(), courseID)
	assert.ErrorIs(t, err, course.ErrNotFound)
}

var _ = user.RoleOwner // keep user import if assertions shrink
