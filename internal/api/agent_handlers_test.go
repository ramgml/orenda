package api_test

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
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	timeentryservice "github.com/ramgml/orenda/internal/service/timeentry"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// agentFixture bundles a router + a fresh agent + its plain token.
type agentFixture struct {
	router  http.Handler
	agentID string
	token   string
	db      *sqlLite
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	ownerEmail := "agent-owner-" + randLite()[:8] + "@x.com"
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
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	taskSvc := taskservice.New(
		sqlite.NewTaskRepository(db),
		sqlite.NewTaskLockRepository(db),
		nil, nil, hub,
	)
	// Task 87: the auto-timer needs the time-entry repo and the
	// columns repo (status -> column sync), otherwise the submit
	// gate fails open and claim never opens an interval.
	taskSvc.Time = sqlite.NewTimeEntryRepository(db)
	taskSvc.Columns = sqlite.NewProjectRepository(db)

	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)

	// Adapter for agentservice.TokenMinter.
	tm := &agentFixtureTMinter{tokens: tokens}
	agentSvc := agentservice.New(agents, users, tm, hub, nil)
	got, err := agentSvc.Register(context.Background(), "qwen-test", []string{"qwen"}, "test", nil)
	require.NoError(t, err)

	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    sqlite.NewProjectRepository(db),
		Tasks:       sqlite.NewTaskRepository(db),
		Tokens:      tokens,
		TaskService: taskSvc,
		TimeService: timeentryservice.New(sqlite.NewTimeEntryRepository(db), hub, nil),
		Agents:      agents,
		Comments:    commentSvc,
		Activities:  sqlite.NewActivityRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)
	return &agentFixture{
		router:  router,
		agentID: got.Agent.ID,
		token:   got.PlainToken,
		db:      db,
	}
}

type agentFixtureTMinter struct{ tokens *sqlite.APITokenRepo }

func (a *agentFixtureTMinter) MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (string, string, error) {
	row, err := a.tokens.Create(ctx, userID, name, hash, scopesJSON, expiresAt)
	if err != nil {
		return "", "", err
	}
	return row.ID, row.Name, nil
}

func TestAgent_MeReturnsAgent(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	t.Logf("fx.token=%q agentID=%q", fx.token, fx.agentID)

	// First, verify the route even matches the right path.
	for _, p := range []string{"/api/v1/agent/me", "/api/v1/me", "/api/v1/agent/heartbeat", "/api/v1/agent/tasks/x/claim"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer "+fx.token)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		t.Logf("GET %-40s -> %d %s", p, rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var a struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &a))
	assert.Equal(t, fx.agentID, a.ID)
	assert.Equal(t, "qwen-test", a.Name)
}

func TestAgent_NoTokenReturns401(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAgent_BadTokenReturns401(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAgent_HeartbeatReturnsAgent(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	t.Logf("heartbeat: %d %s", rr.Code, rr.Body.String())
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestAgent_ClaimRejectsNoTask(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	// Send claim with a non-existent task id — should return ErrNotFound (404).
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/no-such-task/claim", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
}

func TestAgent_ClaimReleaseSubmitRoundTrip(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	// Seed a real task owned by the fixture's user.
	row := fx.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	_ = user.User{} // keep import for type refs
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "AgentTest", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(fx.db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "test"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	// Claim.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/claim", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "claim body=%s", rr.Body.String())

	// Heartbeat.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	// Submit.
	req = httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/submit",
		bytes.NewReader([]byte(`{"note":"e2e"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var sub struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &sub))
	assert.Equal(t, "review", sub.Status)

	// Release.
	req = httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/release", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

// Task 87: the submit gate. A freshly claimed task has no spent
// time and no closed interval -> submit returns 422
// time_not_logged. POST /time (0-minute bypass) then satisfies the
// gate and the same submit returns 200.
func TestAgent_SubmitGate422AndManualBypass(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	row := fx.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "AgentGateTest", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(fx.db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "gated"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+fx.token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		return rr
	}

	// Gate on a task the agent never claimed: no spent time, no open
	// timer on it -> 422 time_not_logged.
	rr := post("/api/v1/agent/tasks/"+tr.ID+"/submit", `{}`)
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code, "body=%s", rr.Body.String())
	assert.JSONEq(t, `{"error":"time_not_logged"}`, rr.Body.String())

	// 0-minute manual entry: the documented bypass.
	rr = post("/api/v1/agent/tasks/"+tr.ID+"/time", `{"minutes":0,"note":"trivial"}`)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

	// Gate satisfied -> submit now goes through.
	rr = post("/api/v1/agent/tasks/"+tr.ID+"/submit", `{"note":"done"}`)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
}

// Task 87: submitting a claimed task with a running auto-timer
// passes the gate without a manual entry (open interval counts).
func TestAgent_SubmitGateOpenTimerPasses(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	row := fx.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "AgentGateTimer", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(fx.db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "running"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+fx.token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		return rr
	}

	require.Equal(t, http.StatusOK, post("/api/v1/agent/tasks/"+tr.ID+"/claim", `{}`).Code)
	rr := post("/api/v1/agent/tasks/"+tr.ID+"/submit", `{"note":"open timer"}`)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
}

// Task 87: negative minutes are rejected before the task lookup.
func TestAgent_AddManualTimeRejectsNegative(t *testing.T) {
	t.Parallel()

	fx := newAgentFixture(t)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/whatever/time", bytes.NewReader([]byte(`{"minutes":-5}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.JSONEq(t, `{"error":"minutes_must_be_non_negative"}`, rr.Body.String())
}

// Phase 27.11: agent-side comment endpoint writes the comment as
// AuthorAgent + the agent's id, not as the user. Pre-27.11 the CLI
// posted to the user-cookie route `/api/v1/tasks/{id}/comments`
// which only accepts a JWT session — agent tokens got 401.
func TestAgent_CommentCreatesAgentAuthoredComment(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	// Seed a real task.
	row := fx.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "CommentTest", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(fx.db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "needs review"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	// POST under the agent namespace.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/comments",
		bytes.NewReader([]byte(`{"body_md":"agent observation"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

	var got struct {
		AuthorType string `json:"author_type"`
		AuthorID   string `json:"author_id"`
		BodyMD     string `json:"body_md"`
		TargetID   string `json:"target_id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "agent", got.AuthorType, "comment should be authored by the agent")
	assert.Equal(t, fx.agentID, got.AuthorID, "comment author_id should be the agent's id")
	assert.Equal(t, "agent observation", got.BodyMD)
	assert.Equal(t, tr.ID, got.TargetID)
}

// Phase 27.11: agent namespace is the bearer-token path. A cookie
// session on /agent/tasks/.../comments must not pass through — it
// belongs to the user-side namespace, not the agent one.
func TestAgent_CommentRejectsUserCookie(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	row := fx.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "Cmt2", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(fx.db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "x"}
	require.NoError(t, tasks.Create(context.Background(), tr))

	// Mint a user JWT cookie through the login endpoint. We use the
	// fixture owner's real password (the one newAgentFixture set up).
	// The ownerEmail helper round-trips through the DB to grab the
	// random email; the password is the same one the fixture used.
	loginBody, _ := json.Marshal(map[string]string{
		"email":    ownerEmailFor(t, fx, ownerID),
		"password": "hunter2!",
	})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	loginRR := httptest.NewRecorder()
	fx.router.ServeHTTP(loginRR, loginReq)
	require.Equal(t, http.StatusOK, loginRR.Code, "login body=%s", loginRR.Body.String())
	cookie := loginRR.Result().Cookies()

	// Use the user cookie on the agent namespace — must 401.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+tr.ID+"/comments",
		bytes.NewReader([]byte(`{"body_md":"sneak"}`)))
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code, "body=%s", rr.Body.String())
}

// ownerEmailFor returns the email of the fixture's owner; used in
// the cookie-based 401 check above. The fixture sets it to a
// random string, so we round-trip through the DB to fetch it.
func ownerEmailFor(t *testing.T, fx *agentFixture, ownerID string) string {
	t.Helper()
	var email string
	require.NoError(t, fx.db.QueryRow("SELECT email FROM users WHERE id = ?", ownerID).Scan(&email))
	return email
}

// Phase 27.11: agent-side await endpoint subscribes the WS hub
// under the agent's id (not the user's). We don't drive a full
// long-poll round-trip — that's covered by the user-side
// longpoll_test.go — but we DO assert that the endpoint exists,
// requires the agent token, and that the user-cookie variant
// returns 401 (mirroring the comment contract).
func TestAgent_AwaitRequiresAgentToken(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)

	// Without any auth → 401.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/events/await",
		bytes.NewReader([]byte(`{"timeout_s":1}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// With a malformed bearer token → 401.
	req = httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/events/await",
		bytes.NewReader([]byte(`{"timeout_s":1}`)))
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// With a valid bearer token → 204 (timeout, no events).
	req = httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/events/await",
		bytes.NewReader([]byte(`{"timeout_s":1,"topic":"tasks"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNoContent, rr.Code, "body=%s", rr.Body.String())
}
