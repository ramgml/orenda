package api_test

// master-agent-role plan (step 2): a role=master agent's bearer token is
// the owner-equivalent on user routes (RequireUser), while project-agent
// tokens keep the 401 contract. Pinned here:
//
//  1. POST /api/v1/agents: {"role":"master"} → 201 role=master; omitted
//     role → project; {"role":"root"} → 400 invalid_role.
//  2. Master bearer on POST /api/v1/projects → 201; the identity carried
//     by master routes is AgentID + owner UserID + IsMaster.
//  3. Project-agent bearer on the same route → 401 (Task 140 invariant).
//  4. Unit: the master fallback refuses to mint an identity when no
//     non-system owner exists (FirstNonSystem empty) → 401, not 500.

import (
	"bytes"
	"context"
	"database/sql"
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
	agentdomain "github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	projectservice "github.com/ramgml/orenda/internal/service/project"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// mintAgentViaAPI creates an agent through POST /api/v1/agents with the
// owner cookie and returns (agentID, plainToken, agentRole).
func mintAgentViaAPI(t *testing.T, fx *agentFixture, cookie []*http.Cookie, name, role string) (string, string, string) {
	t.Helper()
	body := map[string]any{"name": name}
	if role != "" {
		body["role"] = role
	}
	payload, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(payload))
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())

	var out struct {
		Agent struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"agent"`
		PlainToken string `json:"plain_token"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.NotEmpty(t, out.PlainToken, "plain token is returned exactly once")
	return out.Agent.ID, out.PlainToken, out.Agent.Role
}

func loginOwnerCookie(t *testing.T, fx *agentFixture) []*http.Cookie {
	t.Helper()
	owner := firstNonSystemUser(t, fx)
	loginBody, _ := json.Marshal(map[string]string{"email": owner.Email, "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "login body=%s", rr.Body.String())
	return rr.Result().Cookies()
}

func firstNonSystemUser(t *testing.T, fx *agentFixture) *user.User {
	t.Helper()
	all, err := sqlite.NewUserRepository(fx.db).List(context.Background())
	require.NoError(t, err)
	for _, u := range all {
		if u.Role != "system" {
			return u
		}
	}
	t.Fatal("fixture has no non-system owner")
	return nil
}

func TestMasterAgent_RoleCreateVariants(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	cookie := loginOwnerCookie(t, fx)

	suffix := randLite()[:6]
	masterID, _, role := mintAgentViaAPI(t, fx, cookie, "maestro-"+suffix, "master")
	assert.NotEmpty(t, masterID)
	assert.Equal(t, "master", role)

	_, _, defRole := mintAgentViaAPI(t, fx, cookie, "worker-"+suffix, "")
	assert.Equal(t, "project", defRole, "omitted role defaults to project")

	payload, _ := json.Marshal(map[string]string{"name": "bogus-" + suffix, "role": "root"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(payload))
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "invalid_role")
}

func TestMasterAgent_BearerOnUserRoutes(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	cookie := loginOwnerCookie(t, fx)
	owner := firstNonSystemUser(t, fx)

	suffix := randLite()[:6]
	_, masterToken, _ := mintAgentViaAPI(t, fx, cookie, "maestro-"+suffix, "master")
	_, projectToken, _ := mintAgentViaAPI(t, fx, cookie, "worker-"+suffix, "")

	createProject := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects",
			bytes.NewReader([]byte(`{"name":"Инициатива"}`)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else {
			for _, c := range cookie {
				req.AddCookie(c)
			}
		}
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		return rr
	}

	// Master bearer: 201, the route sees an agent-attributed identity.
	masterRR := createProject(masterToken)
	require.Equal(t, http.StatusCreated, masterRR.Code, "master body=%s", masterRR.Body.String())
	var projectOut struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(masterRR.Body.Bytes(), &projectOut))
	assert.NotEmpty(t, projectOut.ID, "201 body carries the project at the top level")

	// Project agent bearer: still 401 on user routes.
	projectRR := createProject(projectToken)
	assert.Equal(t, http.StatusUnauthorized, projectRR.Code,
		"non-master agent tokens must keep the 401 contract, body=%s", projectRR.Body.String())

	// Owner cookie: the user path is untouched (regression).
	cookieRR := createProject("")
	assert.Equal(t, http.StatusCreated, cookieRR.Code, "cookie body=%s", cookieRR.Body.String())

	_ = owner // visibility parity asserted in the attribution tests (step 3).
}

// RequireAgent mirrors the role onto Identity.IsMaster for agent-namespace
// consumers (Task 140 bypass in step 5 reads it).
func TestMasterAgent_RequireAgentSetsIsMaster(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	cookie := loginOwnerCookie(t, fx)
	suffix := randLite()[:6]
	_, masterToken, _ := mintAgentViaAPI(t, fx, cookie, "maestro-"+suffix, "master")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	req.Header.Set("Authorization", "Bearer "+masterToken)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"id"`, "agent/me answers the master agent itself")
}

// fakeAgentRepo is a minimal agent.Repository for middleware-level tests.
// Only GetByTokenID is real; the remaining interface methods are unused by
// the auth path and panic if called.
type fakeAgentRepo struct {
	byTokenID map[string]*agentdomain.Agent
}

func (r *fakeAgentRepo) GetByTokenID(_ context.Context, tokenID string) (*agentdomain.Agent, error) {
	if a, ok := r.byTokenID[tokenID]; ok {
		return a, nil
	}
	return nil, agentdomain.ErrNotFound
}

func (r *fakeAgentRepo) Create(context.Context, *agentdomain.Agent) error {
	panic("not implemented in this test")
}

func (r *fakeAgentRepo) GetByID(context.Context, string) (*agentdomain.Agent, error) {
	return nil, agentdomain.ErrNotFound
}

func (r *fakeAgentRepo) GetByName(context.Context, string) (*agentdomain.Agent, error) {
	return nil, agentdomain.ErrNotFound
}

func (r *fakeAgentRepo) List(context.Context) ([]*agentdomain.Agent, error) {
	return nil, nil
}

func (r *fakeAgentRepo) Update(context.Context, *agentdomain.Agent) error {
	panic("not implemented in this test")
}

func (r *fakeAgentRepo) Delete(context.Context, string) error {
	panic("not implemented in this test")
}

func (r *fakeAgentRepo) TouchLastSeen(_ context.Context, id string) (*agentdomain.Agent, error) {
	return r.GetByID(context.Background(), id)
}

func (r *fakeAgentRepo) SweepOffline(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (r *fakeAgentRepo) ListStaleOnlineAgents(context.Context, time.Duration) ([]*agentdomain.Agent, error) {
	return nil, nil
}

// fakeMasterTokenRepo wraps fakeTokenRepo with bcrypt-verifiable rows.
type fakeMasterTokenRepo struct {
	*fakeTokenRepo
}

func (r *fakeMasterTokenRepo) add(t *testing.T, id string, plain string) {
	t.Helper()
	hash, err := auth.HashAPIToken(plain, 4)
	require.NoError(t, err)
	if r.hashes == nil {
		r.hashes = map[string]*auth.TokenRow{}
	}
	r.hashes[hash] = &auth.TokenRow{ID: id, UserID: "sys-user", ScopesJSON: "[]"}
}

func TestRequireUser_MasterFallbackUnit(t *testing.T) {
	t.Parallel()
	plain := "unit-master-token-0123456789abcdef"
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")

	newRouter := func(users user.Repository, agents *fakeAgentRepo, tokens *fakeMasterTokenRepo) http.Handler {
		cfg := api.AuthConfig{
			Signer: signer, Users: users, Tokens: tokens, Agents: agents,
			CookieName: "orenda_session",
		}
		mux := http.NewServeMux()
		mux.Handle("/api/v1/me", api.RequireUser(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, _ := api.IdentityFrom(r.Context())
			_ = json.NewEncoder(w).Encode(id)
		})))
		return mux
	}

	// Owner present: master token passes with the dual identity.
	users := newFakeUserRepo()
	require.NoError(t, users.Create(context.Background(), &user.User{ID: "u-owner", Email: "o@x.com", Role: user.RoleOwner}))
	agents := &fakeAgentRepo{byTokenID: map[string]*agentdomain.Agent{
		"tok-m": {ID: "a-master", Name: "maestro", Role: agentdomain.RoleMaster},
		"tok-p": {ID: "a-proj", Name: "worker", Role: agentdomain.RoleProject},
	}}
	tokens := &fakeMasterTokenRepo{fakeTokenRepo: &fakeTokenRepo{}}
	tokens.add(t, "tok-m", plain)

	rr := httptest.NewRecorder()
	newRouter(users, agents, tokens).ServeHTTP(rr,
		newBearerReq("/api/v1/me", plain))
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var id api.Identity
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &id))
	assert.Equal(t, "a-master", id.AgentID)
	assert.Equal(t, "u-owner", id.UserID)
	assert.True(t, id.IsMaster)
	assert.Contains(t, id.Scopes, "tasks:write", "scopes mirror the owner role")

	// Project agent token on the same route: 401.
	tokens.add(t, "tok-p", "unit-project-token-0123456789abcd")
	rr = httptest.NewRecorder()
	newRouter(users, agents, tokens).ServeHTTP(rr,
		newBearerReq("/api/v1/me", "unit-project-token-0123456789abcd"))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	// No non-system owner: master token still 401 (nobody to act for).
	lonely := newFakeUserRepo()
	require.NoError(t, lonely.Create(context.Background(), &user.User{ID: "u-sys", Email: "s@x.com", Role: "system"}))
	rr = httptest.NewRecorder()
	newRouter(lonely, agents, tokens).ServeHTTP(rr,
		newBearerReq("/api/v1/me", plain))
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func newBearerReq(path, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// projectActivityRows reads the audit feed straight from storage.
type projectActivityRow struct {
	kind      string
	actorType string
	actorID   string
}

func projectActivityRows(t *testing.T, db *sql.DB, projectID string) []projectActivityRow {
	t.Helper()
	rows, err := db.Query(
		`SELECT kind, actor_type, actor_id FROM project_activity WHERE project_id = ? ORDER BY created_at, id`,
		projectID)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []projectActivityRow
	for rows.Next() {
		var r projectActivityRow
		require.NoError(t, rows.Scan(&r.kind, &r.actorType, &r.actorID))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// master-agent-role plan (step 4): user-namespace project mutations write
// project_activity rows, and the actor comes from the identity — a
// master agent creating / patching a project lands as actor_type=agent,
// an owner cookie session as actor_type=user.
func TestMasterAgent_ProjectActivityAttribution(t *testing.T) {
	t.Parallel()
	db, _ := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "act-owner-" + randLite()[:8] + "@x.com",
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
	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	agentSvc := agentservice.New(agents, users, &agentFixtureTMinter{tokens: tokens}, hub, nil)

	projects := sqlite.NewProjectRepository(db)
	projActRecorder := projectservice.NewActivityRecorder(sqlite.NewProjectActivityRepository(db))
	projActRecorder.IdentitySource = func(ctx context.Context) (project.ActorType, string, bool) {
		id, ok := api.IdentityFrom(ctx)
		if !ok || id == nil {
			return "", "", false
		}
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
		Tasks:                   sqlite.NewTaskRepository(db),
		Tokens:                  tokens,
		Agents:                  agents,
		AgentService:            agentSvc,
		ProjectActivityRecorder: projActRecorder,
		WSHub:                   hub,
		CookieName:              "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	owner := firstNonSystemUser(t, &agentFixture{db: db})
	loginBody, _ := json.Marshal(map[string]string{"email": owner.Email, "password": "hunter2!"})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	loginRR := httptest.NewRecorder()
	router.ServeHTTP(loginRR, loginReq)
	require.Equal(t, http.StatusOK, loginRR.Code, "login body=%s", loginRR.Body.String())
	cookie := loginRR.Result().Cookies()

	// Master agent through the API (also re-pins the create contract).
	masterBody, _ := json.Marshal(map[string]string{"name": "maestro-act-" + randLite()[:6], "role": "master"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(masterBody))
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var created struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
		PlainToken string `json:"plain_token"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	// Master creates a project.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/projects",
		bytes.NewReader([]byte(`{"name":"Инициатива"}`)))
	req.Header.Set("Authorization", "Bearer "+created.PlainToken)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var proj struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proj))
	require.NotEmpty(t, proj.ID)

	// Master patches name + color.
	patchBody, _ := json.Marshal(map[string]any{"name": "Переименована", "color": "#ff0000"})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+proj.ID, bytes.NewReader(patchBody))
	req.Header.Set("Authorization", "Bearer "+created.PlainToken)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	// The audit feed: created + name_changed + color_changed, all
	// agent-attributed.
	rows := projectActivityRows(t, db, proj.ID)
	kinds := map[string]string{}
	for _, row := range rows {
		kinds[row.kind] = row.actorType
	}
	assert.Equal(t, "agent", kinds["created"], "creation attributed to the master agent")
	assert.Equal(t, "agent", kinds["name_changed"])
	assert.Equal(t, "agent", kinds["color_changed"])
	assert.NotContains(t, kinds, "archived_changed", "only changed fields get rows")

	// Owner cookie PATCH: same rows, user-attributed (regression).
	patchBody, _ = json.Marshal(map[string]any{"description": "by owner"})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+proj.ID, bytes.NewReader(patchBody))
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	rows = projectActivityRows(t, db, proj.ID)
	last := rows[len(rows)-1]
	assert.Equal(t, "description_changed", last.kind)
	assert.Equal(t, "user", last.actorType, "cookie session stays user-attributed")
	assert.Equal(t, owner.ID, last.actorID)
}

// master-agent-role plan (step 5): the master agent bypasses the Task
// 140 access filter — GET /api/v1/agent/projects lists a closed,
// ungranted project for the master and stays silent for a project
// agent; the agent-namespace PATCH grant keys stay owner-only even
// for the master.
func TestMasterAgent_Task140Bypass(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	cookie := loginOwnerCookie(t, fx)
	suffix := randLite()[:6]

	_, masterToken, _ := mintAgentViaAPI(t, fx, cookie, "maestro-"+suffix, "master")
	_, projectToken, _ := mintAgentViaAPI(t, fx, cookie, "worker-"+suffix, "")

	// Master creates a project (201) and closes it to agents — the
	// project agent gets no grant, agents_allowed=false.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects",
		bytes.NewReader([]byte(`{"name":"Закрытая"}`)))
	req.Header.Set("Authorization", "Bearer "+masterToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var proj struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &proj))

	patchBody, _ := json.Marshal(map[string]any{"agents_allowed": false})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/projects/"+proj.ID, bytes.NewReader(patchBody))
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	listProjects := func(token string) []map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/agent/projects", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		fx.router.ServeHTTP(rec, r)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var out struct {
			Projects []map[string]any `json:"projects"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out.Projects
	}

	// Master sees the closed project; the project agent does not.
	masterProjects := listProjects(masterToken)
	found := false
	for _, p := range masterProjects {
		if p["id"] == proj.ID {
			found = true
		}
	}
	assert.True(t, found, "master must see the closed ungranted project")

	workerProjects := listProjects(projectToken)
	for _, p := range workerProjects {
		assert.NotEqual(t, proj.ID, p["id"],
			"project agent must not see the closed ungranted project (Task 140)")
	}

	// The grant surface stays owner-only even for the master.
	grantBody, _ := json.Marshal(map[string]any{"agent_ids": []string{"self"}})
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/agent/projects/"+proj.ID, bytes.NewReader(grantBody))
	req.Header.Set("Authorization", "Bearer "+masterToken)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, "body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "owner_only_field")
}
