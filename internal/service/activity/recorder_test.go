package activity_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	activitysvc "github.com/ramgml/orenda/internal/service/activity"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type fixture struct {
	db      *sql.DB
	taskID  string
	ownerID string
	actRepo activity.Repository
}

func setupActivityTest(t *testing.T) (*activitysvc.Recorder, *fixture) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "a.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "act-" + t.Name() + "-" + newIDLite()[:8] + "@x.com",
		PasswordHash: "x",
		DisplayName:  "Owner",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	projRepo := sqlite.NewProjectRepository(db)
	p, _, cols, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Orenda", OwnerID: owner.ID,
	})
	require.NoError(t, err)

	tasks := sqlite.NewTaskRepository(db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	repo := sqlite.NewActivityRepository(db)
	r := activitysvc.New(repo)
	return r, &fixture{
		db:      db,
		taskID:  tr.ID,
		ownerID: owner.ID,
		actRepo: repo,
	}
}

// newIDLite returns a UUID-shaped random string (no real UUIDv7).
// Defined here so the test file doesn't need google/uuid.
func newIDLite() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 36)
	for i := range b {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			b[i] = '-'
			continue
		}
		b[i] = hex[(i*7)%16]
	}
	return string(b)
}

func TestRecorder_RecordTask(t *testing.T) {
	r, fx := setupActivityTest(t)

	err := r.RecordTask(context.Background(), fx.taskID, activity.ActorUser, fx.ownerID, activity.ActionMoved, `{"x":1}`)
	require.NoError(t, err)

	list, err := fx.actRepo.ListByTask(context.Background(), fx.taskID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, activity.ActionMoved, list[0].Action)
	assert.Equal(t, activity.ActorUser, list[0].ActorType)
	assert.Equal(t, fx.ownerID, list[0].ActorID)
}

func TestRecorder_RecordTask_Invalid(t *testing.T) {
	dir := t.TempDir()
	db, _ := sqlite.Open(context.Background(), filepath.Join(dir, "i.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	defer db.Close()
	_ = sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations")

	r := activitysvc.New(sqlite.NewActivityRepository(db))
	err := r.RecordTask(context.Background(), "", activity.ActorUser, "u", activity.ActionMoved, "")
	require.Error(t, err)
}
