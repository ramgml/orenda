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
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/service/notifier"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// notifRouter wires a router with a real notifier service so we can
// assert that claim/release/submit actually produce inbox rows for the
// project owner.
func notifRouter(t *testing.T) (http.Handler, string, *sqlLite) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/nf.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email: "nf@x.com", PasswordHash: mustHashFast(t, "hunter2!"), DisplayName: "NF",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	projRepo := sqlite.NewProjectRepository(db)
	taskRepo := sqlite.NewTaskRepository(db)
	taskSvc := taskservice.New(taskRepo, sqlite.NewTaskLockRepository(db), nil, nil, hub)

	notifSvc := notifier.New(
		sqlite.NewNotificationRepository(db),
		sqlite.NewBotSubscriptionRepository(db),
		nil, // no bots — console bot stays out of the way
		hub,
	)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := api.Dependencies{
		Logger:      zap.Must(zap.NewDevelopment()),
		Signer:      signer,
		Users:       users,
		Projects:    projRepo,
		Tasks:       taskRepo,
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		Agents:      sqlite.NewAgentRepository(db),
		Comments:    nil,
		Activities:  sqlite.NewActivityRepository(db),
		SyncOps:     sqlite.NewSyncOpsRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
		Notifier:    notifSvc,
	}
	router := api.NewRouter(&deps)

	// Login.
	body, _ := json.Marshal(map[string]string{"email": "nf@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	return router, cookie, db
}

// getNotifications fetches the current user's notifications via the
// notifier inbox. Returns the parsed JSON body as a generic map.
func getNotifications(t *testing.T, router http.Handler, cookie string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "list notifications: %s", rr.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	return out
}

// seedProjectAndTask creates a project with one task for the project
// the router is wired against. Returns the project id, task id, and
// a real agent id (needed for claim FK).
func seedProjectAndTask(t *testing.T, db *sqlLite, cookie string) (projectID, taskID, agentID string) {
	t.Helper()
	projRepo := sqlite.NewProjectRepository(db)
	taskRepo := sqlite.NewTaskRepository(db)
	agentRepo := sqlite.NewAgentRepository(db)
	tokenRepo := sqlite.NewAPITokenRepository(db)
	users := sqlite.NewUserRepository(db)

	owner, err := users.GetByEmail(context.Background(), "nf@x.com")
	require.NoError(t, err)

	p, _, _, err := projRepo.CreateProject(context.Background(), &project.Project{
		Name: "Demo", OwnerID: owner.ID, Color: "#3b82f6",
	})
	require.NoError(t, err)

	tr := &task.Task{ProjectID: p.ID, Title: "Test task", Status: task.StatusTodo}
	require.NoError(t, taskRepo.Create(context.Background(), tr))

	// Real agent row required for task_locks FK on agent_id.
	tok, err := tokenRepo.Create(context.Background(), owner.ID, "test-tok", "hash", "[]", nil)
	require.NoError(t, err)
	a := &agent.Agent{Name: "demo-agent", Type: []string{"custom"}, TokenID: tok.ID, Status: agent.StatusOnline}
	require.NoError(t, agentRepo.Create(context.Background(), a))
	return p.ID, tr.ID, a.ID
}

// When an agent claims a task via /tasks/:id/claim the project owner
// receives an in-app notification with type "task.assigned_to_me".
func TestNotify_ClaimProducesInboxRow(t *testing.T) {
	router, cookie, db := notifRouter(t)
	projectID, taskID, agentID := seedProjectAndTask(t, db, cookie)
	_ = projectID

	body, _ := json.Marshal(map[string]string{"agent_id": agentID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/claim", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "claim: %s", rr.Body.String())

	// Inbox should have one row for the owner.
	out := getNotifications(t, router, cookie)
	rows, _ := out["notifications"].([]any)
	require.NotEmpty(t, rows, "expected at least one notification")
	first := rows[0].(map[string]any)
	assert.Equal(t, "task.assigned_to_me", first["type"])
}

// claim twice with the same agent_id collapses via dedup_key.
func TestNotify_ClaimDedupesAcrossCalls(t *testing.T) {
	router, cookie, db := notifRouter(t)
	_, taskID, agentID := seedProjectAndTask(t, db, cookie)

	body, _ := json.Marshal(map[string]string{"agent_id": agentID})
	// First call succeeds.
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/claim", bytes.NewReader(body))
	req1.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr1 := httptest.NewRecorder()
	router.ServeHTTP(rr1, req1)
	require.Equal(t, http.StatusOK, rr1.Code, "claim 1: %s", rr1.Body.String())

	// Release first.
	rel, _ := json.Marshal(map[string]string{"agent_id": agentID})
	relReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/release", bytes.NewReader(rel))
	relReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr2 := httptest.NewRecorder()
	router.ServeHTTP(rr2, relReq)
	require.Equal(t, http.StatusOK, rr2.Code, "release: %s", rr2.Body.String())

	// Now release again → 409 (no lock). Notification dedup_key for
	// "task.assigned_to_me" must remain a single row, NOT be duplicated
	// on each successful claim.
	out := getNotifications(t, router, cookie)
	rows, _ := out["notifications"].([]any)
	var assigned, released int
	for _, r := range rows {
		m := r.(map[string]any)
		switch m["type"] {
		case "task.assigned_to_me":
			assigned++
		case "task.released":
			released++
		}
	}
	assert.Equal(t, 1, assigned)
	assert.Equal(t, 1, released)
}

// Submit produces a "task.review_needed" notification (already wired in
// Phase 6.4); confirm it still works after our changes.
func TestNotify_SubmitStillProducesRow(t *testing.T) {
	router, cookie, db := notifRouter(t)
	_, taskID, agentID := seedProjectAndTask(t, db, cookie)

	// Claim first so submit is happy.
	claimBody, _ := json.Marshal(map[string]string{"agent_id": agentID})
	claimReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/claim", bytes.NewReader(claimBody))
	claimReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, claimReq)
	require.Equal(t, http.StatusOK, rr.Code, "claim: %s", rr.Body.String())

	// Now submit for review.
	subBody, _ := json.Marshal(map[string]string{"agent_id": agentID, "note": "ready"})
	subReq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/submit", bytes.NewReader(subBody))
	subReq.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	subRR := httptest.NewRecorder()
	router.ServeHTTP(subRR, subReq)
	require.Equal(t, http.StatusOK, subRR.Code, "submit: %s", subRR.Body.String())

	out := getNotifications(t, router, cookie)
	rows, _ := out["notifications"].([]any)
	var reviewNeeded int
	for _, r := range rows {
		m := r.(map[string]any)
		if m["type"] == "task.review_needed" {
			reviewNeeded++
		}
	}
	assert.GreaterOrEqual(t, reviewNeeded, 1, "expected task.review_needed notification after submit")
}
