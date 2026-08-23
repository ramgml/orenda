package api_test

// wiki:agent-project-description — agent-namespace project read/write.
//
// Pins the DoD from the wiki постановка:
//
//   - GET /agent/projects/{id} returns the project to a bearer agent.
//   - PATCH /agent/projects/{id} updates description (v1 field) and
//     writes a project_activity row (kind=description_changed,
//     actor_type=agent, before/after diff payload).
//   - Namespace split both directions: a user cookie 401s on the
//     agent route; an agent token 401s on the user-side
//     /api/v1/projects route.
//   - PATCH emits a WS event on the "projects" topic.

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
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	projectservice "github.com/ramgml/orenda/internal/service/project"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type agentProjectFixture struct {
	router      http.Handler
	token       string
	cookie      string
	agentID     string
	ownerID     string
	projectID   string
	projects    project.Repository
	projActRepo *sqlite.ProjectActivityRepository
	hub         ws.Hub
}

func newAgentProjectFixture(t *testing.T) *agentProjectFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	ownerEmail := "ap-owner-" + randLite()[:8] + "@x.com"
	owner := &user.User{
		Email:        ownerEmail,
		PasswordHash: mustHashFast(t),
		DisplayName:  "Owner",
	}
	require.NoError(t, users.Create(context.Background(), owner))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	agentSvc := agentservice.New(agents, users, &agentFixtureTMinter{tokens: tokens}, hub, nil)
	reg, err := agentSvc.Register(context.Background(), "proj-agent", []string{"global"}, "test", nil)
	require.NoError(t, err)

	projects := sqlite.NewProjectRepository(db)
	projActRepo := sqlite.NewProjectActivityRepository(db)
	projActRecorder := projectservice.NewActivityRecorder(projActRepo)
	projActRecorder.IdentitySource = func(ctx context.Context) (project.ActorType, string, bool) {
		id, ok := api.IdentityFrom(ctx)
		if !ok || id == nil {
			return "", "", false
		}
		// Phase 32.12 follow-on: agents carry a UserID on the identity
		// (api_tokens.user_id). Check AgentID FIRST so agent-side writes
		// still produce ActorAgent rows.
		if id.AgentID != "" {
			return project.ActorAgent, id.AgentID, true
		}
		if id.UserID != "" {
			return project.ActorUser, id.UserID, true
		}
		return "", "", false
	}

	deps := api.Dependencies{
		Logger:                  zap.NewNop(),
		Signer:                  signer,
		Users:                   users,
		Projects:                projects,
		Tokens:                  tokens,
		Agents:                  agents,
		AgentService:            agentSvc,
		ProjectActivityRecorder: projActRecorder,
		WSHub:                   hub,
		CookieName:              "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	// Create a project owned by the owner (direct repo call — the
	// agent surface has no create route by design).
	created, _, _, err := projects.CreateProject(context.Background(), &project.Project{
		Name:        "Agent Proj",
		Color:       project.DefaultColor,
		Description: "original description",
		OwnerID:     owner.ID,
	})
	require.NoError(t, err)

	// Log the owner in for the user-cookie side of the namespace split.
	body, _ := json.Marshal(map[string]string{"email": ownerEmail, "password": "hunter2!"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	require.Equal(t, http.StatusOK, loginRR.Code, "login: %s", loginRR.Body.String())
	cookie := ""
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == "orenda_session" {
			cookie = c.Value
		}
	}
	require.NotEmpty(t, cookie)

	return &agentProjectFixture{
		router:      router,
		token:       reg.PlainToken,
		cookie:      cookie,
		agentID:     reg.Agent.ID,
		ownerID:     owner.ID,
		projectID:   created.ID,
		projects:    projects,
		projActRepo: projActRepo,
		hub:         hub,
	}
}

func (fx *agentProjectFixture) agentReq(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

func TestAgentProjects_GetReturnsProject(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	rr := fx.agentReq(http.MethodGet, "/api/v1/agent/projects/"+fx.projectID, nil)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var p project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&p))
	assert.Equal(t, fx.projectID, p.ID)
	assert.Equal(t, "Agent Proj", p.Name)
	assert.Equal(t, "original description", p.Description)
}

func TestAgentProjects_GetNotFound(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	rr := fx.agentReq(http.MethodGet, "/api/v1/agent/projects/does-not-exist", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestAgentProjects_PatchDescription(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)

	rr := fx.agentReq(http.MethodPatch, "/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"description": "updated by agent",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var p project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&p))
	assert.Equal(t, "updated by agent", p.Description)

	// Persisted.
	got, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "updated by agent", got.Description)

	// Activity row: kind=description_changed, actor_type=agent, diff payload.
	rows, err := fx.projActRepo.ListByProject(context.Background(), fx.projectID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1, "exactly one activity row for one description change")
	assert.Equal(t, project.ActivityDescriptionChanged, rows[0].Kind)
	assert.Equal(t, project.ActorAgent, rows[0].ActorType)
	assert.Equal(t, fx.agentID, rows[0].ActorID)
	var diff struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
	require.NoError(t, json.Unmarshal([]byte(rows[0].Payload), &diff))
	assert.Equal(t, "original description", diff.Before)
	assert.Equal(t, "updated by agent", diff.After)
}

func TestAgentProjects_PatchClearsDescription(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	rr := fx.agentReq(http.MethodPatch, "/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"description": "",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	got, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "", got.Description, "empty string clears the description")
}

func TestAgentProjects_PatchNoDescriptionFieldIsNoop(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	// Body without a description key: nothing changes, no activity row.
	rr := fx.agentReq(http.MethodPatch, "/api/v1/agent/projects/"+fx.projectID, map[string]any{})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	got, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "original description", got.Description)
	rows, err := fx.projActRepo.ListByProject(context.Background(), fx.projectID, 10)
	require.NoError(t, err)
	assert.Empty(t, rows, "no-op PATCH must not write an activity row")
}

func TestAgentProjects_PatchIgnoresUserOnlyFields(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	// name/color/archived are user-only; the agent handler must not
	// apply them even if sent.
	rr := fx.agentReq(http.MethodPatch, "/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"description": "desc changed",
		"name":        "HACKED",
		"color":       "#000000",
		"archived":    true,
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	got, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "desc changed", got.Description)
	assert.Equal(t, "Agent Proj", got.Name, "name must not change via agent PATCH")
	assert.NotEqual(t, "#000000", got.Color, "color must not change via agent PATCH")
	assert.False(t, got.Archived, "archived must not change via agent PATCH")
}

// Namespace split, direction 1: a user cookie is NOT an agent
// credential — the agent route must 401.
func TestAgentProjects_CookieRejectedOnAgentRoute(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	for _, m := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/agent/projects/" + fx.projectID, nil},
		{http.MethodPatch, "/api/v1/agent/projects/" + fx.projectID, map[string]any{"description": "x"}},
	} {
		var buf bytes.Buffer
		if m.body != nil {
			_ = json.NewEncoder(&buf).Encode(m.body)
		}
		req := httptest.NewRequest(m.method, m.path, &buf)
		req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code,
			"%s %s with cookie must 401, got %d", m.method, m.path, rr.Code)
	}
}

// Namespace split, direction 2: an agent token is NOT a user
// credential — the user-side project routes must 401.
func TestAgentProjects_AgentTokenRejectedOnUserRoute(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	for _, m := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/projects/" + fx.projectID, nil},
		{http.MethodPatch, "/api/v1/projects/" + fx.projectID, map[string]any{"description": "x"}},
	} {
		var buf bytes.Buffer
		if m.body != nil {
			_ = json.NewEncoder(&buf).Encode(m.body)
		}
		req := httptest.NewRequest(m.method, m.path, &buf)
		req.Header.Set("Authorization", "Bearer "+fx.token)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code,
			"%s %s with agent token must 401, got %d", m.method, m.path, rr.Code)
	}
}

// No token at all → 401.
func TestAgentProjects_RequiresToken(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/projects/"+fx.projectID, nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// WS: PATCH publishes a project.updated event on the "projects" topic.
func TestAgentProjects_PatchEmitsWSEvent(t *testing.T) {
	t.Parallel()
	fx := newAgentProjectFixture(t)
	events, unsub := fx.hub.Subscribe(fx.ownerID, "projects")
	defer unsub()

	rr := fx.agentReq(http.MethodPatch, "/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"description": "ws check",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	select {
	case ev := <-events:
		body, ok := ev.Body.(map[string]any)
		require.True(t, ok, "event body must be a map")
		assert.Equal(t, "project.updated", body["type"])
		assert.Equal(t, "agent", body["actor_type"])
		assert.Equal(t, fx.agentID, body["actor_id"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the projects WS event")
	}
}
