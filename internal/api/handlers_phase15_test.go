package api_test

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
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// phase15Fixture is the test rig for Phase 15 contract changes.
//
// What it owns:
//   - A single-owner user (the agent's "boss")
//   - Two agents: holderAgent and rivalAgent — both registered through
//     agentservice.Register so they get real ids + plain tokens
//   - The full API surface (cookie auth + agent bearer auth) wired
//     against a fresh SQLite DB
//   - Phase 15 TaskLockHolder wired to the same DB the Service
//     uses — so 409 lock_taken and /context lookups resolve
//     against the real task_locks table.
//
// Why a new fixture instead of patching agentFixture: agentFixture
// doesn't currently wire TaskLockHolder — Phase 15 needs it for
// every test in this file, but other tests in agent_handlers_test.go
// don't care about the lock lookup. Adding it conditionally there
// would be a footgun; this fixture is small and self-contained.
type phase15Fixture struct {
	router http.Handler

	ownerEmail    string
	holderToken   string
	holderAgentID string
	rivalToken    string
	rivalAgentID  string

	db       *sqlLite
	tasks    task.Repository
	projects project.Repository
	agents   agent.Repository
}

func newPhase15Fixture(t *testing.T) *phase15Fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "p15.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	ownerEmail := "p15-" + randLite()[:8] + "@x.com"
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        ownerEmail,
		PasswordHash: mustHashFast(t),
		DisplayName:  "Owner",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")

	taskRepo := sqlite.NewTaskRepository(db)
	projRepo := sqlite.NewProjectRepository(db)
	agentRepo := sqlite.NewAgentRepository(db)
	lockRepo := sqlite.NewTaskLockRepository(db)

	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	// taskservice.New wants the interface types Locks /
	// Recorder / CommentAdder / Hub. Phase 15 tests don't
	// assert activity or comments so nil for Recorder /
	// CommentAdder is fine. The lock repo is constructed twice
	// (once for taskservice, once for deps.TaskLockHolder) —
	// both wrap the same *sql.DB so they read each other's rows.
	taskSvc := taskservice.New(taskRepo, lockRepo, nil, nil, hub)

	tokens := sqlite.NewAPITokenRepository(db)
	// Adapter for agentservice.TokenMinter.
	tm := &agentFixtureTMinter{tokens: tokens}
	agentSvc := agentservice.New(agentRepo, users, tm, hub, nil)

	// Two real agents — the fixture user gets one row each via the
	// agent registration flow so we have valid bcrypt-hashed tokens
	// the API will accept on the agent namespace.
	holderReg, err := agentSvc.Register(context.Background(),
		"p15-holder-"+randLite()[:6], []string{"qwen"}, "holder", nil)
	require.NoError(t, err)
	rivalReg, err := agentSvc.Register(context.Background(),
		"p15-rival-"+randLite()[:6], []string{"qwen"}, "rival", nil)
	require.NoError(t, err)

	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    projRepo,
		Tasks:       taskRepo,
		Tokens:      tokens,
		TaskService: taskSvc,
		Agents:      agentRepo,
		Comments:    commentSvc,
		Activities:  sqlite.NewActivityRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
		// Phase 15: the same *sqlite.taskLockRepo is the Tasks
		// Locks repo and the deps.TaskLockHolder — both
		// interfaces are satisfied by the same pointer, so a
		// Claim writes a row that deps.TaskLockHolder.Holder()
		// can read.
		TaskLockHolder: lockRepo,
	}

	return &phase15Fixture{
		router:        api.NewRouter(&deps),
		ownerEmail:    ownerEmail,
		holderToken:   holderReg.PlainToken,
		holderAgentID: holderReg.Agent.ID,
		rivalToken:    rivalReg.PlainToken,
		rivalAgentID:  rivalReg.Agent.ID,
		db:            db,
		tasks:         taskRepo,
		projects:      projRepo,
		agents:        agentRepo,
	}
}

// loginOwner fetches the user cookie via /api/v1/auth/login. The
// cookie is needed for tests that hit endpoints under RequireUser
// (e.g. GET /api/v1/tasks/:id/context). Tests use a fresh request
// per call so the cookie isn't stored on the fixture — each call
// returns a fresh cookie value.
func (f *phase15Fixture) loginOwner(t *testing.T) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"email":    f.ownerEmail,
		"password": "hunter2!",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "login: body=%s", rr.Body.String())
	for _, c := range rr.Result().Cookies() {
		if c.Name == "orenda_session" {
			return c.Value
		}
	}
	t.Fatal("login response did not include orenda_session cookie")
	return ""
}

// seedTask creates a fresh task in a fresh project so tests don't
// fight over IDs. Returns the task id.
func (f *phase15Fixture) seedTask(t *testing.T) (taskID string) {
	t.Helper()
	row := f.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	p, _, cols, err := f.projects.CreateProject(context.Background(), &project.Project{
		Name: "P15", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "p15-task"}
	require.NoError(t, f.tasks.Create(context.Background(), tr))
	return tr.ID
}

// seedDependency wires a blocker relationship between two tasks
// in the same project. The blocker stays in `todo` so it's still
// "unfinished" when the test reads back BlockedBy.
func (f *phase15Fixture) seedDependency(t *testing.T, dependentID, blockerID string) {
	t.Helper()
	row := f.db.QueryRow("SELECT project_id FROM tasks WHERE id = ?", dependentID)
	var projectID string
	require.NoError(t, row.Scan(&projectID))
	_, err := f.db.ExecContext(context.Background(),
		`INSERT INTO task_dependencies (task_id, depends_on_task_id) VALUES (?, ?)`,
		dependentID, blockerID)
	require.NoError(t, err)
	_ = projectID
}

func (f *phase15Fixture) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// ---- Phase 15.2: 409 lock_taken surfaces the holder ----

func TestPhase15_LockTaken_IncludesHolderAgentIDAndName(t *testing.T) {
	f := newPhase15Fixture(t)
	taskID := f.seedTask(t)

	// Holder claims first.
	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+taskID+"/claim", f.holderToken, map[string]string{})
	require.Equal(t, http.StatusOK, rr.Code, "first claim: body=%s", rr.Body.String())

	// Rival tries to claim the same task.
	rr = f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+taskID+"/claim", f.rivalToken, map[string]string{})
	require.Equal(t, http.StatusConflict, rr.Code, "rival claim: body=%s", rr.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "lock_taken", got["error"])
	assert.Equal(t, f.holderAgentID, got["holder_agent_id"], "must include holder agent id")
	assert.NotEmpty(t, got["claimed_at"], "must include claimed_at timestamp")
	// agent name lookup goes through deps.Agents.GetByID — we
	// seeded the holder through agentSvc.Register so the row
	// exists. The display name is whatever agentservice.New set
	// for the unique name field ("p15-holder-XXXXXX").
	assert.Contains(t, got["holder_agent_name"], "p15-holder-",
		"holder_agent_name should resolve via deps.Agents.GetByID")
}

func TestPhase15_LockTaken_FallsBackToBareErrorWhenHolderRepoUnwired(t *testing.T) {
	// Build a router WITHOUT TaskLockHolder set — handlers should
	// still produce a 409, just without the holder fields. This
	// pins backwards-compat: a downstream consumer can rely on
	// {error:"lock_taken"} being present even without the new
	// seam wired.
	f := newPhase15Fixture(t)
	// Re-build the deps with TaskLockHolder nil. The lock repo
	// for taskservice.Locks is created fresh — it's a thin
	// wrapper around the same *sql.DB so both repos see the
	// same task_locks rows.
	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda"),
		Users:       sqlite.NewUserRepository(f.db),
		Projects:    f.projects,
		Tasks:       f.tasks,
		Tokens:      sqlite.NewAPITokenRepository(f.db),
		TaskService: taskservice.New(f.tasks, sqlite.NewTaskLockRepository(f.db), nil, nil, ws.NewHub()),
		Agents:      f.agents,
		Comments:    commentservice.New(sqlite.NewCommentRepository(f.db), ws.NewHub(), nil),
		Activities:  sqlite.NewActivityRepository(f.db),
		WSHub:       ws.NewHub(),
		CookieName:  "orenda_session",
		// TaskLockHolder deliberately nil — backwards-compat path.
	}
	router := api.NewRouter(&deps)

	taskID := f.seedTask(t)

	rr := doWithRouter(t, router, http.MethodPost, "/api/v1/agent/tasks/"+taskID+"/claim", f.holderToken, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	rr = doWithRouter(t, router, http.MethodPost, "/api/v1/agent/tasks/"+taskID+"/claim", f.rivalToken, nil)
	require.Equal(t, http.StatusConflict, rr.Code, "body=%s", rr.Body.String())

	var got map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "lock_taken", got["error"])
	_, hasID := got["holder_agent_id"]
	assert.False(t, hasID, "no holder info when TaskLockHolder is nil")
}

// doWithRouter is the same as phase15Fixture.do but lets a test
// mount a custom router (used for the "no holder seam" case above).
func doWithRouter(t *testing.T, router http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// ---- Phase 15.3: agent context surfaces blocked_by + lock_holder ----

func TestPhase15_AgentContext_BlockedByAndLockHolder(t *testing.T) {
	f := newPhase15Fixture(t)
	targetID := f.seedTask(t)
	blockerID := f.seedTask(t)

	// Holder claims the target BEFORE we wire the dependency. That
	// way the claim isn't blocked at claim time (no unresolved
	// blockers yet); we add the blocker afterwards so the
	// context snapshot reflects the current state. Reading the
	// context shouldn't require re-claiming.
	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+targetID+"/claim", f.holderToken, map[string]string{})
	require.Equal(t, http.StatusOK, rr.Code, "claim body=%s", rr.Body.String())

	f.seedDependency(t, targetID, blockerID)

	// Bearer-agent fetches its own context. LockHolder must be
	// populated (we hold the lock); BlockedBy must list the
	// blocker (just added above).
	rr = f.do(t, http.MethodGet, "/api/v1/agent/tasks/"+targetID+"/context", f.holderToken, nil)
	require.Equal(t, http.StatusOK, rr.Code, "context body=%s", rr.Body.String())

	var got struct {
		BlockedBy  []string `json:"blocked_by"`
		LockHolder *struct {
			AgentID   string `json:"agent_id"`
			AgentName string `json:"agent_name"`
		} `json:"lock_holder"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))

	require.Len(t, got.BlockedBy, 1, "blocked_by should list the unfinished blocker")
	assert.Equal(t, blockerID, got.BlockedBy[0])

	require.NotNil(t, got.LockHolder, "lock_holder should be populated")
	assert.Equal(t, f.holderAgentID, got.LockHolder.AgentID)
	assert.Contains(t, got.LockHolder.AgentName, "p15-holder-")
}

// TestPhase15_UserContext_SameHelpers asserts the user-side
// /tasks/:id/context endpoint surfaces blocked_by + lock_holder
// too — they share populateContextBlockers / populateContextLockHolder
// with the bearer-agent endpoint. We're really testing the
// helpers, not two implementations.
func TestPhase15_UserContext_SameHelpers(t *testing.T) {
	f := newPhase15Fixture(t)
	targetID := f.seedTask(t)
	blockerID := f.seedTask(t)

	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+targetID+"/claim", f.holderToken, map[string]string{})
	require.Equal(t, http.StatusOK, rr.Code)
	f.seedDependency(t, targetID, blockerID)

	cookie := f.loginOwner(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+targetID+"/context", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr = httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "user context body=%s", rr.Body.String())

	var got struct {
		BlockedBy  []string     `json:"blocked_by"`
		LockHolder *lockHolderT `json:"lock_holder"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, []string{blockerID}, got.BlockedBy)
	require.NotNil(t, got.LockHolder)
	assert.Equal(t, f.holderAgentID, got.LockHolder.AgentID)
}

func TestPhase15_AgentContext_LockHolderAbsentWhenNoLock(t *testing.T) {
	// A task nobody has claimed yet should have no LockHolder.
	// BlockedBy should also be empty (no deps seeded).
	//
	// We use the user-side /tasks/:id/context endpoint here
	// because the agent-side endpoint requires the bearer to
	// be currently assigned (AssigneeID != '' && AssigneeID ==
	// agent.ID). When nobody has claimed the task, AssigneeID
	// is empty — so the agent-side would 403. The user side
	// doesn't have that gate and exercises the same helper
	// functions.
	f := newPhase15Fixture(t)
	taskID := f.seedTask(t)

	cookie := f.loginOwner(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/context", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "user context body=%s", rr.Body.String())

	var got struct {
		BlockedBy  []string     `json:"blocked_by"`
		LockHolder *lockHolderT `json:"lock_holder"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Empty(t, got.BlockedBy, "no deps seeded → blocked_by should be absent")
	assert.Nil(t, got.LockHolder, "no agent holds the lock → lock_holder should be omitted")
}

// lockHolderT is a sub-shape helper to keep the "omitted" assertion
// readable. JSON unmarshal treats absent vs explicit null the same
// way in Go, but the struct field stays nil if the JSON key is
// missing — which is what we want to assert.
type lockHolderT struct {
	AgentID    string    `json:"agent_id"`
	AgentName  string    `json:"agent_name"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// ---- Phase 15.4: listAgentTasksHandler excludes self-assigned ----

func TestPhase15_ListAgentTasks_ReadyExcludesSelfAssigned(t *testing.T) {
	f := newPhase15Fixture(t)
	taskA := f.seedTask(t)
	taskB := f.seedTask(t)

	// Holder claims taskA. Rival claims taskB.
	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+taskA+"/claim", f.holderToken, map[string]string{})
	require.Equal(t, http.StatusOK, rr.Code, "holder claim body=%s", rr.Body.String())
	rr = f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+taskB+"/claim", f.rivalToken, map[string]string{})
	require.Equal(t, http.StatusOK, rr.Code, "rival claim body=%s", rr.Body.String())

	// Holder's ?ready=true list should NOT include taskA (claimed
	// by me) and SHOULD NOT include taskB (rival-claimed). The
	// Phase 15 bug is that self-assigned tasks were leaking into
	// the ready list — we pin that as absent.
	rr = f.do(t, http.MethodGet, "/api/v1/agent/tasks?ready=true", f.holderToken, nil)
	require.Equal(t, http.StatusOK, rr.Code, "list body=%s", rr.Body.String())

	var got struct {
		Tasks []struct {
			Task struct {
				ID         string `json:"id"`
				AssigneeID string `json:"assignee_id"`
			} `json:"task"`
			Ready bool `json:"ready"`
		} `json:"tasks"`
		Count int `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))

	// taskA (holder's own) must NOT appear in ready=true.
	for _, row := range got.Tasks {
		if row.Task.ID == taskA {
			t.Errorf("holder's own task %s must not appear in ready=true list (was in list)", taskA)
		}
	}
	// taskB (rival's) also must not appear.
	for _, row := range got.Tasks {
		if row.Task.ID == taskB {
			t.Errorf("rival's task %s must not appear in ready=true list (was in list)", taskB)
		}
	}
}
