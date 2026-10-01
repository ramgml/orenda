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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	agentdomain "github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/user"
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
