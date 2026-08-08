package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/timeentry"
	"github.com/ramgml/orenda/internal/domain/user"
)

// setupTimeEntryDB creates a fully wired DB with one user + one project
// + one task + one agent. Returns the db + the seeded task + agent ids.
func setupTimeEntryDB(t *testing.T) (*sql.DB, string, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir+"/te.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, Migrate(context.Background(), db, MigrationsFS, "migrations"))

	users := NewUserRepository(db)
	owner := &user.User{Email: "te-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + newUUID()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), owner))

	tokens := NewAPITokenRepository(db)
	tok, err := tokens.Create(context.Background(), owner.ID, "te-tok", "fake", "[]", nil)
	require.NoError(t, err)
	agents := NewAgentRepository(db)
	a := &agent.Agent{Name: "te-" + newUUID()[:6], Type: agent.TypeQwen, TokenID: tok.ID}
	require.NoError(t, agents.Create(context.Background(), a))

	projects := NewProjectRepository(db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{Name: "TE", OwnerID: owner.ID})
	require.NoError(t, err)

	tasks := NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "t"}
	require.NoError(t, tasks.Create(context.Background(), tr))
	return db, tr.ID, a.ID
}

func TestTimeEntryRepo_StartStopSingleActive(t *testing.T) {
	db, taskID, agentID := setupTimeEntryDB(t)
	repo := NewTimeEntryRepository(db)

	now := time.Now().Truncate(time.Second)
	open := &timeentry.TimeEntry{
		TaskID:    taskID,
		AgentID:   agentID,
		StartedAt: now,
		Source:    timeentry.SourceTimer,
	}
	got, err := repo.Create(context.Background(), open)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)

	// Second open entry for the same agent → ErrAlreadyOpen.
	dup := &timeentry.TimeEntry{
		TaskID:    taskID,
		AgentID:   agentID,
		StartedAt: now.Add(time.Minute),
	}
	_, err = repo.Create(context.Background(), dup)
	assert.ErrorIs(t, err, timeentry.ErrAlreadyOpen)

	// FindOpen returns the existing one.
	openFound, err := repo.FindOpen(context.Background(), agentID)
	require.NoError(t, err)
	require.NotNil(t, openFound)
	assert.Equal(t, got.ID, openFound.ID)

	// Stop the timer.
	ended := now.Add(30 * time.Minute)
	duration := int64(ended.Sub(now).Seconds())
	got.EndedAt = &ended
	got.DurationS = &duration
	require.NoError(t, repo.Update(context.Background(), got))

	// Now we can open another one.
	second := &timeentry.TimeEntry{
		TaskID:    taskID,
		AgentID:   agentID,
		StartedAt: ended.Add(time.Second),
	}
	_, err = repo.Create(context.Background(), second)
	require.NoError(t, err)

	// FindOpen now returns the new one.
	openFound, err = repo.FindOpen(context.Background(), agentID)
	require.NoError(t, err)
	require.NotNil(t, openFound)
}

func TestTimeEntryRepo_ListByTask(t *testing.T) {
	db, taskID, agentID := setupTimeEntryDB(t)
	repo := NewTimeEntryRepository(db)
	now := time.Now().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		ended := now.Add(time.Duration(i+1) * time.Hour)
		dur := int64(ended.Sub(now).Seconds())
		_, err := repo.Create(context.Background(), &timeentry.TimeEntry{
			TaskID:    taskID,
			AgentID:   agentID,
			StartedAt: now.Add(time.Duration(i) * time.Minute),
			EndedAt:   &ended,
			DurationS: &dur,
			Source:    timeentry.SourceManual,
		})
		require.NoError(t, err)
	}

	list, err := repo.ListByTask(context.Background(), taskID)
	require.NoError(t, err)
	assert.Len(t, list, 3)
	// First row is most recent (started_at DESC)
	for i := 0; i < 2; i++ {
		assert.True(t, list[i].StartedAt.After(list[i+1].StartedAt) ||
			list[i].StartedAt.Equal(list[i+1].StartedAt))
	}
}

func TestTimeEntryRepo_ListByAgentRange(t *testing.T) {
	db, taskID, agentID := setupTimeEntryDB(t)
	repo := NewTimeEntryRepository(db)
	now := time.Now().Truncate(time.Second)

	for i := 0; i < 3; i++ {
		ended := now.Add(time.Duration(i+1) * time.Hour)
		dur := int64(ended.Sub(now).Seconds())
		_, err := repo.Create(context.Background(), &timeentry.TimeEntry{
			TaskID:    taskID,
			AgentID:   agentID,
			StartedAt: now.Add(time.Duration(i) * time.Hour),
			EndedAt:   &ended,
			DurationS: &dur,
		})
		require.NoError(t, err)
	}

	// Range covers only the first entry.
	from := now.Add(-time.Hour)
	to := now.Add(30 * time.Minute)
	list, err := repo.ListByAgent(context.Background(), agentID, from, to)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestTimeEntryRepo_ListByDay(t *testing.T) {
	db, taskID, agentID := setupTimeEntryDB(t)
	repo := NewTimeEntryRepository(db)
	now := time.Now().Truncate(time.Second)

	ended := now.Add(30 * time.Minute)
	dur := int64(ended.Sub(now).Seconds())
	_, err := repo.Create(context.Background(), &timeentry.TimeEntry{
		TaskID: taskID, AgentID: agentID, StartedAt: now, EndedAt: &ended, DurationS: &dur,
	})
	require.NoError(t, err)

	list, err := repo.ListByDay(context.Background(), agentID, now)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}
