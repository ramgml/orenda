package api_test

// T330: проект → авто-создание выделенного агента.
//
// Pins the DoD from the wiki постановка (page project-auto-agent):
//
//   - POST /api/v1/projects → 201 with agent_token {agent, plain_token};
//     the agent is named project-<number>-<slug>, is granted to the
//     project (project_agents row), and projects.agents_allowed stays 0.
//   - Name collision: a pre-registered agent holding the would-be
//     project-<n>-<slug> name forces the -2 suffix (deterministic
//     retry via ErrNameTaken).
//   - DELETE /projects/{id}: the grant row cascades away, the agent
//     row stays (documented orphan — the owner can delete it manually).
//   - PATCH archived=true: grant and agent stay in place.
//   - Provisioning failure: the project is still 201, no agent_token
//     field, X-Agent-Provision-Error header carries the error text.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	projectagent "github.com/ramgml/orenda/internal/service/projectagent"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// failingProvisioner always errors — the handler must degrade to 201 +
// X-Agent-Provision-Error without losing the project response.
type failingProvisioner struct{ err error }

func (f failingProvisioner) EnsureProjectAgent(context.Context, *project.Project, string) (*agentservice.Registered, error) {
	return nil, f.err
}

// t330Fixture wires a fresh router with the T330 provisioner attached.
type t330Fixture struct {
	router   http.Handler
	cookie   string
	ownerID  string
	projects project.Repository
	agents   agent.Repository
	users    user.Repository
	tminter  *agentFixtureTMinter
}

func newT330Fixture(t *testing.T) *t330Fixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	email := "t330-" + randLite()[:8] + "@x.com"
	owner := &user.User{Email: email, PasswordHash: mustHashFast(t), DisplayName: "T330"}
	require.NoError(t, users.Create(context.Background(), owner))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tokens := sqlite.NewAPITokenRepository(db)
	projects := sqlite.NewProjectRepository(db)
	agentsRepo := sqlite.NewAgentRepository(db)
	tm := &agentFixtureTMinter{tokens: tokens}
	agentSvc := agentservice.New(agentsRepo, users, tm, hub, nil)

	deps := api.Dependencies{
		Logger:                  zap.NewNop(),
		Signer:                  signer,
		Users:                   users,
		Projects:                projects,
		Tokens:                  tokens,
		Agents:                  agentsRepo,
		AgentService:            agentSvc,
		ProjectAgentProvisioner: projectagent.New(agentSvc, projects),
		WSHub:                   hub,
		CookieName:              "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "login: %s", rr.Body.String())
	cookie := ""
	for _, c := range rr.Result().Cookies() {
		if c.Name == "orenda_session" {
			cookie = c.Value
		}
	}
	require.NotEmpty(t, cookie)

	return &t330Fixture{
		router:   router,
		cookie:   cookie,
		ownerID:  owner.ID,
		projects: projects,
		agents:   agentsRepo,
		users:    users,
		tminter:  tm,
	}
}

// create posts /api/v1/projects and returns the recorder.
func (f *t330Fixture) create(t *testing.T, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: f.cookie})
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// authed issues an authenticated request against the fixture router.
func (f *t330Fixture) authed(method, path string, body any) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: f.cookie})
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// t330CreateResponse is the 201 body: the project plus the optional
// agent_token envelope (plain_token shown exactly once).
type t330CreateResponse struct {
	*project.Project
	AgentToken *struct {
		Agent      agent.Agent `json:"agent"`
		PlainToken string      `json:"plain_token"`
	} `json:"agent_token"`
}

// decodeCreate decodes the create-project response.
func decodeCreate(t *testing.T, rr *httptest.ResponseRecorder) *t330CreateResponse {
	t.Helper()
	var out t330CreateResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.NotNil(t, out.Project, "project part: %s", rr.Body.String())
	return &out
}

// grantIDs reads the project's grant list through getProjectAgentsHandler.
func (f *t330Fixture) grantIDs(t *testing.T, projectID string) []string {
	t.Helper()
	rr := f.authed(http.MethodGet, "/api/v1/projects/"+projectID+"/agents", nil)
	require.Equal(t, http.StatusOK, rr.Code, "grant list: %s", rr.Body.String())
	var out struct {
		AgentIDs []string `json:"agent_ids"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	return out.AgentIDs
}

func TestT330_CreateProject_ProvisionsAgent(t *testing.T) {
	t.Parallel()
	fx := newT330Fixture(t)

	rr := fx.create(t, "Мой Первый Проект")
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	resp := decodeCreate(t, rr)
	require.NotNil(t, resp.AgentToken, "201 must carry agent_token: %s", rr.Body.String())

	// Name: project-<number>-<slug>, ASCII-transliterated project name.
	wantName := fmt.Sprintf("project-%d-moi-pervyi-proekt", resp.Number)
	assert.NotEmpty(t, resp.AgentToken.PlainToken, "plain_token shown exactly once")
	assert.Equal(t, []string{"custom"}, resp.AgentToken.Agent.Type)
	assert.Equal(t, 3, resp.AgentToken.Agent.MaxConcurrent)

	// The agent exists in storage with a stored (hashed) token.
	stored, err := fx.agents.GetByID(context.Background(), resp.AgentToken.Agent.ID)
	require.NoError(t, err)
	assert.Equal(t, wantName, stored.Name)

	// Grant list via API (getProjectAgentsHandler): exactly this agent.
	grants := fx.grantIDs(t, resp.ID)
	require.Len(t, grants, 1)
	assert.Equal(t, resp.AgentToken.Agent.ID, grants[0])

	// agents_allowed stays 0 — access is grant-based (task 140), not open.
	p, err := fx.projects.GetProject(context.Background(), resp.ID)
	require.NoError(t, err)
	assert.False(t, p.AgentsAllowed, "projects.agents_allowed must stay 0")
}

func TestT330_CreateProject_NameCollisionGetsSuffix(t *testing.T) {
	t.Parallel()
	fx := newT330Fixture(t)

	// Warmup POST draws number N and registers project-N-collide.
	rr := fx.create(t, "Collide")
	require.Equal(t, http.StatusCreated, rr.Code, "warmup body=%s", rr.Body.String())
	warm := decodeCreate(t, rr)
	require.NotNil(t, warm.AgentToken)
	require.Equal(t, fmt.Sprintf("project-%d-collide", warm.Number), warm.AgentToken.Agent.Name)

	// Simulate a clean collision for the NEXT project: the number seq
	// is monotonic, so the next project draws N+1 and its base name
	// would be project-<N+1>-collide. Pre-register an agent holding
	// that exact name through the same agent service (as any
	// operator-named agent would), then POST a same-named project.
	// The provisioner must retry with the -2 suffix instead of
	// failing the create.
	nextBase := fmt.Sprintf("project-%d-collide", warm.Number+1)
	squatter := agentservice.New(
		fx.agents,
		fx.users,
		fx.tminter,
		nil, nil,
	)
	_, err := squatter.Register(context.Background(), nextBase, []string{"custom"}, "blocks the next project agent name", nil)
	require.NoError(t, err, "pre-registered squatter: %s", nextBase)

	rr2 := fx.create(t, "Collide")
	require.Equal(t, http.StatusCreated, rr2.Code, "second body=%s", rr2.Body.String())
	second := decodeCreate(t, rr2)
	require.NotNil(t, second.AgentToken, "collision must not fail provisioning")

	// Distinct numbers, and the second name carries the -2 suffix.
	assert.Equal(t, warm.Number+1, second.Number)
	require.Equal(t, fmt.Sprintf("project-%d-collide-2", second.Number), second.AgentToken.Agent.Name)
}

func TestT330_DeleteProject_GrantCascadesAgentStays(t *testing.T) {
	t.Parallel()
	fx := newT330Fixture(t)

	rr := fx.create(t, "Delete Me")
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	resp := decodeCreate(t, rr)
	require.NotNil(t, resp.AgentToken)
	agentID := resp.AgentToken.Agent.ID

	rr = fx.authed(http.MethodDelete, "/api/v1/projects/"+resp.ID, nil)
	require.Equal(t, http.StatusNoContent, rr.Code, "delete body=%s", rr.Body.String())

	// The grant row cascaded away with the project — read at the
	// storage level (the project itself is gone, so the API route
	// 404s now).
	grants, err := fx.projects.ListAllowedAgentIDs(context.Background(), resp.ID)
	require.NoError(t, err)
	assert.Empty(t, grants, "project_agents row must cascade away")

	// The agent row stays (documented orphan: offline, no grants,
	// deletable by the owner; cleanup is not this task's surface).
	stored, err := fx.agents.GetByID(context.Background(), agentID)
	require.NoError(t, err, "agent must survive project delete")
	assert.Equal(t, agentID, stored.ID)
}

func TestT330_ArchiveProject_GrantAndAgentStay(t *testing.T) {
	t.Parallel()
	fx := newT330Fixture(t)

	rr := fx.create(t, "Archive Me")
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	resp := decodeCreate(t, rr)
	require.NotNil(t, resp.AgentToken)

	rr = fx.authed(http.MethodPatch, "/api/v1/projects/"+resp.ID, map[string]any{"archived": true})
	require.Equal(t, http.StatusOK, rr.Code, "archive body=%s", rr.Body.String())

	// Grant row survives the archive (delegation resumes on unarchive
	// with no manual steps).
	grants := fx.grantIDs(t, resp.ID)
	require.Len(t, grants, 1)
	assert.Equal(t, resp.AgentToken.Agent.ID, grants[0])

	// Agent row untouched.
	_, err := fx.agents.GetByID(context.Background(), resp.AgentToken.Agent.ID)
	require.NoError(t, err)
}

func TestT330_ProvisionFailure_StillCreatedWithHeader(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	email := "t330fail-" + randLite()[:8] + "@x.com"
	owner := &user.User{Email: email, PasswordHash: mustHashFast(t), DisplayName: "T330 fail"}
	require.NoError(t, users.Create(context.Background(), owner))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tokens := sqlite.NewAPITokenRepository(db)
	projects := sqlite.NewProjectRepository(db)

	deps := api.Dependencies{
		Logger:                  zap.NewNop(),
		Signer:                  signer,
		Users:                   users,
		Projects:                projects,
		Tokens:                  tokens,
		ProjectAgentProvisioner: failingProvisioner{err: fmt.Errorf("mint exploded: orenda_secretvalue1234567890abcdef1234")},
		WSHub:                   hub,
		CookieName:              "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(body)))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := ""
	for _, c := range rr.Result().Cookies() {
		if c.Name == "orenda_session" {
			cookie = c.Value
		}
	}

	createBody, _ := json.Marshal(map[string]any{"name": "Failing Project"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(string(createBody)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	// The project still materialises: 201, project in the body.
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var resp t330CreateResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.NotNil(t, resp.Project)
	assert.Equal(t, "Failing Project", resp.Project.Name)
	assert.Nil(t, resp.AgentToken, "no agent_token on provision failure")

	// The header names the failure — with any token-shaped secret
	// redacted (the fake error embeds an orenda_ credential).
	header := rr.Header().Get("X-Agent-Provision-Error")
	require.NotEmpty(t, header, "failure must surface in X-Agent-Provision-Error")
	assert.Contains(t, header, "mint exploded")
	assert.NotContains(t, header, "orenda_secretvalue", "token-shaped secrets must be redacted")

	// The project really persisted.
	_, err := projects.GetProject(context.Background(), resp.Project.ID)
	require.NoError(t, err)
}
