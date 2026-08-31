package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Task 112: PATCH /api/v1/tasks/{id}/comments/{commentId} — edit own
// comment. 200 for the author, 403 for anyone else, 404 unknown
// comment, 400 empty body.

// p3LoginAs logs a user in by email and returns the session cookie.
// Task 112: the 403 case needs a second account distinct from the
// p3-owner fixture user.
func p3LoginAs(t *testing.T, router http.Handler, email string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "login failed: %s", rr.Body.String())
	for _, c := range rr.Result().Cookies() {
		if c.Name == "orenda_session" {
			return c.Value
		}
	}
	t.Fatal("no orenda_session cookie")
	return ""
}

// p3SeedSecondUser creates another user account in the fixture DB and
// returns its email (the login helper derives the password from the
// shared test password).
func p3SeedSecondUser(t *testing.T, db *sqlLite) string {
	t.Helper()
	users := sqlite.NewUserRepository(db)
	email := "other-" + randLite()[:8] + "@orenda.test"
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        email,
		PasswordHash: mustHashFast(t),
		DisplayName:  "Other",
	}))
	return email
}

func TestP3_UpdateOwnComment(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "C")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")

	rr := p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/comments", cookie,
		map[string]any{"body_md": "v1"})
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	rr = p3AuthJSON(router, http.MethodPatch,
		"/api/v1/tasks/"+taskID+"/comments/"+created.ID, cookie,
		map[string]any{"body_md": "v2"})
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var got struct {
		ID       string  `json:"id"`
		BodyMD   string  `json:"body_md"`
		EditedAt *string `json:"edited_at"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "v2", got.BodyMD)
	require.NotNil(t, got.EditedAt, "edited_at must be set after an edit")
}

func TestP3_UpdateOthersCommentForbidden(t *testing.T) {
	t.Parallel()
	router, db := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "C")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")

	rr := p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/comments", cookie,
		map[string]any{"body_md": "mine"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	otherEmail := p3SeedSecondUser(t, db)
	other := p3LoginAs(t, router, otherEmail)

	rr = p3AuthJSON(router, http.MethodPatch,
		"/api/v1/tasks/"+taskID+"/comments/"+created.ID, other,
		map[string]any{"body_md": "hijack"})
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
	assert.JSONEq(t, `{"error":"forbidden"}`, rr.Body.String())

	// The comment body is unchanged.
	rr = p3AuthGet(router, cookie, "/api/v1/tasks/"+taskID+"/comments")
	require.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Comments []struct {
			ID     string `json:"id"`
			BodyMD string `json:"body_md"`
		} `json:"comments"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	for _, c := range list.Comments {
		if c.ID == created.ID {
			assert.Equal(t, "mine", c.BodyMD, "a 403 must not modify the comment")
		}
	}
}

func TestP3_UpdateMissingCommentNotFound(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "C")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")

	rr := p3AuthJSON(router, http.MethodPatch,
		"/api/v1/tasks/"+taskID+"/comments/c-does-not-exist", cookie,
		map[string]any{"body_md": "v2"})
	require.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
	assert.JSONEq(t, `{"error":"not_found"}`, rr.Body.String())
}

func TestP3_UpdateCommentEmptyBody(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "C")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")

	rr := p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/comments", cookie,
		map[string]any{"body_md": "v1"})
	require.Equal(t, http.StatusCreated, rr.Code)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	rr = p3AuthJSON(router, http.MethodPatch,
		"/api/v1/tasks/"+taskID+"/comments/"+created.ID, cookie,
		map[string]any{"body_md": "   "})
	require.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())
	assert.JSONEq(t, `{"error":"invalid_input"}`, rr.Body.String())
}

// Task 112: the agent-side PATCH route mirrors the user contract.
func TestAgent_UpdateOwnComment(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	taskID := seedAgentCommentTask(t, fx)

	// Create an agent-authored comment first.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/comments",
		bytes.NewReader([]byte(`{"body_md":"v1"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	req = httptest.NewRequest(http.MethodPatch,
		"/api/v1/agent/tasks/"+taskID+"/comments/"+created.ID,
		bytes.NewReader([]byte(`{"body_md":"v2"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	var got struct {
		ID       string  `json:"id"`
		BodyMD   string  `json:"body_md"`
		EditedAt *string `json:"edited_at"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "v2", got.BodyMD)
	require.NotNil(t, got.EditedAt, "edited_at must be set after an edit")
}

func TestAgent_UpdateOthersCommentForbidden(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	taskID := seedAgentCommentTask(t, fx)

	// First agent authors a comment.
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/agent/tasks/"+taskID+"/comments",
		bytes.NewReader([]byte(`{"body_md":"agent a was here"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &created))

	// A second, unrelated agent tries to edit it.
	other := registerAgentForFixture(t, fx, "second-editor")
	req = httptest.NewRequest(http.MethodPatch,
		"/api/v1/agent/tasks/"+taskID+"/comments/"+created.ID,
		bytes.NewReader([]byte(`{"body_md":"hijack"}`)))
	req.Header.Set("Authorization", "Bearer "+other.PlainToken)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code, "body=%s", rr.Body.String())
	assert.JSONEq(t, `{"error":"forbidden"}`, rr.Body.String())
}

func TestAgent_UpdateMissingCommentNotFound(t *testing.T) {
	t.Parallel()
	fx := newAgentFixture(t)
	taskID := seedAgentCommentTask(t, fx)

	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/agent/tasks/"+taskID+"/comments/c-does-not-exist",
		bytes.NewReader([]byte(`{"body_md":"v2"}`)))
	req.Header.Set("Authorization", "Bearer "+fx.token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	fx.router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code, "body=%s", rr.Body.String())
	assert.JSONEq(t, `{"error":"not_found"}`, rr.Body.String())
}

// seedAgentCommentTask seeds a real task row in the agent fixture DB
// (same pattern as TestAgent_CommentCreatesAgentAuthoredComment).
func seedAgentCommentTask(t *testing.T, fx *agentFixture) string {
	t.Helper()
	row := fx.db.QueryRow("SELECT id FROM users LIMIT 1")
	var ownerID string
	require.NoError(t, row.Scan(&ownerID))
	projects := sqlite.NewProjectRepository(fx.db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{
		Name: "CommentEditTest", OwnerID: ownerID,
	})
	require.NoError(t, err)
	tasks := sqlite.NewTaskRepository(fx.db)
	tr := &task.Task{ProjectID: p.ID, ColumnID: cols[0].ID, Title: "comment edit"}
	require.NoError(t, tasks.Create(context.Background(), tr))
	return tr.ID
}

// registerAgentForFixture registers an additional agent on an
// existing agent fixture (registerSecondAgent lives in
// agent_task_manage_test.go and is bound to proposeFixture).
func registerAgentForFixture(t *testing.T, fx *agentFixture, label string) *secondAgent {
	t.Helper()
	svc := agentservice.New(
		sqlite.NewAgentRepository(fx.db),
		sqlite.NewUserRepository(fx.db),
		&agentFixtureTMinter{tokens: sqlite.NewAPITokenRepository(fx.db)},
		nil, nil,
	)
	got, err := svc.Register(context.Background(), label, []string{"test"}, "test", nil)
	require.NoError(t, err)
	return &secondAgent{ID: got.Agent.ID, PlainToken: got.PlainToken}
}
