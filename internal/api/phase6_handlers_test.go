package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/bot"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	notifierservice "github.com/ramgml/orenda/internal/service/notifier"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// p6Email for Phase 6 tests.
const p6Email = "p6-owner@orenda.test"

// p6Fixture returns a router with the full Phase 6 dependency set.
func p6Fixture(t *testing.T) (http.Handler, *sqlLite) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/p6.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	_ = os.MkdirAll(filepath.Join(dir, "uploads"), 0o755)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        p6Email,
		PasswordHash: mustHashFast(t),
		DisplayName:  "P6",
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
	agentSvc := agentservice.New(agents, users, &p6TokenMinter{tokens: tokens}, hub, nil)

	inbox := sqlite.NewNotificationRepository(db)
	subs := sqlite.NewBotSubscriptionRepository(db)
	reg := bot.NewRegistry()
	reg.Register(bot.Console{Out: io.Discard})
	notifierSvc := notifierservice.New(inbox, subs, reg, hub)

	deps := api.Dependencies{
		Logger:       zap.NewNop(),
		Signer:       signer,
		Users:        users,
		Projects:     sqlite.NewProjectRepository(db),
		Tasks:        sqlite.NewTaskRepository(db),
		Tokens:       tokens,
		TaskService:  taskSvc,
		Agents:       agents,
		AgentService: agentSvc,
		Comments:     commentSvc,
		Attachments:  nil,
		Activities:   sqlite.NewActivityRepository(db),
		WSHub:        hub,
		Notifier:     notifierSvc,
		CookieName:   "orenda_session",
	}
	r := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)
	return r, db
}

// p6TokenMinter adapts the tokens repo to agentservice.TokenMinter.
type p6TokenMinter struct{ tokens *sqlite.APITokenRepo }

func (a *p6TokenMinter) MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (string, string, error) {
	row, err := a.tokens.Create(ctx, userID, name, hash, scopesJSON, expiresAt)
	if err != nil {
		return "", "", err
	}
	return row.ID, row.Name, nil
}

// p6Login returns the session cookie.
func p6Login(t *testing.T, router http.Handler) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": p6Email, "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	return rr.Result().Cookies()[0].Value
}

// p6AuthJSON sends an authenticated JSON POST request.
func p6AuthJSON(router http.Handler, path, cookie string, body any) *httptest.ResponseRecorder {
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// p6AuthGet issues an authenticated GET.
func p6AuthGet(router http.Handler, cookie, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// p6SeedAgent creates a real agent and returns its id.
func p6SeedAgent(t *testing.T, router http.Handler, cookie, name string) string {
	t.Helper()
	rr := p6AuthJSON(router, "/api/v1/agents", cookie,
		map[string]any{"name": name, "type": []string{"qwen"}})
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var resp struct {
		Agent struct {
			ID string `json:"id"`
		} `json:"agent"`
		PlainToken string `json:"plain_token"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	return resp.Agent.ID
}

// p6SeedTask creates a project + task and returns the task id.
func p6SeedTask(t *testing.T, router http.Handler, cookie string) string {
	t.Helper()
	rr := p6AuthJSON(router, "/api/v1/projects", cookie,
		map[string]any{"name": "P6"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	rr = p6AuthGet(router, cookie, "/api/v1/projects/"+p.ID+"/board")
	require.Equal(t, http.StatusOK, rr.Code)
	var b struct {
		Columns []struct {
			ID string `json:"id"`
		} `json:"columns"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &b))
	require.NotEmpty(t, b.Columns)

	rr = p6AuthJSON(router, "/api/v1/projects/"+p.ID+"/tasks", cookie,
		map[string]any{"title": "t", "column_id": b.Columns[0].ID})
	require.Equal(t, http.StatusCreated, rr.Code)
	var tr struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))
	return tr.ID
}

func TestP6_SubmitNotifiesOwner(t *testing.T) {
	router, _ := p6Fixture(t)
	cookie := p6Login(t, router)

	taskID := p6SeedTask(t, router, cookie)

	// Seed an agent so Submit's TaskService can bind it.
	agentID := p6SeedAgent(t, router, cookie, "p6-notify")

	// Submit via cookie-authenticated user (Phase 3 handler).
	rr := p6AuthJSON(router, "/api/v1/tasks/"+taskID+"/submit", cookie,
		map[string]any{"agent_id": agentID, "note": "ready"})
	require.Equal(t, http.StatusOK, rr.Code, "submit body=%s", rr.Body.String())

	// The owner should now have a notification in the inbox.
	rr = p6AuthGet(router, cookie, "/api/v1/notifications")
	require.Equal(t, http.StatusOK, rr.Code)
	var out struct {
		Notifications []struct {
			Type       string `json:"type"`
			TargetType string `json:"target_type"`
			TargetID   string `json:"target_id"`
		} `json:"notifications"`
		Unread int `json:"unread"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, 1, out.Unread)
	require.Len(t, out.Notifications, 1)
	assert.Equal(t, "task.review_needed", out.Notifications[0].Type)
	assert.Equal(t, taskID, out.Notifications[0].TargetID)
}

func TestP6_MentionNotifiesMentionedUser(t *testing.T) {
	router, db := p6Fixture(t)
	cookie := p6Login(t, router)

	// Add a second user (the mentioned one) — they should get the
	// notification even though they didn't write the comment.
	users := sqlite.NewUserRepository(db)
	other := &user.User{
		Email:        "other-" + p6Email,
		PasswordHash: "x",
		DisplayName:  "Other",
	}
	require.NoError(t, users.Create(context.Background(), other))

	taskID := p6SeedTask(t, router, cookie)

	// Write a comment mentioning the other user by id.
	rr := p6AuthJSON(router, "/api/v1/tasks/"+taskID+"/comments", cookie,
		map[string]any{"body_md": "Hello @user:" + other.ID})
	require.Equal(t, http.StatusCreated, rr.Code)

	// The other user's inbox should have one notification.
	inbox := sqlite.NewNotificationRepository(db)
	count, err := inbox.UnreadCount(context.Background(), other.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "mentioned user should have 1 unread")

	list, err := inbox.ListByUser(context.Background(), other.ID, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "mention.created", list[0].Type)
}

func TestP6_SubmitDedupes(t *testing.T) {
	router, _ := p6Fixture(t)
	cookie := p6Login(t, router)

	taskID := p6SeedTask(t, router, cookie)
	agentID := p6SeedAgent(t, router, cookie, "p6-dedup")

	for i := 0; i < 3; i++ {
		rr := p6AuthJSON(router, "/api/v1/tasks/"+taskID+"/submit", cookie,
			map[string]any{"agent_id": agentID})
		require.Equal(t, http.StatusOK, rr.Code)
	}

	rr := p6AuthGet(router, cookie, "/api/v1/notifications")
	require.Equal(t, http.StatusOK, rr.Code)
	var out struct {
		Unread int `json:"unread"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, 1, out.Unread, "3 submits deduped into 1 unread")
}

func TestP6_MarkNotificationRead(t *testing.T) {
	router, _ := p6Fixture(t)
	cookie := p6Login(t, router)

	taskID := p6SeedTask(t, router, cookie)
	agentID := p6SeedAgent(t, router, cookie, "p6-read")

	p6AuthJSON(router, "/api/v1/tasks/"+taskID+"/submit", cookie,
		map[string]any{"agent_id": agentID})

	rr := p6AuthGet(router, cookie, "/api/v1/notifications")
	require.Equal(t, http.StatusOK, rr.Code)
	var out struct {
		Notifications []struct {
			ID string `json:"id"`
		} `json:"notifications"`
		Unread int `json:"unread"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.Equal(t, 1, out.Unread)

	notifID := out.Notifications[0].ID
	rr = p6AuthJSON(router, "/api/v1/notifications/"+notifID+"/read", cookie, nil)
	assert.Equal(t, http.StatusNoContent, rr.Code)

	rr = p6AuthGet(router, cookie, "/api/v1/notifications")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Equal(t, 0, out.Unread)
}
