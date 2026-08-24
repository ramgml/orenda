package api_test

// wiki:project-wiki-link — project ↔ wiki-page link tests.
//
// Covers the user-cookie and agent-namespace PATCH endpoints
// (validation + persistence + activity row + WS event), the
// user-side GET returns wiki_slug, the FK behaviour (wiki-page
// delete clears the link), and the namespace split.
//
// Fixtures wire a router with users, agents, projects, wiki + an
// activity recorder so we can assert the audit trail like the
// agent-project-description suite does.

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
	"github.com/ramgml/orenda/internal/domain/wiki"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	projectservice "github.com/ramgml/orenda/internal/service/project"
	wikiservice "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type projectWikiFixture struct {
	router      http.Handler
	token       string
	cookie      string
	ownerID     string
	projectID   string
	projects    project.Repository
	projActRepo *sqlite.ProjectActivityRepository
	wikiService *wikiservice.Service
	hub         ws.Hub
	ownerEmail  string
}

func newProjectWikiFixture(t *testing.T) *projectWikiFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	ownerEmail := "pwl-owner-" + randLite()[:8] + "@x.com"
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
	reg, err := agentSvc.Register(context.Background(), "pwl-agent", []string{"global"}, "test", nil)
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

	wikiRepo := sqlite.NewWikiRepository(db)
	wikiSvc := wikiservice.New(wikiRepo, hub)

	deps := api.Dependencies{
		Logger:                  zap.NewNop(),
		Signer:                  signer,
		Users:                   users,
		Projects:                projects,
		Tokens:                  tokens,
		Agents:                  agents,
		AgentService:            agentSvc,
		WikiService:             wikiSvc,
		ProjectActivityRecorder: projActRecorder,
		WSHub:                   hub,
		CookieName:              "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	created, _, _, err := projects.CreateProject(context.Background(), &project.Project{
		Name:        "PWL Proj",
		Color:       project.DefaultColor,
		Description: "desc",
		OwnerID:     owner.ID,
	})
	require.NoError(t, err)

	// Log the owner in (cookie) — used by user-cookie tests.
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

	return &projectWikiFixture{
		router:      router,
		token:       reg.PlainToken,
		cookie:      cookie,
		ownerID:     owner.ID,
		projectID:   created.ID,
		projects:    projects,
		projActRepo: projActRepo,
		wikiService: wikiSvc,
		hub:         hub,
		ownerEmail:  ownerEmail,
	}
}

func (fx *projectWikiFixture) userReq(method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: fx.cookie})
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

func (fx *projectWikiFixture) agentReq(path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(http.MethodPatch, path, &buf)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

// createRoadmapPage seeds the one wiki page the wiki-slug tests link
// against (slug=roadmap, per wiki:project-wiki-link the «Orenda dev»
// project points at wiki:roadmap). Tests that need a different slug
// (none in the current suite) should add a helper rather than
// re-introducing a four-arg createWikiPage.
func (fx *projectWikiFixture) createRoadmapPage(t *testing.T) {
	t.Helper()
	saved, err := fx.wikiService.Save(context.Background(), &wiki.Page{
		Slug:      "roadmap",
		Title:     "Roadmap",
		ContentMD: "# Roadmap",
	})
	require.NoError(t, err)
	require.Equal(t, "roadmap", saved.Slug)
}

// ---------- User cookie: GET returns wiki_slug ----------

func TestProjectWiki_GetReturnsEmptySlugByDefault(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	rr := fx.userReq(http.MethodGet, "/api/v1/projects/"+fx.projectID, nil)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var got project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, "", got.WikiSlug, "wiki_slug defaults to empty when not linked")
}

// ---------- User cookie: PATCH validates and persists ----------

func TestProjectWiki_UserPatch_LinksExistingPage(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	rr := fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "roadmap",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var got project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, "roadmap", got.WikiSlug)

	// Persisted.
	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "roadmap", persisted.WikiSlug, "wiki_slug must round-trip through the DB")
}

func TestProjectWiki_UserPatch_RejectsUnknownSlugWith422(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	rr := fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "no-such-page",
	})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"unknown slug must be 422, got %d body=%s", rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "wiki_slug_not_found")

	// The project row is unchanged.
	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "", persisted.WikiSlug, "rejected PATCH must not write wiki_slug")
}

func TestProjectWiki_UserPatch_EmptyStringUnlinks(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	// First link.
	rr := fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "roadmap",
	})
	require.Equal(t, http.StatusOK, rr.Code, "link: %s", rr.Body.String())

	// Then unlink via empty string.
	rr = fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "",
	})
	require.Equal(t, http.StatusOK, rr.Code, "unlink: %s", rr.Body.String())

	var got project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, "", got.WikiSlug)

	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "", persisted.WikiSlug)
}

func TestProjectWiki_UserPatch_NilLeavesSlugAlone(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	// First link.
	rr := fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "roadmap",
	})
	require.Equal(t, http.StatusOK, rr.Code)

	// Then a body without wiki_slug — pointer nil → leave alone.
	rr = fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"description": "new copy",
	})
	require.Equal(t, http.StatusOK, rr.Code)

	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "roadmap", persisted.WikiSlug, "nil wiki_slug must NOT clear the link")
	assert.Equal(t, "new copy", persisted.Description)
}

func TestProjectWiki_UserPatch_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	rr := fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "  roadmap  ",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var got project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, "roadmap", got.WikiSlug, "whitespace must be trimmed")
}

// ---------- Agent namespace: PATCH validates and writes audit ----------

func TestProjectWiki_AgentPatch_LinksAndWritesActivity(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	events, unsub := fx.hub.Subscribe(fx.ownerID, "projects")
	defer unsub()

	rr := fx.agentReq("/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "roadmap",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var got project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, "roadmap", got.WikiSlug)

	// Persisted.
	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "roadmap", persisted.WikiSlug)

	// Activity row.
	rows, err := fx.projActRepo.ListByProject(context.Background(), fx.projectID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, project.ActivityWikiSlugChanged, rows[0].Kind)
	assert.Equal(t, project.ActorAgent, rows[0].ActorType)
	var diff struct {
		Before string `json:"before"`
		After  string `json:"after"`
	}
	require.NoError(t, json.Unmarshal([]byte(rows[0].Payload), &diff))
	assert.Equal(t, "", diff.Before)
	assert.Equal(t, "roadmap", diff.After)

	// WS event published.
	select {
	case ev := <-events:
		body, ok := ev.Body.(map[string]any)
		require.True(t, ok, "WS event body must be a map")
		assert.Equal(t, "project.updated", body["type"])
		assert.Equal(t, "agent", body["actor_type"])
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the projects WS event")
	}
}

func TestProjectWiki_AgentPatch_RejectsUnknownSlugWith422(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	rr := fx.agentReq("/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "no-such-page",
	})
	require.Equal(t, http.StatusUnprocessableEntity, rr.Code,
		"unknown slug must be 422, got %d body=%s", rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), "wiki_slug_not_found")

	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "", persisted.WikiSlug)
}

func TestProjectWiki_AgentPatch_EmptyStringUnlinks(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	// First link.
	rr := fx.agentReq("/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "roadmap",
	})
	require.Equal(t, http.StatusOK, rr.Code, "link: %s", rr.Body.String())

	// Then unlink.
	rr = fx.agentReq("/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "",
	})
	require.Equal(t, http.StatusOK, rr.Code, "unlink: %s", rr.Body.String())

	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "", persisted.WikiSlug)
}

func TestProjectWiki_AgentPatch_NoSlugFieldNoOp(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	// Empty body — nothing to change.
	rr := fx.agentReq("/api/v1/agent/projects/"+fx.projectID, map[string]any{})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "", persisted.WikiSlug)

	rows, err := fx.projActRepo.ListByProject(context.Background(), fx.projectID, 10)
	require.NoError(t, err)
	assert.Empty(t, rows, "no-op PATCH must NOT write an activity row")
}

func TestProjectWiki_AgentPatch_BothDescriptionAndSlug(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	rr := fx.agentReq("/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"description": "now linked",
		"wiki_slug":   "roadmap",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "now linked", persisted.Description)
	assert.Equal(t, "roadmap", persisted.WikiSlug)

	// Two activity rows: one per field, oldest-first ordering.
	rows, err := fx.projActRepo.ListByProject(context.Background(), fx.projectID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2, "two changed fields → two activity rows")
	// ListByProject is newest-first; we expect the slug event last.
	kinds := []project.ActivityKind{rows[0].Kind, rows[1].Kind}
	assert.Contains(t, kinds, project.ActivityDescriptionChanged)
	assert.Contains(t, kinds, project.ActivityWikiSlugChanged)
}

func TestProjectWiki_AgentPatch_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	rr := fx.agentReq("/api/v1/agent/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "  roadmap  ",
	})
	require.Equal(t, http.StatusOK, rr.Code)

	var got project.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	assert.Equal(t, "roadmap", got.WikiSlug)
}

// ---------- FK chain: deleting a wiki page clears the link ----------

func TestProjectWiki_DeleteWikiPageClearsLink(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	// Link.
	rr := fx.userReq(http.MethodPatch, "/api/v1/projects/"+fx.projectID, map[string]any{
		"wiki_slug": "roadmap",
	})
	require.Equal(t, http.StatusOK, rr.Code, "link: %s", rr.Body.String())

	// Delete the wiki page.
	require.NoError(t, fx.wikiService.Delete(context.Background(), "roadmap"))

	// The project row is still there; wiki_slug cleared via the FK.
	persisted, err := fx.projects.GetProject(context.Background(), fx.projectID)
	require.NoError(t, err)
	assert.Equal(t, "", persisted.WikiSlug,
		"deleting the linked wiki page must clear wiki_slug via FK ON DELETE SET NULL")
}

// ---------- User-cookie namespace split (mirrors agent-projects) ----------

func TestProjectWiki_UserCookie_RejectedOnAgentRoute(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	for _, m := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/agent/projects/" + fx.projectID, nil},
		{http.MethodPatch, "/api/v1/agent/projects/" + fx.projectID, map[string]any{"wiki_slug": "roadmap"}},
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

func TestProjectWiki_AgentToken_RejectedOnUserRoute(t *testing.T) {
	t.Parallel()
	fx := newProjectWikiFixture(t)
	fx.createRoadmapPage(t)

	for _, m := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/projects/" + fx.projectID, nil},
		{http.MethodPatch, "/api/v1/projects/" + fx.projectID, map[string]any{"wiki_slug": "roadmap"}},
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
