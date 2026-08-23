package api_test

// Phase 28.4 (polish): regression coverage for handlers_auth.go.
//
// Two contracts to keep honest:
//   1. loginHandler sets the session cookie's `Secure` attribute from
//      `deps.CookieSecure` (not the Phase 1 hardcoded `false`).
//      Loopback dev installs rely on `false` to keep the cookie
//      over plain HTTP; HTTPS installs flip it on. The handler
//      must reflect whatever the config decided.
//   2. loginHandler sets the cookie's `Expires` from `deps.JWTTTL`,
//      not the previously hardcoded 24h — otherwise the cookie's
//      lifetime drifts from the embedded JWT exp and
//      `RequireUser` silently fails on otherwise-valid sessions.
//   3. logoutHandler's MaxAge=-1 cookie carries the same `Secure`
//      as the login one. Otherwise browsers scope the deletion to
//      the non-secure cookie set and the secure one survives
//      logout — a small but real session-leak vector.
//
// We use a small in-memory `user.Repository` so the test runs
// without SQLite (the auth_test.go fakeUserRepo is close but
// doesn't store the bcrypt-hashed password needed by login).
// LoginHandlerForTest / LogoutHandlerForTest are the test seams
// exported by the api package for this purpose — see
// handlers_auth.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
)

// pwUserRepo is an in-memory user.Repository. Only Create +
// GetByEmail matter for the login path; the rest are stubs that
// satisfy the interface so we can plug the repo into
// api.Dependencies.Users without a compile error.
type pwUserRepo struct {
	users map[string]*user.User
}

func newPWRepo() *pwUserRepo { return &pwUserRepo{users: map[string]*user.User{}} }

func (r *pwUserRepo) Create(_ context.Context, u *user.User) error {
	r.users[u.Email] = u
	return nil
}
func (r *pwUserRepo) GetByEmail(_ context.Context, email string) (*user.User, error) {
	u, ok := r.users[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return u, nil
}
func (r *pwUserRepo) GetByID(_ context.Context, id string) (*user.User, error) {
	for _, u := range r.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, user.ErrNotFound
}
func (r *pwUserRepo) List(_ context.Context) ([]*user.User, error) {
	out := make([]*user.User, 0, len(r.users))
	for _, u := range r.users {
		out = append(out, u)
	}
	return out, nil
}
func (r *pwUserRepo) Update(_ context.Context, _ *user.User) error { return nil }
func (r *pwUserRepo) Delete(_ context.Context, _ string) error     { return nil }
func (r *pwUserRepo) FirstNonSystem(_ context.Context) (*user.User, error) {
	for _, u := range r.users {
		if u.Role != user.RoleSystem {
			return u, nil
		}
	}
	return nil, user.ErrNotFound
}

// loginRouter mounts only the login/logout handlers so the test
// doesn't have to stand up the rest of the API surface. The
// user repo is in-memory; the signer is real.
func loginRouter(users user.Repository, signer *auth.Signer, cookieName string, secure bool, ttl time.Duration) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/login", api.LoginHandlerForTest(&api.Dependencies{
		Users:        users,
		Signer:       signer,
		CookieName:   cookieName,
		CookieSecure: secure,
		JWTTTL:       ttl,
	}))
	mux.Handle("/api/v1/auth/logout", api.LogoutHandlerForTest(&api.Dependencies{
		CookieName:   cookieName,
		CookieSecure: secure,
	}))
	return mux
}

func seedOwner(t *testing.T, repo *pwUserRepo, password string) {
	t.Helper()
	hash, err := auth.HashPassword(password, 4) // cost 4 keeps the test fast
	require.NoError(t, err)
	u := &user.User{
		ID:           "u-owner",
		Email:        "owner@orenda.local",
		DisplayName:  "Owner",
		Role:         user.RoleOwner,
		PasswordHash: hash,
	}
	require.NoError(t, repo.Create(t.Context(), u))
}

func TestLogin_CookieAttributes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		cookieSecure  bool
		jwtTTL        time.Duration
		wantSecure    bool
		wantExpiresIn time.Duration // tolerance window: actual - expected
	}{
		{
			name:          "loopback default (Secure=false, 24h TTL — Phase 28.4 default)",
			cookieSecure:  false,
			jwtTTL:        24 * time.Hour,
			wantSecure:    false,
			wantExpiresIn: 24 * time.Hour,
		},
		{
			name:          "HTTPS install (Secure=true)",
			cookieSecure:  true,
			jwtTTL:        24 * time.Hour,
			wantSecure:    true,
			wantExpiresIn: 24 * time.Hour,
		},
		{
			name:          "operator opted into a longer session (168h legacy)",
			cookieSecure:  true,
			jwtTTL:        168 * time.Hour,
			wantSecure:    true,
			wantExpiresIn: 168 * time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			users := newPWRepo()
			seedOwner(t, users, "correct-horse")
			signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", tc.jwtTTL, "orenda")

			r := loginRouter(users, signer, "orenda_session", tc.cookieSecure, tc.jwtTTL)

			body := strings.NewReader(`{"email":"owner@orenda.local","password":"correct-horse"}`)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code, "login should succeed: %s", rr.Body.String())

			var got map[string]any
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
			assert.Equal(t, "u-owner", got["user_id"])

			cookies := rr.Result().Cookies()
			require.Len(t, cookies, 1, "login should set exactly one cookie")
			cookie := cookies[0]
			assert.Equal(t, "orenda_session", cookie.Name)
			assert.True(t, cookie.HttpOnly, "session cookie must be HttpOnly")
			assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite, "OWASP recommends Lax for cookie-based auth")
			assert.Equal(t, tc.wantSecure, cookie.Secure, "cookie.Secure must follow deps.CookieSecure")
			assert.NotEmpty(t, cookie.Value, "cookie value carries the JWT")

			// Expires: handler sets time.Now().Add(deps.JWTTTL).
			// Tolerate a small drift (test runtime).
			expectedExpiry := time.Now().Add(tc.wantExpiresIn)
			drift := cookie.Expires.Sub(expectedExpiry)
			if drift < 0 {
				drift = -drift
			}
			assert.Less(t, drift, 5*time.Second, "cookie.Expires should match JWT TTL ±5s")
		})
	}
}

func TestLogin_InvalidCredentials_Returns401(t *testing.T) {
	t.Parallel()
	// A small sanity check that the cookie attribute refactor
	// didn't break the existing error path. We don't want to
	// regress a 500 → user-disclosing leak just to gain a
	// Secure flag.
	users := newPWRepo()
	seedOwner(t, users, "correct-horse")
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", 24*time.Hour, "orenda")
	r := loginRouter(users, signer, "orenda_session", true, 24*time.Hour)

	body := strings.NewReader(`{"email":"owner@orenda.local","password":"WRONG"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	assert.Empty(t, rr.Result().Cookies(), "failed login must NOT set a cookie")
}

func TestLogout_CookieAttributes(t *testing.T) {
	t.Parallel()
	// logout doesn't need a user or signer; only the cookie
	// attributes from deps matter. Phase 28.4 contract: the
	// MaxAge=-1 cookie must carry the same Secure value as the
	// login cookie, or the deletion scopes to the wrong cookie
	// set.
	users := newPWRepo()
	seedOwner(t, users, "any-password")
	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", 24*time.Hour, "orenda")

	tests := []struct {
		name         string
		cookieSecure bool
		wantSecure   bool
	}{
		{name: "logout Secure=false matches login", cookieSecure: false, wantSecure: false},
		{name: "logout Secure=true matches login", cookieSecure: true, wantSecure: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := loginRouter(users, signer, "orenda_session", tc.cookieSecure, 24*time.Hour)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			require.Equal(t, http.StatusOK, rr.Code)
			cookies := rr.Result().Cookies()
			require.Len(t, cookies, 1)
			cookie := cookies[0]
			assert.Equal(t, tc.wantSecure, cookie.Secure, "logout cookie.Secure must match login")
			assert.Equal(t, -1, cookie.MaxAge, "logout cookie.MaxAge must be -1 to delete")
			assert.True(t, cookie.HttpOnly)
		})
	}
}
