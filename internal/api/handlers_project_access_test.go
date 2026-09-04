package api_test

// Task 140 (agent-project-scope) contract tests.
//
// Access model: a project is visible/claimable to an agent when
// projects.agents_allowed = 1 OR the agent holds a grant row in
// project_agents. A closed project with an empty grant list is
// accessible to nobody. Subtests map 1:1 to the assignment points
// (a)–(k):
//
//	a  agents_allowed=true → visible + claimable
//	b  closed + grant row   → visible + claimable
//	c  closed, no grant     → invisible + claim → 422 not_in_scope
//	d  closed + EMPTY list  → same as (c)
//	e  fresh project        → agents_allowed=0 in DB + invisible
//	f  GET /agent/projects  → filtered to open + granted
//	g  inbox task           → always visible
//	h  ?project_id/?project → scoping, 400 on both, 404 unknown
//	i  agent PATCH agents_allowed → 422 owner_only_field, DB unchanged
//	j  agent token on PUT /projects/{id}/agents → 401 (namespace split)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// accessFixture is the test rig for the task-140 access contract.
// Same shape as phase15Fixture: one owner user, two registered
// agents (bearer tokens), full router against a fresh SQLite DB.
type accessFixture struct {
	router http.Handler

	ownerEmail  string
	agentAToken string
	agentAID    string
	agentBToken string
	agentBID    string

	db       *sqlLite
	tasks    task.Repository
	projects project.Repository
	agents   agent.Repository
}

func newAccessFixture(t *testing.T) *accessFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	ownerEmail := "t140-" + randLite()[:8] + "@x.com"
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

	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	taskSvc := taskservice.New(taskRepo, sqlite.NewTaskLockRepository(db), nil, nil, hub)

	tokens := sqlite.NewAPITokenRepository(db)
	tm := &agentFixtureTMinter{tokens: tokens}
	agentSvc := agentservice.New(agentRepo, users, tm, hub, nil)

	regA, err := agentSvc.Register(context.Background(),
		"t140-agenta-"+randLite()[:6], []string{"qwen"}, "worker", nil)
	require.NoError(t, err)
	regB, err := agentSvc.Register(context.Background(),
		"t140-agentb-"+randLite()[:6], []string{"qwen"}, "rival", nil)
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
	}

	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)
	return &accessFixture{
		router:      router,
		ownerEmail:  ownerEmail,
		agentAToken: regA.PlainToken,
		agentAID:    regA.Agent.ID,
		agentBToken: regB.PlainToken,
		agentBID:    regB.Agent.ID,
		db:          db,
		tasks:       taskRepo,
		projects:    projRepo,
		agents:      agentRepo,
	}
}

// createProject seeds a project owned by the fixture user. Fresh
// projects are closed by default (agents_allowed = 0) — the agent
// surface must prove access explicitly.
func (f *accessFixture) createProject(t *testing.T, name string) *project.Project {
	t.Helper()
	var ownerID string
	require.NoError(t, f.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&ownerID))
	p, _, _, err := f.projects.CreateProject(context.Background(), &project.Project{
		Name: name, OwnerID: ownerID,
	})
	require.NoError(t, err)
	return p
}

// seedTask creates a todo task in the given project.
func (f *accessFixture) seedTask(t *testing.T, p *project.Project, title string) *task.Task {
	t.Helper()
	var colID string
	require.NoError(t, f.db.QueryRow(
		`SELECT c.id FROM columns c JOIN boards b ON b.id = c.board_id WHERE b.project_id = ? AND c.status = 'todo'`,
		p.ID).Scan(&colID))
	tr := &task.Task{ProjectID: p.ID, ColumnID: colID, Title: title}
	require.NoError(t, f.tasks.Create(context.Background(), tr))
	return tr
}

// seedInboxTask creates a todo task with project_id NULL (Phase 16
// inbox shape — no project, no column).
func (f *accessFixture) seedInboxTask(t *testing.T, title string) *task.Task {
	t.Helper()
	tr := &task.Task{Title: title}
	require.NoError(t, f.tasks.Create(context.Background(), tr))
	return tr
}

// setAgentsAllowed flips projects.agents_allowed directly in SQL —
// the tests exercise the API flag path separately (subtest k).
func (f *accessFixture) setAgentsAllowed(t *testing.T, projectID string, v bool) {
	t.Helper()
	_, err := f.db.ExecContext(context.Background(),
		`UPDATE projects SET agents_allowed = ? WHERE id = ?`, v, projectID)
	require.NoError(t, err)
}

// grantAgent inserts a project_agents row directly (the repo-level
// path is covered by the API round-trip in subtest k).
func (f *accessFixture) grantAgent(t *testing.T, projectID, agentID string) {
	t.Helper()
	var ownerID string
	require.NoError(t, f.db.QueryRow("SELECT id FROM users LIMIT 1").Scan(&ownerID))
	_, err := f.db.ExecContext(context.Background(),
		`INSERT INTO project_agents (project_id, agent_id, added_by) VALUES (?, ?, ?)`,
		projectID, agentID, ownerID)
	require.NoError(t, err)
}

// agentTasks GETs the agent work surface and decodes the task ids.
func (f *accessFixture) agentTasks(t *testing.T, token, query string) []string {
	t.Helper()
	rr := f.do(t, http.MethodGet, "/api/v1/agent/tasks"+query, token, nil)
	require.Equal(t, http.StatusOK, rr.Code, "list tasks body=%s", rr.Body.String())
	var got struct {
		Tasks []struct {
			Task struct {
				ID string `json:"id"`
			} `json:"task"`
		} `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	ids := make([]string, 0, len(got.Tasks))
	for _, row := range got.Tasks {
		ids = append(ids, row.Task.ID)
	}
	return ids
}

func (f *accessFixture) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return doWithRouter(t, f.router, method, path, token, body)
}

// ---- (a) open project: visible + claimable ----

func TestProjectAccess_OpenProjectVisibleAndClaimable(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "open-project")
	tr := f.seedTask(t, p, "open task")
	f.setAgentsAllowed(t, p.ID, true)

	ids := f.agentTasks(t, f.agentAToken, "")
	assert.Contains(t, ids, tr.ID, "task of an open project must be listed")

	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+tr.ID+"/claim", f.agentAToken, map[string]string{})
	require.Equal(t, http.StatusOK, rr.Code, "claim body=%s", rr.Body.String())
}

// ---- (b) closed project + grant row: visible + claimable ----

func TestProjectAccess_ClosedProjectWithGrantVisibleAndClaimable(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "granted-project")
	tr := f.seedTask(t, p, "granted task")
	f.grantAgent(t, p.ID, f.agentAID)

	ids := f.agentTasks(t, f.agentAToken, "")
	assert.Contains(t, ids, tr.ID, "granted agent must see the closed project's task")

	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+tr.ID+"/claim", f.agentAToken, map[string]string{})
	require.Equal(t, http.StatusOK, rr.Code, "claim body=%s", rr.Body.String())
}

// ---- (c) closed project, no grant: invisible + claim 422 ----

func TestProjectAccess_ClosedProjectWithoutGrantInvisibleAndBlocked(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "closed-project")
	tr := f.seedTask(t, p, "closed task")

	ids := f.agentTasks(t, f.agentAToken, "")
	assert.NotContains(t, ids, tr.ID, "ungranted agent must not see the task")

	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+tr.ID+"/claim", f.agentAToken, map[string]string{})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "not_in_scope", got["error"])
}

// ---- (d) closed project + EMPTY grant list: same as (c) ----

func TestProjectAccess_ClosedProjectEmptyGrantListBlocked(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "empty-grants")
	tr := f.seedTask(t, p, "empty-list task")
	// Empty list = the default; PUT /agents with [] goes through the
	// repo once via API in subtest k — here the table just has no rows.

	ids := f.agentTasks(t, f.agentAToken, "")
	assert.NotContains(t, ids, tr.ID)

	var n int
	require.NoError(t, f.db.QueryRow(
		`SELECT COUNT(*) FROM project_agents WHERE project_id = ?`, p.ID).Scan(&n))
	assert.Equal(t, 0, n)

	rr := f.do(t, http.MethodPost, "/api/v1/agent/tasks/"+tr.ID+"/claim", f.agentAToken, map[string]string{})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "not_in_scope", got["error"])
}

// ---- (e) fresh project: agents_allowed=0 + invisible ----

func TestProjectAccess_FreshProjectDefaultsClosed(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "fresh-project")

	var aa int
	require.NoError(t, f.db.QueryRow(
		`SELECT agents_allowed FROM projects WHERE id = ?`, p.ID).Scan(&aa))
	assert.Equal(t, 0, aa, "fresh project must default to agents_allowed=0")

	tr := f.seedTask(t, p, "fresh task")
	ids := f.agentTasks(t, f.agentAToken, "")
	assert.NotContains(t, ids, tr.ID, "default-closed project must be invisible to agents")
}

// ---- (f) GET /agent/projects is filtered ----

func TestProjectAccess_AgentProjectListFiltered(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	openP := f.createProject(t, "list-open")
	f.setAgentsAllowed(t, openP.ID, true)
	grantedP := f.createProject(t, "list-granted")
	f.grantAgent(t, grantedP.ID, f.agentAID)
	closedP := f.createProject(t, "list-closed")

	rr := f.do(t, http.MethodGet, "/api/v1/agent/projects", f.agentAToken, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var got struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	ids := make([]string, 0, len(got.Projects))
	for _, p := range got.Projects {
		ids = append(ids, p.ID)
	}
	assert.Contains(t, ids, openP.ID, "open project must be listed")
	assert.Contains(t, ids, grantedP.ID, "granted project must be listed")
	assert.NotContains(t, ids, closedP.ID, "closed ungranted project must be hidden")
}

// ---- (g) inbox task stays visible ----

func TestProjectAccess_InboxTaskAlwaysVisible(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	tr := f.seedInboxTask(t, "inbox task")

	ids := f.agentTasks(t, f.agentAToken, "")
	assert.Contains(t, ids, tr.ID, "inbox (no-project) tasks need no grant")
}

// ---- (h) ?project_id / ?project scoping ----

func TestProjectAccess_AgentTasksProjectScoping(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	openP := f.createProject(t, "scoped-open")
	f.setAgentsAllowed(t, openP.ID, true)
	// Flip one open project back and forth so the direct-SQL helper
	// exercises both values (lint: unparam — v always true otherwise).
	f.setAgentsAllowed(t, openP.ID, false)
	f.setAgentsAllowed(t, openP.ID, true)
	inScope := f.seedTask(t, openP, "in-scope task")
	otherP := f.createProject(t, "scoped-other")
	f.setAgentsAllowed(t, otherP.ID, true)
	outOfScope := f.seedTask(t, otherP, "out-of-scope task")
	inbox := f.seedInboxTask(t, "inbox noise")

	// project_id=<uuid>
	ids := f.agentTasks(t, f.agentAToken, "?project_id="+openP.ID)
	assert.Contains(t, ids, inScope.ID)
	assert.NotContains(t, ids, outOfScope.ID)
	assert.NotContains(t, ids, inbox.ID, "scoped listing never mixes in inbox tasks")

	// project=<number>
	ids = f.agentTasks(t, f.agentAToken, fmt.Sprintf("?project=%d", openP.Number))
	assert.Contains(t, ids, inScope.ID)
	assert.NotContains(t, ids, outOfScope.ID)
	assert.NotContains(t, ids, inbox.ID, "scoped listing never mixes in inbox tasks")

	// project=P<number>
	ids = f.agentTasks(t, f.agentAToken, fmt.Sprintf("?project=P%d", openP.Number))
	assert.Contains(t, ids, inScope.ID)

	// both parameters → 400
	rr := f.do(t, http.MethodGet,
		"/api/v1/agent/tasks?project_id="+openP.ID+"&project=P1", f.agentAToken, nil)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "invalid_input", got["error"])

	// unknown number → 404
	rr = f.do(t, http.MethodGet, "/api/v1/agent/tasks?project=999999", f.agentAToken, nil)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

// ---- (i) agent PATCH with agents_allowed → 422 owner_only_field ----

func TestProjectAccess_AgentPatchAgentsAllowedRejected(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "agent-patch")

	rr := f.do(t, http.MethodPatch, "/api/v1/agent/projects/"+p.ID,
		f.agentAToken, map[string]any{"agents_allowed": true})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	var got map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "owner_only_field", got["error"])

	var aa int
	require.NoError(t, f.db.QueryRow(
		`SELECT agents_allowed FROM projects WHERE id = ?`, p.ID).Scan(&aa))
	assert.Equal(t, 0, aa, "DB must stay closed after the rejected PATCH")
}

// ---- (j) agent token on PUT /projects/{id}/agents → 401 ----

func TestProjectAccess_AgentTokenOnUserAgentsPutUnauthorized(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "ns-split")

	rr := f.do(t, http.MethodPut, "/api/v1/projects/"+p.ID+"/agents",
		f.agentAToken, map[string]any{"agent_ids": []string{f.agentAID}})
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---- (k) owner flow: PATCH flag + PUT/GET grant round-trips ----

func TestProjectAccess_OwnerFlowPatchFlagAndGrantRoundTrip(t *testing.T) {
	t.Parallel()
	f := newAccessFixture(t)
	p := f.createProject(t, "owner-flow")
	cookie := f.loginOwner(t)

	userDo := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&buf).Encode(body))
		}
		req := httptest.NewRequest(method, path, &buf)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		rr := httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		return rr
	}

	// PATCH agents_allowed=true → 200 + DB 1
	rr := userDo(http.MethodPatch, "/api/v1/projects/"+p.ID, map[string]any{"agents_allowed": true})
	require.Equal(t, http.StatusOK, rr.Code, "patch body=%s", rr.Body.String())
	var aa int
	require.NoError(t, f.db.QueryRow(
		`SELECT agents_allowed FROM projects WHERE id = ?`, p.ID).Scan(&aa))
	assert.Equal(t, 1, aa)

	// PUT [agentA] → 200; GET → [agentA]
	rr = userDo(http.MethodPut, "/api/v1/projects/"+p.ID+"/agents",
		map[string]any{"agent_ids": []string{f.agentAID}})
	require.Equal(t, http.StatusOK, rr.Code, "put body=%s", rr.Body.String())
	rr = userDo(http.MethodGet, "/api/v1/projects/"+p.ID+"/agents", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		AgentIDs []string `json:"agent_ids"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	assert.Equal(t, []string{f.agentAID}, list.AgentIDs)

	// PUT [] → GET []
	rr = userDo(http.MethodPut, "/api/v1/projects/"+p.ID+"/agents",
		map[string]any{"agent_ids": []string{}})
	require.Equal(t, http.StatusOK, rr.Code)
	rr = userDo(http.MethodGet, "/api/v1/projects/"+p.ID+"/agents", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	assert.Empty(t, list.AgentIDs)

	// PUT with unknown agent → 422 unknown_agent
	rr = userDo(http.MethodPut, "/api/v1/projects/"+p.ID+"/agents",
		map[string]any{"agent_ids": []string{"no-such-agent-uuid"}})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code)
	var errBody map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &errBody))
	assert.Equal(t, "unknown_agent", errBody["error"])
}

// loginOwner fetches the user cookie via /api/v1/auth/login (same
// convention as phase15Fixture.loginOwner).
func (f *accessFixture) loginOwner(t *testing.T) string {
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
