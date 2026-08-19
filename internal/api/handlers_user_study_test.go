package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/study"
	"github.com/ramgml/orenda/internal/domain/task"
	studysvc "github.com/ramgml/orenda/internal/service/study"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// buildUserStudyDeps wires a real SQLite DB + the study service
// so the user-side handler tests can drive the same code path the
// production router uses (deps.StudyService → Service.Propose /
// Accept / Dismiss / ListPending).
func buildUserStudyDeps(t *testing.T) (*Dependencies, string, string) {
	t.Helper()
	ctx := context.Background()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	require.NoError(t, sqlite.Migrate(ctx, db, sqlite.MigrationsFS, "migrations"))
	t.Cleanup(func() { _ = db.Close() })

	propRepo := sqlite.NewStudyProposalRepository(db)
	taskRepo := sqlite.NewTaskRepository(db)
	studySvc := studysvc.New(propRepo, taskRepo, nil, nil)

	deps := &Dependencies{
		StudyService: studySvc,
		Courses:      sqlite.NewCourseRepository(db),
	}

	const (
		ownerID  = "u-user"
		courseID = "c-user"
		agentID  = "a-user"
		taskID   = "t-pre-existing"
	)
	_, err = db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "user@031.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, ?, ?, '[]')`,
		"t-user", ownerID, "seed", "h")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
		agentID, "planner", "[]", "t-user")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		courseID, "Rust", ownerID)
	require.NoError(t, err)
	// Pre-existing task for the Accept→Idempotent test — we need a
	// target the second Accept can return.
	_, err = db.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, study_course_id) VALUES (?, ?, ?, ?)`,
		taskID, "Existing task", "todo", courseID)
	require.NoError(t, err)

	return deps, agentID, courseID
}

// withRouteParam sets the chi "id" route parameter on the request so
// handlers that call chi.URLParam(r, "id") can read it.
func withRouteParam2(r *http.Request, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// seedPropose posts a proposal via the service so tests don't have
// to repeat the boilerplate.
func seedPropose(t *testing.T, deps *Dependencies, agentID, courseID, title string) *study.Proposal {
	t.Helper()
	res, err := deps.StudyService.Propose(context.Background(), agentID, studysvc.ProposeInput{
		CourseID:   courseID,
		Title:      title,
		TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	return res.Proposal
}

// TestListStudyProposals_HappyPath — the Dashboard tray reads the
// pending proposals and the response shape matches what the SPA
// expects (a `proposals` key wrapping the array).
func TestListStudyProposals_HappyPath(t *testing.T) {
	deps, agentID, courseID := buildUserStudyDeps(t)
	p1 := seedPropose(t, deps, agentID, courseID, "Read chapter 5")
	p2 := seedPropose(t, deps, agentID, courseID, "Practice exercises")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/study-proposals", nil)
	w := httptest.NewRecorder()
	listStudyProposalsHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Proposals []study.Proposal `json:"proposals"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Proposals, 2)
	// Order: created_at ASC. The seeds happen in order, so p1
	// should come before p2; we don't pin exact order (clock skew)
	// but assert both ids are present.
	ids := map[string]bool{p1.ID: true, p2.ID: true}
	for _, p := range resp.Proposals {
		assert.True(t, ids[p.ID])
	}
}

// TestListStudyProposals_ExcludesResolved — only pending. After
// accept/dismiss the proposal must drop off the tray.
func TestListStudyProposals_ExcludesResolved(t *testing.T) {
	deps, agentID, courseID := buildUserStudyDeps(t)
	p1 := seedPropose(t, deps, agentID, courseID, "Will be accepted")
	p2 := seedPropose(t, deps, agentID, courseID, "Will be dismissed")
	_ = p1
	_ = p2

	// Accept p1, dismiss p2.
	_, err := deps.StudyService.Accept(context.Background(), p1.ID)
	require.NoError(t, err)
	_, err = deps.StudyService.Dismiss(context.Background(), p2.ID)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/study-proposals", nil)
	w := httptest.NewRecorder()
	listStudyProposalsHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Proposals []study.Proposal `json:"proposals"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Proposals,
		"resolved proposals (accepted + dismissed) drop off the tray")
}

// TestListStudyProposals_NoServiceWired — nil StudyService → 503.
func TestListStudyProposals_NoServiceWired(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/study-proposals", nil)
	w := httptest.NewRecorder()
	listStudyProposalsHandler(&Dependencies{}).ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestAcceptStudyProposal_HappyPath — 201 + task + proposal in the
// response body.
func TestAcceptStudyProposal_HappyPath(t *testing.T) {
	deps, agentID, courseID := buildUserStudyDeps(t)
	p := seedPropose(t, deps, agentID, courseID, "Read chapter 5")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/"+p.ID+"/accept", nil)
	r = withRouteParam2(r, p.ID)
	w := httptest.NewRecorder()
	acceptStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Proposal        study.Proposal `json:"proposal"`
		Task            task.Task      `json:"task"`
		AlreadyAccepted bool           `json:"already_accepted"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, study.StatusAccepted, resp.Proposal.Status)
	assert.NotEmpty(t, resp.Task.ID, "task row created")
	assert.Equal(t, "Read chapter 5", resp.Task.Title)
	assert.Equal(t, courseID, resp.Task.StudyCourseID)
	assert.False(t, resp.AlreadyAccepted)
}

// TestAcceptStudyProposal_Idempotent — second accept on the same
// proposal returns 200 (not 201), already_accepted=true, and the
// original task id (no duplicate row).
func TestAcceptStudyProposal_Idempotent(t *testing.T) {
	deps, agentID, courseID := buildUserStudyDeps(t)
	p := seedPropose(t, deps, agentID, courseID, "Idempotent")

	// First accept → 201.
	r := httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/"+p.ID+"/accept", nil)
	r = withRouteParam2(r, p.ID)
	w := httptest.NewRecorder()
	acceptStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code)
	var first struct {
		Task            task.Task `json:"task"`
		AlreadyAccepted bool      `json:"already_accepted"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))

	// Second accept → 200 with the original task id.
	r = httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/"+p.ID+"/accept", nil)
	r = withRouteParam2(r, p.ID)
	w = httptest.NewRecorder()
	acceptStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var second struct {
		Task            task.Task `json:"task"`
		AlreadyAccepted bool      `json:"already_accepted"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &second))
	assert.True(t, second.AlreadyAccepted)
	assert.Equal(t, first.Task.ID, second.Task.ID, "idempotent: returns the same task id")
}

// TestAcceptStudyProposal_OnDismissed_ReturnsConflict — accept
// of a dismissed proposal surfaces 409 with the proposal_resolved
// error key. This is the lifecycle guard from the service.
func TestAcceptStudyProposal_OnDismissed_ReturnsConflict(t *testing.T) {
	deps, agentID, courseID := buildUserStudyDeps(t)
	p := seedPropose(t, deps, agentID, courseID, "Dismissed first")
	_, err := deps.StudyService.Dismiss(context.Background(), p.ID)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/"+p.ID+"/accept", nil)
	r = withRouteParam2(r, p.ID)
	w := httptest.NewRecorder()
	acceptStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "proposal_resolved")
}

// TestAcceptStudyProposal_UnknownProposal — 404 with body that
// says "proposal not found".
func TestAcceptStudyProposal_UnknownProposal(t *testing.T) {
	deps, _, _ := buildUserStudyDeps(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/no-such/accept", nil)
	r = withRouteParam2(r, "no-such")
	w := httptest.NewRecorder()
	acceptStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestDismissStudyProposal_HappyPath — 200 + the (now dismissed)
// proposal.
func TestDismissStudyProposal_HappyPath(t *testing.T) {
	deps, agentID, courseID := buildUserStudyDeps(t)
	p := seedPropose(t, deps, agentID, courseID, "Skip this")

	r := httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/"+p.ID+"/dismiss", nil)
	r = withRouteParam2(r, p.ID)
	w := httptest.NewRecorder()
	dismissStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Proposal study.Proposal `json:"proposal"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, study.StatusDismissed, resp.Proposal.Status)
}

// TestDismissStudyProposal_OnAccepted_ReturnsConflict — dismiss
// of an accepted proposal is the symmetric lifecycle guard.
func TestDismissStudyProposal_OnAccepted_ReturnsConflict(t *testing.T) {
	deps, agentID, courseID := buildUserStudyDeps(t)
	p := seedPropose(t, deps, agentID, courseID, "Accepted first")
	_, err := deps.StudyService.Accept(context.Background(), p.ID)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/"+p.ID+"/dismiss", nil)
	r = withRouteParam2(r, p.ID)
	w := httptest.NewRecorder()
	dismissStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "proposal_resolved")
}

// TestDismissStudyProposal_UnknownProposal — 404.
func TestDismissStudyProposal_UnknownProposal(t *testing.T) {
	deps, _, _ := buildUserStudyDeps(t)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/study-proposals/no-such/dismiss", nil)
	r = withRouteParam2(r, "no-such")
	w := httptest.NewRecorder()
	dismissStudyProposalHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}
