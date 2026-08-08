package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// agentFixture bundles a router + a fresh agent + its plain token.
type agentFixture struct {
	router    http.Handler
	agentID   string
	token     string
	cookieVal string // unused but kept for future tests
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/a.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "agent-owner-" + randLite()[:8] + "@x.com",
		PasswordHash: "x",
		DisplayName:  "Owner",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	taskSvc := taskservice.New(
		sqlite.NewTaskRepository(db),
		sqlite.NewTaskLockRepository(db),
		nil, nil, hub,
	)

	tokens := sqlite.NewAPITokenRepository(db)
	agents := sqlite.NewAgentRepository(db)

	// Adapter for agentservice.TokenMinter.
	tm := &agentFixtureTMinter{tokens: tokens}
	agentSvc := agentservice.New(agents, users, tm, hub, nil)
	got, err := agentSvc.Register(context.Background(), "qwen-test", agent.TypeQwen, "test", nil)
	require.NoError(t, err)

	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    sqlite.NewProjectRepository(db),
		Tasks:       sqlite.NewTaskRepository(db),
		Tokens:      tokens,
		TaskService: taskSvc,
		Agents:      agents,
		Comments:    commentSvc,
		Activities:  sqlite.NewActivityRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	return &agentFixture{
		router:  api.NewRouter(deps),
		agentID: got.Agent.ID,
		token:   got.PlainToken,
	}
}

type agentFixtureTMinter struct{ tokens *sqlite.APITokenRepo }

func (a *agentFixtureTMinter) MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (string, string, error) {
	row, err := a.tokens.Create(ctx, userID, name, hash, scopesJSON, expiresAt)
	if err != nil {
		return "", "", err
	}
	return row.ID, row.Name, nil
}

func TestAgent_MeReturnsAgent(t *testing.T) {
	fx := newAgentFixture(t)

	t.Logf("fx.token=%q agentID=%q", fx.token, fx.agentID)

	// First, verify the route even matches the right path.
	for _, p := range []string{"/api/v1/agent/me", "/api/v1/me", "/api/v1/agent/heartbeat", "/api/v1/agent/tasks/x/claim"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		req.Header.Set("Authorization", "Bearer "+fx.token)
		rr := httptest.NewRecorder()
		fx.router.ServeHTTP(rr, req)
		t.Logf("GET %-40s -> %d %s", p, rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())

	var a struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &a))
	assert.Equal(t, fx.agentID, a.ID)
	assert.Equal(t, "qwen-test", a.Name)
}

func TestAgent_NoTokenReturns401(t *testing.T) {
	fx := newAgentFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAgent_BadTokenReturns401(t *testing.T) {
	fx := newAgentFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent/me", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAgent_HeartbeatReturnsAgent(t *testing.T) {
	fx := newAgentFixture(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", nil)
	req.Header.Set("Authorization", "Bearer "+fx.token)
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	t.Logf("heartbeat: %d %s", rr.Code, rr.Body.String())
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestAgent_ClaimRejectsNoTask(t *testing.T) {
	fx := newAgentFixture(t)

	// Send claim with a non-existent task id — should return ErrNotFound (404).
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/no-such-task/claim", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
}
