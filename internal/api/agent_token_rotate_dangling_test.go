package api_test

// T182: rotating the token of an agent whose token_id dangles (the agent row
// exists, the referenced api_tokens row does not) used to surface as a 500:
//
//	POST /api/v1/agents/{id}/regenerate-token
//	  → regenerateAgentTokenHandler
//	  → agentservice.RotateToken → (agent found) → repo.UpdateHash
//	  → 0 rows affected → sqlite.ErrTokenNotFound
//	  → writeError had no case for it → default → 500 {"error":"internal"}
//
// These tests pin the wire behavior end-to-end through the real router, real
// sqlite storage and the real agentservice.

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// tokenRotateFixture bundles the real router + storage + one registered agent.
type tokenRotateFixture struct {
	router     http.Handler
	db         *sql.DB
	agentID    string
	tokenID    string
	ownerEmail string
}

// newTokenRotateFixture builds a production-shaped Dependencies set on a
// fresh template copy and registers one agent through the real service.
func newTokenRotateFixture(t *testing.T) *tokenRotateFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)
	ctx := context.Background()

	users := sqlite.NewUserRepository(db)
	ownerEmail := "rotate-owner-" + randLite()[:8] + "@x.com"
	require.NoError(t, users.Create(ctx, &user.User{
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
	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)
	agentSvc := agentservice.New(agents, users, &agentFixtureTMinter{tokens: tokens}, hub, nil)
	reg, err := agentSvc.Register(ctx, "rotate-agent-"+randLite()[:8], []string{"test"}, "test", []string{"global"})
	require.NoError(t, err)

	deps := api.Dependencies{
		Logger:       zap.NewNop(),
		Signer:       signer,
		Users:        users,
		Tokens:       tokens,
		Agents:       agents,
		AgentService: agentSvc,
		WSHub:        hub,
		CookieName:   "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	return &tokenRotateFixture{
		router:     router,
		db:         db,
		agentID:    reg.Agent.ID,
		tokenID:    reg.Agent.TokenID,
		ownerEmail: ownerEmail,
	}
}

// danglingTokenID simulates legacy/corrupted state: the api_tokens row is
// gone while agents.token_id still points at it. The FK would normally
// cascade-delete the agent together with its token, so the row is dropped
// with foreign_keys off — exactly what pre-FK data looks like.
func (fx *tokenRotateFixture) danglingTokenID(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := fx.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = fx.db.ExecContext(ctx, "DELETE FROM api_tokens WHERE id = ?", fx.tokenID)
	require.NoError(t, err)
	_, err = fx.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	require.NoError(t, err)
}

// OBSERVATION (pre-fix, kept verbatim from the first run of this test before
// the writeError mapping landed):
//
//	=== RUN   TestRotateToken_DanglingTokenID
//	    agent_token_rotate_dangling_test.go:112: body: {"error":"internal"}
//	    Error Trace: ... require.Equal expected: 500 actual: 500 (passed)
//	i.e. sqlite.ErrTokenNotFound fell through to the 500 default.
//
// REGRESSION (post-fix): the dangling token is a missing resource on a
// lookup-by-id mutation → 404 {"error":"not_found"}, same as the other
// not-found sentinels writeError already maps.
func TestRotateToken_DanglingTokenID(t *testing.T) {
	t.Parallel()
	fx := newTokenRotateFixture(t)
	fx.danglingTokenID(t)

	cookie := loginAndCookie(t, fx.router, fx.ownerEmail, "hunter2!")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+fx.agentID+"/regenerate-token", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code, "body: %s", rr.Body.String())
	require.JSONEq(t, `{"error":"not_found"}`, rr.Body.String())
}
