package api_test

// Phase 33.1 + 33.3: agent-side task creation — POST /api/v1/agent/tasks.
//
// The DOGFOOD rule "new work = a task in the instance" was not
// executable by agents (the user-side create endpoint sits under
// RequireUser). These tests pin the agent-namespace twin:
//
//   - the proposed task lands as status=backlog + awaiting=none,
//     visible only on the kanban backlog column (NOT in the review
//     queue, which is reserved for agent-submitted work),
//   - the proposed task is NOT claimable from GET /api/v1/agent/tasks
//     (the listing filters to Status=todo),
//   - after the owner drags the card out of backlog (kanban move),
//     the task shows up in GET /api/v1/agent/tasks?ready=true.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	activitysvc "github.com/ramgml/orenda/internal/service/activity"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// proposeFixture bundles a router + agent token + owner cookie + a
// seeded project (with its default columns resolved by status).
type proposeFixture struct {
	router       http.Handler
	agentID      string
	token        string
	cookie       []*http.Cookie
	db           *sqlLite
	hub          ws.Hub
	projectID    string
	backlogColID string
	todoColID    string
}

func newProposeFixture(t *testing.T) *proposeFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/p.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	ownerEmail := "propose-owner-" + randLite()[:8] + "@x.com"
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        ownerEmail,
		PasswordHash: mustHashFast(t),
		DisplayName:  "Owner",
	}))
	var ownerID string
	require.NoError(t, db.QueryRow("SELECT id FROM users WHERE email = ?", ownerEmail).Scan(&ownerID))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	projects := sqlite.NewProjectRepository(db)
	tasks := sqlite.NewTaskRepository(db)
	activityRepo := sqlite.NewActivityRepository(db)
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	// taskservice.Recorder adapter — the service expects the
	// internal/service/task.Recorder interface (Record with
	// actorType), while the activity service exposes RecordTask.
	// Wrap so the service's audit path (used by manage.go's
	// task.updated / task.deleted rows) actually writes.
	taskRecorder := activityRecorderAdapter{repo: activityRepo}
	tombstoneRecorder := sqlite.NewTaskRetractedRepository(db)
	taskSvc := taskservice.NewWithTombstone(tasks, sqlite.NewTaskLockRepository(db), taskRecorder, tombstoneRecorder, nil, hub)
	taskSvc.Columns = projects

	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	tm := &agentFixtureTMinter{tokens: tokens}
	agentSvc := agentservice.New(agents, users, tm, hub, nil)
	got, err := agentSvc.Register(context.Background(), "proposer-test", []string{"test"}, "test", nil)
	require.NoError(t, err)

	deps := api.Dependencies{
		Logger:           zap.NewNop(),
		Signer:           signer,
		Users:            users,
		Projects:         projects,
		Tasks:            tasks,
		Tokens:           tokens,
		TaskService:      taskSvc,
		Agents:           agents,
		Comments:         commentSvc,
		Activities:       activityRepo,
		ActivityRecorder: activitysvc.New(activityRepo),
		WSHub:            hub,
		CookieName:       "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	// Owner cookie via the real login endpoint.
	loginBody, _ := json.Marshal(map[string]string{"email": ownerEmail, "password": "hunter2!"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	require.Equal(t, http.StatusOK, loginRR.Code, "login body=%s", loginRR.Body.String())

	// Seed a project; resolve the backlog / todo columns by status.
	p, _, _, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "Propose", OwnerID: ownerID,
	})
	require.NoError(t, err)
	var backlogCol, todoCol string
	require.NoError(t, db.QueryRow(
		"SELECT c.id FROM columns c JOIN boards b ON b.id = c.board_id WHERE b.project_id = ? AND c.status = 'backlog'",
		p.ID).Scan(&backlogCol))
	require.NoError(t, db.QueryRow(
		"SELECT c.id FROM columns c JOIN boards b ON b.id = c.board_id WHERE b.project_id = ? AND c.status = 'todo'",
		p.ID).Scan(&todoCol))

	return &proposeFixture{
		router:       router,
		agentID:      got.Agent.ID,
		token:        got.PlainToken,
		cookie:       loginRR.Result().Cookies(),
		db:           db,
		hub:          hub,
		projectID:    p.ID,
		backlogColID: backlogCol,
		todoColID:    todoCol,
	}
}

// proposeAsAgent posts a propose body under the agent bearer token.
func (f *proposeFixture) proposeAsAgent(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func (f *proposeFixture) doWithCookie(t *testing.T, method, path string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	for _, c := range f.cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func validProposeBody(projectID string) map[string]any {
	return map[string]any{
		"project_id":     projectID,
		"title":          "Write the phase-33.1 report",
		"description_md": "# Why\n\nThe agent could not create tasks.",
	}
}

func TestAgent_ProposeTask_Created(t *testing.T) {
	f := newProposeFixture(t)

	// WS: a subscriber on the `tasks` topic must see task.created.
	ch, unsub := f.hub.Subscribe("owner", "tasks")
	defer unsub()

	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

	var got task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, task.StatusBacklog, got.Status)
	assert.Equal(t, task.AwaitingNone, got.Awaiting,
		"propose must land with awaiting=none (Phase 33.3: review queue is for submit'нутой работы, not backlog)")
	assert.Equal(t, f.projectID, got.ProjectID)
	assert.Equal(t, f.backlogColID, got.ColumnID, "task should land on the backlog column")
	assert.Equal(t, task.PriorityMedium, got.Priority, "priority defaults to medium")

	// Activity audit: task.created with actor_type=agent.
	rows, err := sqlite.NewActivityRepository(f.db).ListByTask(context.Background(), got.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1, "exactly one activity row (task.created) expected, got %+v", rows)
	assert.Equal(t, "agent", string(rows[0].ActorType))
	assert.Equal(t, f.agentID, rows[0].ActorID)
	assert.Equal(t, "task.created", string(rows[0].Action))

	// WS event on topic `tasks`.
	select {
	case ev := <-ch:
		body, ok := ev.Body.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "task.created", body["type"])
		assert.Equal(t, f.agentID, body["actor"])
	case <-time.After(2 * time.Second):
		t.Fatal("no task.created event on the tasks topic")
	}
}

// TestAgent_ProposeTask_PRefResolution verifies that the agent
// propose endpoint accepts a P-prefixed project reference ("P1")
// in the project_id body field and resolves it to the correct
// project. This is the primary agent-facing path for P-refs.
func TestAgent_ProposeTask_PRefResolution(t *testing.T) {
	f := newProposeFixture(t)

	// The fixture creates one project; after migration 036 it gets
	// number 1. Propose with "P1" instead of the UUID.
	rr := f.proposeAsAgent(t, validProposeBody("P1"))
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

	var got task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, f.projectID, got.ProjectID,
		"P1 should resolve to the same project as the UUID")
}

func TestAgent_ProposeTask_OptionalFields(t *testing.T) {
	f := newProposeFixture(t)

	// Seed a parent task and a blocker task.
	parent := &task.Task{ProjectID: f.projectID, ColumnID: f.todoColID, Title: "parent"}
	require.NoError(t, sqlite.NewTaskRepository(f.db).Create(context.Background(), parent))
	blocker := &task.Task{ProjectID: f.projectID, ColumnID: f.todoColID, Title: "blocker"}
	require.NoError(t, sqlite.NewTaskRepository(f.db).Create(context.Background(), blocker))

	body := validProposeBody(f.projectID)
	body["priority"] = "high"
	body["parent_task_id"] = parent.ID
	body["blocked_by"] = []string{blocker.ID}
	rr := f.proposeAsAgent(t, body)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

	var got task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, task.PriorityHigh, got.Priority)
	assert.Equal(t, parent.ID, got.ParentTaskID)

	// The blocker edge must be real: the task is NOT in the ready list.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks?ready=true", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	listRR := httptest.NewRecorder()
	f.router.ServeHTTP(listRR, req)
	require.Equal(t, http.StatusOK, listRR.Code)
	assert.NotContains(t, listRR.Body.String(), got.ID,
		"blocked task must not appear in ?ready=true")

	// Unknown parent / blocker / bad priority → clean 4xx, not a 500.
	for _, tc := range []struct {
		name string
		mut  func(map[string]any)
		want int
	}{
		{"unknown parent", func(b map[string]any) { b["parent_task_id"] = "no-such" }, http.StatusNotFound},
		{"unknown blocker", func(b map[string]any) { b["blocked_by"] = []string{"no-such"} }, http.StatusNotFound},
		{"bad priority", func(b map[string]any) { b["priority"] = "sideways" }, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := validProposeBody(f.projectID)
			tc.mut(b)
			rr := f.proposeAsAgent(t, b)
			assert.Equal(t, tc.want, rr.Code, "body=%s", rr.Body.String())
		})
	}
}

func TestAgent_ProposeTask_Validation(t *testing.T) {
	f := newProposeFixture(t)
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing project_id", map[string]any{"title": "t", "description_md": "d"}},
		{"missing title", map[string]any{"project_id": f.projectID, "description_md": "d"}},
		{"missing description_md", map[string]any{"project_id": f.projectID, "title": "t"}},
		{"blank description_md", map[string]any{"project_id": f.projectID, "title": "t", "description_md": "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.proposeAsAgent(t, tc.body)
			assert.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())
		})
	}
}

func TestAgent_ProposeTask_UnknownProject(t *testing.T) {
	f := newProposeFixture(t)
	rr := f.proposeAsAgent(t, validProposeBody("no-such-project"))
	assert.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
}

// The agent namespace rejects both anonymous callers and user-cookie
// sessions (namespaces are split — mirrors TestAgent_CommentRejectsUserCookie).
func TestAgent_ProposeTask_Auth(t *testing.T) {
	f := newProposeFixture(t)
	raw, _ := json.Marshal(validProposeBody(f.projectID))

	// No token → 401.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// User cookie on the agent namespace → 401.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks", bytes.NewReader(raw))
	for _, c := range f.cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code, "body=%s", rr.Body.String())
}

// The full triage loop: propose → owner sees it ONLY on the backlog
// column (NOT in the review queue, NOT in the agent list) → owner
// drags the card to todo → task becomes claimable via
// GET /api/v1/agent/tasks?ready=true.
//
// Phase 33.3: the propose handler no longer stamps awaiting=human.
// The review queue is reserved for agent-submitted work
// (status=review); backlog triage happens on the board. The agent
// does not see the task on /api/v1/agent/tasks until the owner has
// moved it into a claimable column.
func TestAgent_ProposeTask_BoardTriageFlow(t *testing.T) {
	f := newProposeFixture(t)

	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var created task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))
	assert.Equal(t, task.StatusBacklog, created.Status)
	assert.Equal(t, task.AwaitingNone, created.Awaiting,
		"propose must land awaiting=none; review queue is for submit'нутой работы")

	// 1. NOT in the review queue (proposed backlog tasks are triaged on the board).
	rr = f.doWithCookie(t, http.MethodGet, "/api/v1/review-queue", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), created.ID,
		"proposed task must NOT appear in /review-queue; body=%s", rr.Body.String())

	// 2. NOT in the agent list (with or without ?ready=true).
	for _, q := range []string{"", "?ready=true"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks"+q, nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rr = httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		assert.NotContains(t, rr.Body.String(), created.ID,
			"proposed task must NOT appear in /api/v1/agent/tasks%s; body=%s", q, rr.Body.String())
	}

	// 3. Visible on the kanban backlog (the owner triages there).
	rr = f.doWithCookie(t, http.MethodGet, "/api/v1/projects/"+f.projectID+"/tasks?status=backlog", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), created.ID,
		"proposed task must be visible on the kanban backlog; body=%s", rr.Body.String())

	// Accept = kanban move backlog → todo (existing user endpoint).
	rr = f.doWithCookie(t, http.MethodPost, "/api/v1/tasks/"+created.ID+"/move",
		map[string]any{"column_id": f.todoColID})
	require.Equal(t, http.StatusOK, rr.Code, "move body=%s", rr.Body.String())
	var moved task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &moved))
	assert.Equal(t, task.StatusTodo, moved.Status)
	assert.Equal(t, task.AwaitingNone, moved.Awaiting, "move to todo must keep awaiting=none")

	// 4. Now claimable by agents.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks?ready=true", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	readyRR := httptest.NewRecorder()
	f.router.ServeHTTP(readyRR, req)
	require.Equal(t, http.StatusOK, readyRR.Code)
	assert.Contains(t, readyRR.Body.String(), created.ID,
		"triaged task must appear in ?ready=true; body=%s", readyRR.Body.String())
}

// Phase 33.3: pin the invariant that a backlog task is NEVER
// claimable by an agent, even when no other ready work exists. The
// owner must drag it out of backlog first. Without this guarantee
// agents would race for half-formed propose-tasks and the review
// surface would re-appear under a different name.
func TestAgent_ProposeTask_BacklogNotInAgentList(t *testing.T) {
	f := newProposeFixture(t)

	// Seed another ready-to-do task so the agent list is not empty —
	// the assertion below must hold even with other tasks present.
	other := &task.Task{ProjectID: f.projectID, ColumnID: f.todoColID, Title: "other"}
	require.NoError(t, sqlite.NewTaskRepository(f.db).Create(context.Background(), other))

	rr := f.proposeAsAgent(t, validProposeBody(f.projectID))
	require.Equal(t, http.StatusCreated, rr.Code)
	var created task.Task
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	// Agent list — both ready=true and full — must show the todo
	// task but NOT the backlog proposal.
	for _, q := range []string{"", "?ready=true"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/tasks"+q, nil)
		req.Header.Set("Authorization", "Bearer "+f.token)
		rr = httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		body := rr.Body.String()
		assert.Contains(t, body, other.ID, "ready todo task must be in /agent/tasks%s", q)
		assert.NotContains(t, body, created.ID,
			"backlog proposal must NOT be in /agent/tasks%s (invariant: agent does not pull from backlog)", q)
	}
}
