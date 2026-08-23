package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coursedomain "github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/study"
	studysvc "github.com/ramgml/orenda/internal/service/study"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// buildAgentStudyDeps wires a real SQLite DB + identity-bearing
// router under /agent/study-proposals and /agent/courses/{id} so
// tests can drive the same auth path the production binary uses
// (RequireAgent middleware + chi URL params).
func buildAgentStudyDeps(t *testing.T) (*Dependencies, string, string) {
	t.Helper()
	db := copyInternalTemplateDB(t)
	ctx := context.Background()

	// Study repo + task repo + course repo all on the same DB.
	propRepo := sqlite.NewStudyProposalRepository(db)
	taskRepo := sqlite.NewTaskRepository(db)
	studySvc := studysvc.New(propRepo, taskRepo, nil /* no hub */, nil /* no recorder */)

	deps := &Dependencies{
		Users:        nil, // not needed for the agent routes under test
		StudyService: studySvc,
		Courses:      sqlite.NewCourseRepository(db),
	}

	const (
		ownerID  = "u-as"
		tokID    = "t-as"
		agentID  = "a-as"
		courseID = "c-as"
	)
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		ownerID, "as@031.local", "x", "U")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, ?, ?, '[]')`,
		tokID, ownerID, "seed", "h")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
		agentID, "planner", "[]", tokID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status, pace_notes_md) VALUES (?, ?, ?, 'active', ?)`,
		courseID, "Rust", ownerID, "initial notes",
	)
	require.NoError(t, err)

	return deps, agentID, courseID
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// seedAuthIdentity stamps the agent identity into ctx so handlers
// can read it via IdentityFrom. The production RequireAgent sets
// it from the bearer token; tests construct the same context
// directly because exercising the full middleware here would
// just re-test the auth wiring.
func seedAuthIdentity(r *http.Request, agentID string) *http.Request {
	return r.WithContext(WithIdentity(r.Context(), &Identity{AgentID: agentID}))
}

// withRouteParam sets the chi "id" route parameter on the request
// so handlers that call chi.URLParam(r, "id") can read it. The router
// normally does this; bypassing the router in a direct handler test
// means we have to mint a RouteContext ourselves.
func withRouteParam(r *http.Request, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// TestAgentStudyPropose_HappyPath: the planner posts a proposal,
// the handler stamps agent_id, returns 201 + proposal.
func TestAgentStudyPropose_HappyPath(t *testing.T) {
	t.Parallel()
	deps, agentID, _ := buildAgentStudyDeps(t)

	body, _ := json.Marshal(map[string]string{
		"title":       "Read chapter 5",
		"target_date": "2099-08-17",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/study-proposals", bytes.NewReader(body))
	r = seedAuthIdentity(r, agentID)
	w := httptest.NewRecorder()

	proposeStudyHandlerAgent(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())

	var p study.Proposal
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	assert.Equal(t, agentID, p.CreatedByAgent)
	assert.Equal(t, "Read chapter 5", p.Title)
	assert.Equal(t, study.StatusPending, p.Status)
	assert.Equal(t, "2099-08-17", p.TargetDate)
}

// TestAgentStudyPropose_MissingFields: title or target_date
// empty → 400 with a stable error key.
func TestAgentStudyPropose_MissingFields(t *testing.T) {
	t.Parallel()
	deps, agentID, _ := buildAgentStudyDeps(t)
	cases := []struct {
		name string
		body map[string]string
	}{
		{"empty title", map[string]string{"title": "", "target_date": "2099-08-17"}},
		{"empty target_date", map[string]string{"title": "Read"}},
		{"missing both", map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/study-proposals", bytes.NewReader(body))
			r = seedAuthIdentity(r, agentID)
			w := httptest.NewRecorder()
			proposeStudyHandlerAgent(deps).ServeHTTP(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "missing_title_or_target_date")
		})
	}
}

// TestAgentStudyPropose_NoIdentity: handler is reached without
// agent_id in the context → 401 (defence in depth — the
// middleware should reject first, but handlers must guard).
func TestAgentStudyPropose_NoIdentity(t *testing.T) {
	t.Parallel()
	deps, _, _ := buildAgentStudyDeps(t)
	body, _ := json.Marshal(map[string]string{
		"title": "Read", "target_date": "2099-08-17",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/study-proposals", bytes.NewReader(body))
	w := httptest.NewRecorder()
	proposeStudyHandlerAgent(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAgentStudyPropose_NoServiceWired: deps.StudyService nil →
// 503 so partial-router fixtures (some routes only) report a
// useful error instead of crashing.
func TestAgentStudyPropose_NoServiceWired(t *testing.T) {
	t.Parallel()
	deps := &Dependencies{} // no StudyService
	body, _ := json.Marshal(map[string]string{
		"title": "Read", "target_date": "2099-08-17",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/study-proposals", bytes.NewReader(body))
	r = seedAuthIdentity(r, "a-no-service")
	w := httptest.NewRecorder()
	proposeStudyHandlerAgent(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestPatchCoursePaceNotes_HappyPath: the planner writes pace
// notes through the narrow PATCH; the row returns with the trimmed
// value + 200.
func TestPatchCoursePaceNotes_HappyPath(t *testing.T) {
	t.Parallel()
	deps, _, courseID := buildAgentStudyDeps(t)

	body, _ := json.Marshal(map[string]string{
		"pace_notes_md": "3 times a week, mornings",
	})
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/courses/"+courseID, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = seedAuthIdentity(r, "a-planner")
	r = withRouteParam(r, courseID)
	w := httptest.NewRecorder()

	patchCoursePaceNotesHandlerAgent(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var c coursedomain.Course
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &c))
	assert.Equal(t, "3 times a week, mornings", c.PaceNotesMD,
		"handler reads back the trimmed, validated value")
}

// TestPatchCoursePaceNotes_Trims: leading/trailing whitespace is
// stripped by the repo's Course.Validate — the response shows
// the trimmed form.
func TestPatchCoursePaceNotes_Trims(t *testing.T) {
	t.Parallel()
	deps, _, courseID := buildAgentStudyDeps(t)

	body, _ := json.Marshal(map[string]string{"pace_notes_md": "  trim me  "})
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/courses/"+courseID, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = seedAuthIdentity(r, "a-planner")
	r = withRouteParam(r, courseID)
	w := httptest.NewRecorder()

	patchCoursePaceNotesHandlerAgent(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var c coursedomain.Course
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &c))
	assert.Equal(t, "trim me", c.PaceNotesMD)
}

// TestPatchCoursePaceNotes_Oversized: oversized payload → 400
// (the repo's Course.Validate rejects >64 KiB).
func TestPatchCoursePaceNotes_Oversized(t *testing.T) {
	t.Parallel()
	deps, _, courseID := buildAgentStudyDeps(t)

	huge := strings.Repeat("x", 65537)
	body, _ := json.Marshal(map[string]string{"pace_notes_md": huge})
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/courses/"+courseID, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = seedAuthIdentity(r, "a-planner")
	r = withRouteParam(r, courseID)
	w := httptest.NewRecorder()

	patchCoursePaceNotesHandlerAgent(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestPatchCoursePaceNotes_UnknownCourse: course id not in DB →
// 404.
func TestPatchCoursePaceNotes_UnknownCourse(t *testing.T) {
	t.Parallel()
	deps, _, _ := buildAgentStudyDeps(t)

	body, _ := json.Marshal(map[string]string{"pace_notes_md": "nope"})
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agent/courses/no-such-course", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = seedAuthIdentity(r, "a-planner")
	r = withRouteParam(r, "no-such-course")
	w := httptest.NewRecorder()

	patchCoursePaceNotesHandlerAgent(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestListCoursesHandlerAgent_EnrichesActive — Phase 31.5: when
// ?status=active, the row carries a progress sub-object with
// lessons_total / lessons_done / open_lessons[]. Drafts/others
// return the plain row.
//
// Pre-existing limitation: the underlying Course.ListCourses("") in
// `listCoursesHandlerAgent` filters by owner_id (the empty-string
// "list all" form documented there doesn't actually exist in the
// repo). The agent surface is single-owner — the resolver on the
// wire shape is left as-is for Phase 31.5 and pinned via the
// smoke test (Phase 31.11). The enrichment code path itself is
// pinned here by constructing the same logical row through a
// direct call into enrichActiveCourse.
func TestEnrichActiveCourse_AttachedProgress(t *testing.T) {
	t.Parallel()
	deps, _, courseID := buildAgentStudyDeps(t)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := r.Context()
	base := map[string]any{"id": courseID}
	got, err := enrichActiveCourse(ctx, deps, base, courseID)
	require.NoError(t, err)

	prog, ok := got["progress"].(*activeCourseProgress)
	require.True(t, ok, "expected a *activeCourseProgress on base (got %T)", got["progress"])
	assert.Equal(t, 0, prog.LessonsTotal, "fresh active course has zero lessons")
	assert.Equal(t, 0, prog.LessonsDone)
	assert.Empty(t, prog.OpenLessons)
}
