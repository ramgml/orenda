package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
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
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/attachment"
	"github.com/ramgml/orenda/internal/domain/user"
	agentservice "github.com/ramgml/orenda/internal/service/agent"
	attachmentsvc "github.com/ramgml/orenda/internal/service/attachment"
	commentservice "github.com/ramgml/orenda/internal/service/comment"
	taskservice "github.com/ramgml/orenda/internal/service/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// attachmentTestAdapter bridges *attachmentsvc.Service → api.AttachmentService
// for tests (the production wiring in cmd/orenda has the same bridge).
type attachmentTestAdapter struct{ inner *attachmentsvc.Service }

func (a attachmentTestAdapter) StoreFromBytes(
	ctx context.Context,
	t attachment.TargetType,
	targetID, filename, mime string,
	uploaderType attachment.UploaderType,
	uploaderID string,
	body io.Reader,
) (*api.AttachmentResult, error) {
	res, err := a.inner.StoreFromBytes(ctx, t, targetID, filename, mime, uploaderType, uploaderID, body)
	if err != nil {
		return nil, err
	}
	return &api.AttachmentResult{Attachment: res.Attachment, Duplicate: res.Duplicate}, nil
}

func (a attachmentTestAdapter) Get(ctx context.Context, id string) (*attachment.Attachment, error) {
	return a.inner.Get(ctx, id)
}

func (a attachmentTestAdapter) ListByTarget(ctx context.Context, t attachment.TargetType, targetID string) ([]*attachment.Attachment, error) {
	return a.inner.ListByTarget(ctx, t, targetID)
}

func (a attachmentTestAdapter) ListByProject(ctx context.Context, projectID string) ([]*attachment.ProjectAttachment, error) {
	return a.inner.ListByProject(ctx, projectID)
}

func (a attachmentTestAdapter) Delete(ctx context.Context, id string) error {
	return a.inner.Delete(ctx, id)
}

func (a attachmentTestAdapter) Open(
	ctx context.Context,
	id string,
) (*attachment.Attachment, api.ReadSeekCloser, error) {
	return a.inner.Open(ctx, id)
}

// adapterForTokens bridges sqlite.APITokenRepo to the agent.TokenMinter
// interface (production wiring lives in cmd/orenda).
type adapterForTokens struct{ inner *sqlite.APITokenRepo }

var _ = (*sqlite.APITokenRepo)(nil) // ensure alias reachable

func (a adapterForTokens) MintToken(ctx context.Context, userID, name, hash, scopesJSON string, expiresAt *time.Time) (string, string, error) {
	row, err := a.inner.Create(ctx, userID, name, hash, scopesJSON, expiresAt)
	if err != nil {
		return "", "", err
	}
	return row.ID, row.Name, nil
}

// activityRecorderAdapter lets the test router record task-activity
// rows the same way production does. Without it the Phase 11 project
// activity feed is empty.
//
// Production taskservice emits some events with an empty ActorID
// (Service.Move today), but the activity model requires it. Substitute
// a sentinel so the row makes it into the table.
type activityRecorderAdapter struct{ repo activity.Repository }

func (a activityRecorderAdapter) Record(ctx context.Context, taskID string, actorType activity.ActorType, actorID string, action activity.Action, payload string) error {
	if actorID == "" {
		actorID = "unknown"
	}
	return a.repo.Create(ctx, &activity.Activity{
		TaskID:    taskID,
		ActorType: actorType,
		ActorID:   actorID,
		Action:    action,
		Payload:   payload,
	})
}

const p3Email = "p3-owner@orenda.test"

// buildP3Router wires a fresh router with the full Phase 3 dependency set.
//
// Returns the router and the *sql.DB handle so tests can seed extra rows
// (real agents, real tasks for FK satisfaction).
func buildP3Router(t *testing.T) (http.Handler, *sqlLite) {
	t.Helper()
	db, dir := copyTemplateDB(t)

	users := sqlite.NewUserRepository(db)
	require.NoError(t, users.Create(context.Background(), &user.User{
		Email:        p3Email,
		PasswordHash: mustHashFast(t),
		DisplayName:  "P3",
	}))

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	commentSvc := commentservice.New(sqlite.NewCommentRepository(db), hub, nil)
	activityRepo := sqlite.NewActivityRepository(db)
	// activityRecorderAdapter writes task-activity rows the same way
	// production does (via the SQLite activity repo). Without this
	// the Phase 11 project activity tab sees zero rows even after
	// creating tasks and comments.
	recorder := activityRecorderAdapter{repo: activityRepo}
	taskSvc := taskservice.New(
		sqlite.NewTaskRepository(db),
		sqlite.NewTaskLockRepository(db),
		recorder, nil, hub,
	)

	deps := api.Dependencies{
		Logger:      zap.NewNop(),
		Signer:      signer,
		Users:       users,
		Projects:    sqlite.NewProjectRepository(db),
		Tasks:       sqlite.NewTaskRepository(db),
		Tokens:      sqlite.NewAPITokenRepository(db),
		TaskService: taskSvc,
		Agents:      sqlite.NewAgentRepository(db),
		AgentService: agentservice.New(
			sqlite.NewAgentRepository(db),
			users,
			&adapterForTokens{inner: sqlite.NewAPITokenRepository(db)},
			hub, nil,
		),
		Comments:    commentSvc,
		Attachments: nil,
		Activities:  activityRepo,
		SyncOps:     sqlite.NewSyncOpsRepository(db),
		WSHub:       hub,
		CookieName:  "orenda_session",
	}
	uploadsDir := filepath.Join(dir, "uploads")
	_ = os.MkdirAll(uploadsDir, 0o755)
	deps.Attachments = attachmentTestAdapter{inner: attachmentsvc.New(
		sqlite.NewAttachmentRepository(db), attachmentsvc.Config{
			UploadDir:    uploadsDir,
			MaxSizeBytes: 1 << 20,
			AllowedMimes: []string{"text/*"},
		}, hub)}
	r := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)
	return r, db
}

// sqlLite aliases *sql.DB.
type sqlLite = stdlibSQLDB

type stdlibSQLDB = sql.DB

// p3Login performs /auth/login and returns the session cookie value.
func p3Login(t *testing.T, router http.Handler) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": p3Email, "password": "hunter2!"})
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

// p3AuthGet issues an authenticated GET.
func p3AuthGet(router http.Handler, cookie, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// p3AuthJSON issues an authenticated JSON request.
func p3AuthJSON(router http.Handler, method, path, cookie string, body any) *httptest.ResponseRecorder {
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// p3SeedAgent creates a real agent row and returns its id.
func p3SeedAgent(t *testing.T, db *sqlLite, label string) string {
	t.Helper()
	tokens := sqlite.NewAPITokenRepository(db)
	users := sqlite.NewUserRepository(db)
	u, err := users.GetByEmail(context.Background(), p3Email)
	require.NoError(t, err)
	tok, err := tokens.Create(context.Background(), u.ID, "tok-"+label, "fakehash", "[]", nil)
	require.NoError(t, err)
	a := &agent.Agent{Name: label, Type: []string{"qwen"}, TokenID: tok.ID}
	agents := sqlite.NewAgentRepository(db)
	require.NoError(t, agents.Create(context.Background(), a))
	return a.ID
}

// p3SeedProject creates a project + first default column and returns
// {projectID, columnID}.
func p3SeedProject(t *testing.T, router http.Handler, cookie, name string) (string, string) {
	t.Helper()
	rr := p3AuthJSON(router, http.MethodPost, "/api/v1/projects", cookie, map[string]any{"name": name})
	require.Equal(t, http.StatusCreated, rr.Code)
	var p struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &p))

	rr = p3AuthGet(router, cookie, "/api/v1/projects/"+p.ID+"/board")
	require.Equal(t, http.StatusOK, rr.Code)
	var b struct {
		Columns []struct {
			ID string `json:"id"`
		} `json:"columns"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &b))
	require.NotEmpty(t, b.Columns)
	return p.ID, b.Columns[0].ID
}

// p3SeedTask creates a task and returns its id.
func p3SeedTask(t *testing.T, router http.Handler, cookie, projectID, columnID, title string) string {
	t.Helper()
	rr := p3AuthJSON(router, http.MethodPost,
		"/api/v1/projects/"+projectID+"/tasks", cookie,
		map[string]any{"title": title, "column_id": columnID})
	require.Equal(t, http.StatusCreated, rr.Code)
	var tr struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))
	return tr.ID
}

// ---------- tests ----------

func TestP3_ListAgents(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)

	rr := p3AuthGet(router, cookie, "/api/v1/agents")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestP3_CreateAgent(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)

	rr := p3AuthJSON(router, http.MethodPost, "/api/v1/agents", cookie,
		map[string]any{"name": "qwen-" + randLite()[:6], "type": []string{"qwen"}})
	assert.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
}

func TestP3_CommentsAndActivity(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "C")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")

	// Comment
	rr := p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/comments", cookie,
		map[string]any{"body_md": "hello @user:owner"})
	assert.Equal(t, http.StatusCreated, rr.Code)

	// List
	rr = p3AuthGet(router, cookie, "/api/v1/tasks/"+taskID+"/comments")
	assert.Equal(t, http.StatusOK, rr.Code)
	var list struct {
		Comments []map[string]any `json:"comments"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	assert.GreaterOrEqual(t, len(list.Comments), 1)

	// Activity
	rr = p3AuthGet(router, cookie, "/api/v1/tasks/"+taskID+"/activity")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestP3_AttachmentUpload(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "A")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// CreatePart lets us set the per-part Content-Type; CreateFormFile
	// doesn't expose that knob and picks up "application/octet-stream"
	// for ".txt" when no Go-side MIME registry entry is present.
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", `form-data; name="file"; filename="hello.txt"`)
	hdr.Set("Content-Type", "text/plain")
	file, _ := mw.CreatePart(hdr)
	_, _ = file.Write([]byte("hello attachment"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/tasks/"+taskID+"/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "orenda_session", Value: cookie})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusCreated, rr.Code, "body=%s", rr.Body.String())
}

func TestP3_TaskContext(t *testing.T) {
	t.Parallel()
	router, _ := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "Ctx")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")

	rr := p3AuthGet(router, cookie, "/api/v1/tasks/"+taskID+"/context")
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestP3_ClaimReleaseSubmitReview(t *testing.T) {
	t.Parallel()
	router, db := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "Cl")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")
	agentID := p3SeedAgent(t, db, "claim1")

	// Claim
	rr := p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/claim", cookie,
		map[string]any{"agent_id": agentID})
	assert.Equal(t, http.StatusOK, rr.Code, "claim body=%s", rr.Body.String())

	// Release
	rr = p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/release", cookie,
		map[string]any{"agent_id": agentID})
	assert.Equal(t, http.StatusOK, rr.Code)

	// Submit
	rr = p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/submit", cookie,
		map[string]any{"agent_id": agentID, "note": "ready"})
	assert.Equal(t, http.StatusOK, rr.Code)

	// Approve
	rr = p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/review", cookie,
		map[string]any{"decision": "approve"})
	assert.Equal(t, http.StatusOK, rr.Code)
	var tr struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &tr))
	assert.Equal(t, "done", tr.Status)
}

// TestP3_ReviewWithoutCommentReturnsBadRequest pins Phase 30.17: a
// reject with no comment must surface as 400 invalid_input, not 500
// internal. Service-level validation (TaskService.Review) returns
// taskservice.ErrInvalidInput; the handler defers to writeError;
// writeError must map that service-package sentinel to 400. Before
// Phase 30.17, the map only listed domain.ErrInvalidInput variants,
// so the service-package sentinel fell through to the default 500.
// We also test bogus decision, which goes through the same code path
// on the service side (ErrInvalidInput for "decision != approve and
// != reject").
func TestP3_ReviewWithoutCommentReturnsBadRequest(t *testing.T) {
	t.Parallel()
	router, db := buildP3Router(t)
	cookie := p3Login(t, router)
	p, col := p3SeedProject(t, router, cookie, "Rev")
	taskID := p3SeedTask(t, router, cookie, p, col, "t")
	agentID := p3SeedAgent(t, db, "rev1")

	// Drive the task to status=review so the reject path is valid
	// from a state-machine perspective.
	rr := p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/claim", cookie,
		map[string]any{"agent_id": agentID})
	require.Equal(t, http.StatusOK, rr.Code, "claim")
	rr = p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/submit", cookie,
		map[string]any{"agent_id": agentID})
	require.Equal(t, http.StatusOK, rr.Code, "submit")

	// 1) reject without comment → 400 invalid_input.
	rr = p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/review", cookie,
		map[string]any{"decision": "reject"})
	assert.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "invalid_input", resp["error"])

	// 2) reject with whitespace-only comment → still 400.
	rr = p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/review", cookie,
		map[string]any{"decision": "reject", "comment": "   \n  "})
	assert.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())

	// 3) bogus decision → 400 (service rejects "approve" != "reject").
	rr = p3AuthJSON(router, http.MethodPost,
		"/api/v1/tasks/"+taskID+"/review", cookie,
		map[string]any{"decision": "bogus"})
	assert.Equal(t, http.StatusBadRequest, rr.Code, "body=%s", rr.Body.String())
}

// randLite returns a UUID-shaped random-ish string (avoids google/uuid import).
func randLite() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 36)
	for i := range b {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			b[i] = '-'
			continue
		}
		b[i] = hex[(i*7)%16]
	}
	return string(b)
}
