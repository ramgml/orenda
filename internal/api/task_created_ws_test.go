package api_test

// T192: user-side task creation must publish a task.created WS event
// so `orenda agent next --await` long-polls wake immediately and open
// kanban boards refetch. Covers the three user-side create paths:
//
//   - POST /api/v1/projects/{id}/tasks (createTaskHandler)
//   - POST /api/v1/inbox/tasks         (createInboxTaskHandler)
//   - POST /api/v1/sync op=create_task (applySyncCreateTask, PWA outbox)
//
// The agent-propose path already published (with an "actor" field) and
// is covered by TestAgent_ProposeTask_Created; these tests additionally
// pin that user-side events carry NO "actor".

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// wsCreateFixture is columnDeps with the hub exposed (for Subscribe)
// and the sync-ops store wired (so the same router serves POST
// /api/v1/sync). Local to this file — columnDeps deliberately keeps
// its hub private and the P3/P8 builders don't hand the hub out.
type wsCreateFixture struct {
	router    http.Handler
	cookie    string
	projectID string
	cols      []*project.Column
	hub       ws.Hub
}

func wsCreateDeps(t *testing.T) wsCreateFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	u := &user.User{Email: "wscreate@x.com", PasswordHash: mustHashFast(t), DisplayName: "W"}
	require.NoError(t, users.Create(context.Background(), u))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	repo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    projRepo,
		Tasks:       repo,
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		SyncOps:     sqlite.NewSyncOpsRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	body, _ := json.Marshal(map[string]string{"email": "wscreate@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	p, _, _, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "WS Create", OwnerID: u.ID, Color: "#3b82f6",
	})
	require.NoError(t, err)
	_, cols, err := projRepo.GetBoard(context.Background(), p.ID)
	require.NoError(t, err)
	return wsCreateFixture{
		router:    router,
		cookie:    cookie,
		projectID: p.ID,
		cols:      cols,
		hub:       hub,
	}
}

// recvTaskCreated reads one event from ch and pins the shared contract:
// task.created on the `tasks` topic, body["task"] is the created
// *task.Task, and — user-side creates only — no "actor" field.
func recvTaskCreated(t *testing.T, ch <-chan ws.Event) *task.Task {
	t.Helper()
	select {
	case ev := <-ch:
		require.Equal(t, "tasks", ev.Topic)
		body, ok := ev.Body.(map[string]any)
		require.True(t, ok, "event body must be a map, got %T", ev.Body)
		assert.Equal(t, "task.created", body["type"])
		assert.NotContains(t, body, "actor",
			"user-side creates carry no actor (only agent propose does)")
		tr, ok := body["task"].(*task.Task)
		require.True(t, ok, "body[\"task\"] must be *task.Task, got %T", body["task"])
		require.NotEmpty(t, tr.ID)
		return tr
	case <-time.After(2 * time.Second):
		t.Fatal("no task.created event on the tasks topic")
		return nil
	}
}

// assertNoEvent fails the test if any event arrives within 500ms —
// the exactly-once guard after the create (and after an idempotent
// sync replay).
func assertNoEvent(t *testing.T, ch <-chan ws.Event) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected extra event: %+v", ev)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestCreateTask_PublishesWSCreated(t *testing.T) {
	t.Parallel()
	f := wsCreateDeps(t)

	// Subscribe BEFORE the POST so the event cannot slip past.
	ch, unsub := f.hub.Subscribe("owner", "tasks")
	defer unsub()

	rr := doReq(f.router, http.MethodPost, "/api/v1/projects/"+f.projectID+"/tasks", f.cookie,
		map[string]any{"title": "board task", "column_id": f.cols[0].ID})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	got := recvTaskCreated(t, ch)
	assert.Equal(t, created.ID, got.ID, "event carries the created task")
	assert.Equal(t, f.projectID, got.ProjectID)
	assertNoEvent(t, ch) // exactly-once: no duplicate broadcast
}

func TestCreateInboxTask_PublishesWSCreated(t *testing.T) {
	t.Parallel()
	f := wsCreateDeps(t)

	ch, unsub := f.hub.Subscribe("owner", "tasks")
	defer unsub()

	rr := doReq(f.router, http.MethodPost, "/api/v1/inbox/tasks", f.cookie,
		map[string]any{"title": "quick capture"})
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var created task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))
	assert.Empty(t, created.ProjectID, "inbox task has no project")

	got := recvTaskCreated(t, ch)
	assert.Equal(t, created.ID, got.ID, "event carries the created task")
	assert.Empty(t, got.ProjectID)
	assertNoEvent(t, ch)
}

func TestSyncCreateTask_PublishesWSCreated(t *testing.T) {
	t.Parallel()
	f := wsCreateDeps(t)

	ch, unsub := f.hub.Subscribe("owner", "tasks")
	defer unsub()

	op := map[string]any{
		"op":         "create_task",
		"target":     f.projectID,
		"payload":    map[string]any{"title": "offline task", "column_id": f.cols[0].ID},
		"client_id":  "c-ws-create-1",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	rr := p8PostSync(t, f.router, f.cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	results := parseSyncResults(t, rr)
	require.Len(t, results, 1)
	require.True(t, results[0].OK, "err=%s", results[0].Error)

	got := recvTaskCreated(t, ch)
	assert.Equal(t, results[0].ID, got.ID, "event carries the created task")

	// Idempotent replay of the same client_id: the op short-circuits
	// (same server id) and must NOT re-publish.
	rr2 := p8PostSync(t, f.router, f.cookie, []map[string]any{op})
	require.Equal(t, http.StatusOK, rr2.Code)
	results2 := parseSyncResults(t, rr2)
	require.Len(t, results2, 1)
	assert.True(t, results2[0].OK)
	assert.Equal(t, results[0].ID, results2[0].ID, "replay returns the same id")
	assertNoEvent(t, ch) // exactly one event total across create + replay
}
