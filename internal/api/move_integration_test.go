package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// moveDeps wires Dependencies + a real ws.Hub for integration tests.
func moveDeps(t *testing.T) (api.Dependencies, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "move.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        "move@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "Mover",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	repo := sqlite.NewTaskRepository(db)
	taskSvc := taskservice.New(repo, sqlite.NewTaskLockRepository(db), nil, nil, hub)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	return api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    sqlite.NewProjectRepository(db),
		Tasks:       repo,
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		WSHub:       hub,
		CookieName:  "orenda_session",
	}, "hunter2!"
}

func TestIntegration_MoveViaHandler(t *testing.T) {
	deps, _ := moveDeps(t)
	router := api.NewRouter(&deps)

	// Login.
	body, _ := json.Marshal(map[string]string{"email": "move@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	// Create project + get board.
	rr = authed(http.MethodPost, "/api/v1/projects", map[string]any{"name": "Move"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	rr = authed(http.MethodGet, "/api/v1/projects/"+p.ID+"/board", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var b struct {
		Columns []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"columns"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &b))
	require.GreaterOrEqual(t, len(b.Columns), 2)
	backlog := b.Columns[0]
	todo := b.Columns[1]

	// Create task in backlog.
	rr = authed(http.MethodPost, "/api/v1/projects/"+p.ID+"/tasks", map[string]any{
		"title": "X", "column_id": backlog.ID,
	})
	require.Equal(t, http.StatusCreated, rr.Code)
	var tr struct {
		ID       string `json:"id"`
		ColumnID string `json:"column_id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))
	require.Equal(t, backlog.ID, tr.ColumnID)

	// Move it to todo.
	rr = authed(http.MethodPost, "/api/v1/tasks/"+tr.ID+"/move", map[string]any{
		"column_id": todo.ID,
	})
	require.Equal(t, http.StatusOK, rr.Code, "move: %s", rr.Body.String())
	var moved struct {
		ID       string  `json:"id"`
		ColumnID string  `json:"column_id"`
		Position float64 `json:"position"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &moved))
	assert.Equal(t, todo.ID, moved.ColumnID)
	assert.Greater(t, moved.Position, 0.0)
}

func TestIntegration_MoveNotFoundReturns404(t *testing.T) {
	deps, _ := moveDeps(t)
	router := api.NewRouter(&deps)

	body, _ := json.Marshal(map[string]string{"email": "move@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	cookie := rr.Result().Cookies()[0].Value

	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/no-such/move", bytes.NewReader([]byte(`{"column_id":"x"}`)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestIntegration_MoveBroadcastsOnWebSocket(t *testing.T) {
	deps, plain := moveDeps(t)
	router := api.NewRouter(&deps)

	// Login to get a JWT (the WS endpoint needs ?token=...).
	body, _ := json.Marshal(map[string]string{"email": "move@x.com", "password": plain})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	loginResp := struct {
		Token string `json:"token"`
	}{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &loginResp))
	token := loginResp.Token
	cookie := rr.Result().Cookies()[0].Value

	authed := func(method, path string, body any) *httptest.ResponseRecorder {
		buf, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}

	rr = authed(http.MethodPost, "/api/v1/projects", map[string]any{"name": "WSMove"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	rr = authed(http.MethodGet, "/api/v1/projects/"+p.ID+"/board", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var b struct {
		Columns []struct {
			ID string `json:"id"`
		} `json:"columns"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &b))
	backlog := b.Columns[0]
	todo := b.Columns[1]

	rr = authed(http.MethodPost, "/api/v1/projects/"+p.ID+"/tasks", map[string]any{
		"title": "X", "column_id": backlog.ID,
	})
	require.Equal(t, http.StatusCreated, rr.Code)
	var tr struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))

	// Open WS while the server is running.
	wsSrv := httptest.NewServer(router)
	defer wsSrv.Close()
	wsURL := wsScheme(wsSrv.URL) + "/api/v1/ws?token=" + url.QueryEscape(token)
	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	defer conn.Close()

	// Drain initial frames (none expected on idle).
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	// Trigger a move.
	rr = authed(http.MethodPost, "/api/v1/tasks/"+tr.ID+"/move", map[string]any{
		"column_id": todo.ID,
	})
	require.Equal(t, http.StatusOK, rr.Code)

	// Read at least one frame within 2s.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err, "expected WS event after move")
	assert.Contains(t, string(msg), "task.moved")
}

func wsScheme(httpURL string) string {
	if len(httpURL) >= 7 && httpURL[:7] == "http://" {
		return "ws://" + httpURL[7:]
	}
	return httpURL
}
