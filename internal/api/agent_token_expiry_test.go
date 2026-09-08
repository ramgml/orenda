package api_test

// T182: api_tokens.expires_at enforcement in the auth middleware.
//
// Semantics pinned here (see api.verifyAPIToken):
//   - expires_at is set and expires_at <= now → EXPIRED
//   - expires_at NULL → never expires
//   - an expired credential is indistinguishable on the wire from an
//     unknown one: the same generic 401 "unauthorized", no leak in the body.
//
// Cases (a)-(c) drive the real sqlite repo through the RequireAgent
// middleware; (d) pins the boundary with a captured, second-precision
// instant; (f) is a leak regression comparing expired vs wrong-password
// responses byte-for-byte.

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
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// expiryFixture seeds one live agent token on real sqlite and returns the
// router, the DB handle (for direct expires_at writes) and the plaintext.
type expiryFixture struct {
	router http.Handler
	db     *stdlibSQLDB
	// plain is the minted token; its bcrypt image lives in api_tokens.
	plain string
	// tokenID pins the exact api_tokens row the fixture minted, so
	// setExpiry can never miss (a name-based WHERE that matches 0 rows
	// would silently leave the old expiry in place).
	tokenID string
}

// newExpiryFixture wires the RequireAgent middleware against real
// sqlite-backed token + agent repos and registers one agent (NULL expiry).
// The token row is minted directly (not via agentservice) so the test
// controls the stored fields exactly.
func newExpiryFixture(t *testing.T) *expiryFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)
	ctx := context.Background()

	users := sqlite.NewUserRepository(db)
	owner := &user.User{
		Email:        "exp-owner-" + randLite()[:8] + "@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "Owner",
	}
	require.NoError(t, users.Create(ctx, owner))

	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	plain, err := auth.NewAPIToken()
	require.NoError(t, err)
	hash, err := auth.HashAPIToken(plain, 4)
	require.NoError(t, err)
	// NULL expiry — the (c) baseline also proves the storage layer
	// round-trips an unset expires_at as "never expires".
	row, err := tokens.Create(ctx, owner.ID, "agent:exp-"+randLite()[:8], hash, `["global"]`, nil)
	require.NoError(t, err)
	require.Nil(t, row.ExpiresAt, "mint without TTL policy must leave expires_at NULL")

	// agents.token_id has an FK to api_tokens.id — seed the agent row
	// that owns the token (RequireAgent resolves AgentID via token_id).
	a := &agent.Agent{Name: "exp-agent-" + randLite()[:8], Type: []string{"test"}, TokenID: row.ID}
	require.NoError(t, agents.Create(ctx, a))

	cfg := api.AuthConfig{
		Signer:     auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda"),
		Users:      users,
		Tokens:     tokens,
		Agents:     agents,
		CookieName: "orenda_session",
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/agent/me", api.RequireAgent(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := api.IdentityFrom(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(id)
	})))
	return &expiryFixture{router: mux, db: db, plain: plain, tokenID: row.ID}
}

// setExpiry writes expires_at directly, bypassing the mint/rotate policy —
// simulates a row whose deadline has passed (or a future one). Written in
// the same second-precision UTC layout the repo's formatTime produces.
func (fx *expiryFixture) setExpiry(t *testing.T, at *time.Time) {
	t.Helper()
	var exp any
	if at != nil {
		exp = at.UTC().Format("2006-01-02 15:04:05")
	}
	res, err := fx.db.ExecContext(context.Background(),
		`UPDATE api_tokens SET expires_at = ? WHERE id = ?`, exp, fx.tokenID)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "setExpiry must hit exactly the fixture token row")
}

// probe sends the bearer request and returns the raw recorder so callers
// can compare status AND body bytes.
func (fx *expiryFixture) probe(t *testing.T, plain string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	return rr
}

func TestAgentTokenExpiryMiddleware(t *testing.T) {
	t.Parallel()

	fx := newExpiryFixture(t)

	tests := []struct {
		name     string
		expiry   *time.Time // nil → NULL (never expires)
		wantCode int
	}{
		{name: "(a) live token, expiry in future -> 200", expiry: new(time.Now().UTC().Add(time.Hour)), wantCode: http.StatusOK},
		{name: "(b) expired token -> 401", expiry: new(time.Now().UTC().Add(-time.Hour)), wantCode: http.StatusUnauthorized},
		{name: "(c) NULL expiry -> 200", expiry: nil, wantCode: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx.setExpiry(t, tt.expiry)
			rr := fx.probe(t, fx.plain)
			require.Equal(t, tt.wantCode, rr.Code, "body: %s", rr.Body.String())
		})
	}
}

// (d) boundary: expires_at == now must already be expired (the "<= now"
// rule, not "<"). Deterministic: capture time.Now(), truncate DOWN to the
// storage precision (SQLite datetime strings are second-granular — the
// repo writes and reads "YYYY-MM-DD HH:MM:SS"), write exactly that
// instant, sleep past it, then probe. At probe time the stored deadline
// is strictly in the past, so 401 pins that a deadline equal to "now" at
// check time would be expired too (ExpiresAt.After(now) is false).
func TestAgentTokenExpiry_BoundaryExpiresAtEqualsNow(t *testing.T) {
	t.Parallel()
	fx := newExpiryFixture(t)

	// Captured BEFORE the write so the stored deadline is a known,
	// second-precision instant (storage format truncates sub-seconds).
	fx.setExpiry(t, new(time.Now().UTC().Truncate(time.Second)))
	time.Sleep(1100 * time.Millisecond)

	rr := fx.probe(t, fx.plain)
	require.Equal(t, http.StatusUnauthorized, rr.Code, "body: %s", rr.Body.String())
}

// (f) leak regression: an expired credential and a wrong password must
// produce byte-identical responses (status AND body). Any difference
// would tell an attacker the credential once existed.
func TestAgentTokenExpiry_NoLeakExpiredVsWrongPassword(t *testing.T) {
	t.Parallel()
	fx := newExpiryFixture(t)

	fx.setExpiry(t, new(time.Now().UTC().Add(-time.Hour)))
	expired := fx.probe(t, fx.plain)
	wrong := fx.probe(t, "definitely-not-a-valid-token")

	assert.Equal(t, http.StatusUnauthorized, expired.Code)
	assert.Equal(t, http.StatusUnauthorized, wrong.Code)
	assert.Equal(t, expired.Body.String(), wrong.Body.String(), "body must match byte-for-byte")
}
