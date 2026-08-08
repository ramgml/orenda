package api_test

import (
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
	"github.com/ramgml/orenda/internal/domain/user"
)

// fakeUserRepo is an in-memory user.Repository for middleware tests.
type fakeUserRepo struct {
	users  map[string]*user.User
	emails map[string]string // email -> id
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{users: map[string]*user.User{}, emails: map[string]string{}}
}

func (r *fakeUserRepo) Create(_ context.Context, u *user.User) error {
	if _, exists := r.emails[u.Email]; exists {
		return user.ErrEmailTaken
	}
	if u.ID == "" {
		u.ID = "user-" + u.Email
	}
	r.users[u.ID] = u
	r.emails[u.Email] = u.ID
	return nil
}

func (r *fakeUserRepo) GetByID(_ context.Context, id string) (*user.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, user.ErrNotFound
	}
	return u, nil
}

func (r *fakeUserRepo) GetByEmail(_ context.Context, email string) (*user.User, error) {
	id, ok := r.emails[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return r.users[id], nil
}

func (r *fakeUserRepo) Update(_ context.Context, u *user.User) error {
	r.users[u.ID] = u
	return nil
}

func (r *fakeUserRepo) Delete(_ context.Context, id string) error {
	if _, ok := r.users[id]; !ok {
		return user.ErrNotFound
	}
	delete(r.users, id)
	return nil
}

// fakeTokenRepo satisfies api.APITokenLookup for tests.
type fakeTokenRepo struct {
	hashes map[string]*api.TokenRow
}

func (r *fakeTokenRepo) GetByID(_ context.Context, id string) (*api.TokenRow, error) {
	for _, t := range r.hashes {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, assertAnError()
}

func (r *fakeTokenRepo) ListAllHashes(_ context.Context) (map[string]api.TokenRow, error) {
	out := make(map[string]api.TokenRow, len(r.hashes))
	for k, v := range r.hashes {
		out[k] = *v
	}
	return out, nil
}

func (r *fakeTokenRepo) TouchLastUsed(_ context.Context, id string) error {
	for _, t := range r.hashes {
		if t.ID == id {
			return nil
		}
	}
	return nil
}

func assertAnError() error {
	return errTokenNotFound
}

var errTokenNotFound = fakeErr("token not found")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

// helper: build a router with auth wired and a tiny handler that echoes
// the identity.
func buildAuthRouter(users user.Repository, tokens api.APITokenLookup, signer *auth.Signer) http.Handler {
	mux := http.NewServeMux()
	cfg := api.AuthConfig{
		Signer:     signer,
		Users:      users,
		Tokens:     tokens,
		CookieName: "orenda_session",
	}
	mux.Handle("/api/v1/me", api.RequireUser(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := api.IdentityFrom(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(id)
	})))
	mux.Handle("/api/v1/agent", api.RequireAgent(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := api.IdentityFrom(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(id)
	})))
	return mux
}

func TestRequireUser_AcceptsValidJWT(t *testing.T) {
	users := newFakeUserRepo()
	u := &user.User{ID: "u-1", Email: "a@b.c", Role: user.RoleOwner}
	_ = users.Create(context.Background(), u)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tok, err := signer.Issue(u.ID, u.Email, string(u.Role))
	require.NoError(t, err)

	r := buildAuthRouter(users, &fakeTokenRepo{}, signer)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got api.Identity
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, "u-1", got.UserID)
	assert.Contains(t, got.Scopes, "tasks:write")
}

func TestRequireUser_RejectsExpired(t *testing.T) {
	users := newFakeUserRepo()
	u := &user.User{ID: "u-1", Email: "a@b.c", Role: user.RoleOwner}
	_ = users.Create(context.Background(), u)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", -time.Second, "orenda")
	tok, _ := signer.Issue(u.ID, u.Email, string(u.Role))

	r := buildAuthRouter(users, &fakeTokenRepo{}, signer)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireUser_AcceptsCookie(t *testing.T) {
	users := newFakeUserRepo()
	u := &user.User{ID: "u-1", Email: "a@b.c", Role: user.RoleOwner}
	_ = users.Create(context.Background(), u)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	tok, _ := signer.Issue(u.ID, u.Email, string(u.Role))

	r := buildAuthRouter(users, &fakeTokenRepo{}, signer)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: tok})
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestRequireScope(t *testing.T) {
	// A trivial middleware that injects a fixed identity — used so
	// RequireScope can be exercised without wiring the full cookie/Bearer
	// flow.
	injectID := func(id *api.Identity) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(api.WithIdentity(r.Context(), id))
				next.ServeHTTP(w, r)
			})
		}
	}

	id := &api.Identity{UserID: "u", Scopes: []string{"tasks:read"}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Missing scope: 403.
	wrapped := injectID(id)(api.RequireScope("settings:write")(handler))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)

	// Present scope: 200.
	wrapped2 := injectID(id)(api.RequireScope("tasks:read")(handler))
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr2 := httptest.NewRecorder()
	wrapped2.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusOK, rr2.Code)

	// No identity at all: 401.
	wrapped3 := api.RequireScope("tasks:read")(handler)
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	rr3 := httptest.NewRecorder()
	wrapped3.ServeHTTP(rr3, req3)
	assert.Equal(t, http.StatusUnauthorized, rr3.Code)
}
