package task_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
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

func (h *recordingHub) Publish(_ context.Context, ev ws.Event) {
	h.events = append(h.events, recordedEvent{topic: ev.Topic, body: ev.Body})
}

// Close implements ws.Hub (Phase 22.3).
func (h *recordingHub) Close() {}

func (h *recordingHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 4)
	return ch, func() { close(ch) }
}

type recordingRecorder struct {
	calls []string
}

func (r *recordingRecorder) Record(_ context.Context, taskID string, _ activity.ActorType, actorID string, action activity.Action, payload string) error {
	// Task 117: actorID is captured so tests can pin the audit
	// actor (Activity.Validate rejects empty actor ids in prod).
	r.calls = append(r.calls, taskID+":"+actorID+":"+string(action)+":"+payload)
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
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})

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
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})
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
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})

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
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, &recordingHub{})

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
	svc := taskservice.New(repo, nil, &recordingRecorder{}, nil, hub)

	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	_, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: cols[1].ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, hub.events)
	assert.Equal(t, "tasks", hub.events[0].topic)
}

// Phase 33.1: moving an awaiting=human card off the review queue
// (agent-proposed backlog task accepted onto the board, or a review
// card dragged elsewhere) clears awaiting — the triage happened.
func TestService_Move_ClearsAwaitingHuman(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewProjectRepository(db)
	tasks := sqlite.NewTaskRepository(db)
	svc := taskservice.New(tasks, nil, &recordingRecorder{}, nil, &recordingHub{})
	svc.Columns = repo

	// Default column order matches AllStatuses: backlog, todo,
	// in_progress, review, done.
	backlog, todo, review := cols[0], cols[1], cols[3]

	// backlog(awaiting=human) → todo: accepted, awaiting cleared.
	proposed := &task.Task{
		ProjectID: p.ID, ColumnID: backlog.ID, Title: "proposed",
		Status: task.StatusBacklog, Awaiting: task.AwaitingHuman,
	}
	require.NoError(t, tasks.Create(context.Background(), proposed))
	moved, err := svc.Move(context.Background(), proposed.ID, taskservice.MoveOptions{
		TargetColumnID: todo.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, task.StatusTodo, moved.Status)
	assert.Equal(t, task.AwaitingNone, moved.Awaiting)
	// Persisted, not just in-memory.
	back, err := tasks.GetByID(context.Background(), proposed.ID)
	require.NoError(t, err)
	assert.Equal(t, task.AwaitingNone, back.Awaiting)

	// review(awaiting=human) → review column (a no-op drag): awaiting
	// survives — the card is still waiting on the human.
	inReview := &task.Task{
		ProjectID: p.ID, ColumnID: review.ID, Title: "in review",
		Status: task.StatusReview, Awaiting: task.AwaitingHuman,
	}
	require.NoError(t, tasks.Create(context.Background(), inReview))
	moved, err = svc.Move(context.Background(), inReview.ID, taskservice.MoveOptions{
		TargetColumnID: review.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, task.AwaitingHuman, moved.Awaiting)

	// awaiting=agent is the agent's problem, not the human's — a move
	// never clears it.
	agentsTurn := &task.Task{
		ProjectID: p.ID, ColumnID: backlog.ID, Title: "agent turn",
		Status: task.StatusBacklog, Awaiting: task.AwaitingAgent,
	}
	require.NoError(t, tasks.Create(context.Background(), agentsTurn))
	moved, err = svc.Move(context.Background(), agentsTurn.ID, taskservice.MoveOptions{
		TargetColumnID: todo.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, task.AwaitingAgent, moved.Awaiting)
}

// Task 117: task.moved activity payload carries the target column's
// NAME (snapshotted at event time) alongside the column id, so the
// feed renders "→ In Review" instead of a raw UUID. The payload is
// assembled with encoding/json (never Sprintf) so column names with
// quotes / unicode survive as valid JSON.
func TestService_Move_RecordsColumnNameInPayload(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	rec := &recordingRecorder{}
	svc := taskservice.New(repo, nil, rec, nil, &recordingHub{})
	svc.Columns = projRepo

	require.GreaterOrEqual(t, len(cols), 2)
	backlog, todo := cols[0], cols[1]

	tr := &task.Task{ProjectID: p.ID, ColumnID: backlog.ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	moved, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: todo.ID,
		ActorID:        "user-117",
	})
	require.NoError(t, err)
	require.NotEmpty(t, rec.calls)

	// call format is "taskID:actorID:action:payload" — split the
	// payload off; the actor must be the MoveOptions.ActorID the
	// handler passes from the session (Activity.Validate requires
	// a non-empty actor id in production).
	parts := strings.SplitN(rec.calls[0], ":", 4)
	require.Len(t, parts, 4)
	assert.Equal(t, tr.ID, parts[0])
	assert.Equal(t, "user-117", parts[1])
	assert.Equal(t, string(activity.ActionMoved), parts[2])

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(parts[3]), &payload))
	assert.Equal(t, todo.ID, payload["column_id"])
	assert.Equal(t, todo.Name, payload["column_name"])
	assert.InDelta(t, moved.Position, payload["position"].(float64), 1e-9)
}

// Task 117: a column name containing quotes / unicode must round-trip
// through the payload as valid JSON (the old Sprintf-built payload
// would have produced malformed JSON for embedded quotes).
func TestService_Move_PayloadSurvivesSpecialCharactersInColumnName(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	rec := &recordingRecorder{}
	svc := taskservice.New(repo, nil, rec, nil, &recordingHub{})
	svc.Columns = projRepo

	special, err := projRepo.CreateColumn(context.Background(), p.ID, &project.Column{
		Name: `Кто "здесь"? 🚚`,
	})
	require.NoError(t, err)

	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	_, err = svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: special.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, rec.calls)

	parts := strings.SplitN(rec.calls[0], ":", 4)
	require.Len(t, parts, 4)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(parts[3]), &payload))
	assert.Equal(t, `Кто "здесь"? 🚚`, payload["column_name"])
}

// Task 117: when the Columns repo isn't wired (or the lookup fails)
// the payload keeps the legacy column_id-only shape — no column_name
// key at all. Legacy rows in the feed fall back to rendering the
// column UUID, so this is also the honest regression test for the
// frontend's fallback path.
func TestService_Move_PayloadOmitsColumnNameWhenLookupFails(t *testing.T) {
	db := setupMoveDB(t)
	p, cols := setupMoveProject(t, db)
	repo := sqlite.NewTaskRepository(db)
	// No svc.Columns wiring → the GetColumn lookup is skipped.
	rec := &recordingRecorder{}
	svc := taskservice.New(repo, nil, rec, nil, &recordingHub{})

	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, repo.Create(context.Background(), tr))

	_, err := svc.Move(context.Background(), tr.ID, taskservice.MoveOptions{
		TargetColumnID: cols[1].ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, rec.calls)

	parts := strings.SplitN(rec.calls[0], ":", 4)
	require.Len(t, parts, 4)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(parts[3]), &payload))
	_, has := payload["column_name"]
	assert.False(t, has, "column_name must be absent when the column lookup failed")
	assert.Equal(t, cols[1].ID, payload["column_id"])
}

func TestNullHub(t *testing.T) {
	hub := taskservice.NullHub()
	hub.Publish(context.Background(), ws.Event{Topic: "x", Body: 1}) // must not panic
	ch, unsub := hub.Subscribe("u", "x")
	_ = ch
	unsub()
}
