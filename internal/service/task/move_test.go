package task_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type recordingHub struct {
	events []recordedEvent
}

type recordedEvent struct {
	topic string
	body  any
}

func (h *recordingHub) Publish(_ context.Context, topic string, body any) {
	h.events = append(h.events, recordedEvent{topic: topic, body: body})
}

type recordingRecorder struct {
	calls []string
}

func (r *recordingRecorder) Record(_ context.Context, taskID, action, payload string) error {
	r.calls = append(r.calls, taskID+":"+action+":"+payload)
	return nil
}

func setupMoveDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "move.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	return db
}

func setupMoveProject(t *testing.T, db *sql.DB) (*project.Project, []*project.Column) {
	t.Helper()
	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "mv-" + t.Name() + "-" + randomSuffix() + "@x.com",
		PasswordHash: "x",
		DisplayName:  "O",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	repo := sqlite.NewProjectRepository(db)
	p, _, cols, err := repo.CreateProject(context.Background(), &project.Project{
		Name: "Orenda", OwnerID: owner.ID,
	})
	require.NoError(t, err)
	return p, cols
}

// randomSuffix avoids UUIDv7 timestamp collision across fast tests.
func randomSuffix() string {
	return newUUIDLite()[:8]
}

func newUUIDLite() string {
	// Inline copy to avoid importing google/uuid here.
	const hex = "0123456789abcdef"
	b := make([]byte, 36)
	b[8] = '-'
	b[13] = '-'
	b[18] = '-'
	b[23] = '-'
	x := uint64(0xdeadbeef) ^ uint64(uintptr(0))
	for i := 0; i < 36; i++ {
		if b[i] == '-' {
			continue
		}
		b[i] = hex[x&0xf]
		x >>= 4
	}
	return string(b)
}

func TestService_Move_BetweenColumns(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	svc := taskservice.New(repo, &recordingRecorder{}, &recordingHub{})

	require.GreaterOrEqual(t, len(cols), 2)
	backlog := cols[0]
	todo := cols[1]

	tr := &task.Task{ProjectID: p.ID, ColumnID: backlog.ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	moved, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: todo.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, todo.ID, moved.ColumnID)
}

func TestService_Move_BetweenNeighborsDerivesFractionalPosition(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	svc := taskservice.New(repo, &recordingRecorder{}, &recordingHub{})
	col := cols[0]

	a := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "a", Position: 1024}
	b := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "b", Position: 2048}
	require.NoError(t, repo.Create(context.Background(), a))
	require.NoError(t, repo.Create(context.Background(), b))

	inserted := &task.Task{ProjectID: p.ID, ColumnID: col.ID, Title: "middle"}
	require.NoError(t, repo.Create(context.Background(), inserted))

	moved, err := svc.Move(context.Background(), inserted.ID, taskservice.MoveOptions{
		TargetColumnID: col.ID,
		Before:         b,
		After:          a,
	})
	require.NoError(t, err)
	assert.InDelta(t, (a.Position+b.Position)/2, moved.Position, 1e-9)
}

func TestService_Move_NotFound(t *testing.T) {
	db := setupMoveDB(t)
	_, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	svc := taskservice.New(repo, &recordingRecorder{}, &recordingHub{})

	_, err := svc.Move(context.Background(), "no-such", taskservice.MoveOptions{
		TargetColumnID: cols[0].ID,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, taskservice.ErrNotFound)
}

func TestService_Move_InvalidInput(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	svc := taskservice.New(repo, &recordingRecorder{}, &recordingHub{})

	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	_, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: "",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, taskservice.ErrInvalidInput)
}

func TestService_Move_PublishesHubEvent(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	hub := &recordingHub{}
	svc := taskservice.New(repo, &recordingRecorder{}, hub)

	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	_, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: cols[1].ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, hub.events)
	assert.Equal(t, "tasks", hub.events[0].topic)
}

func TestNullHub(t *testing.T) {
	hub := taskservice.NullHub()
	hub.Publish(context.Background(), "x", 1) // must not panic
}
