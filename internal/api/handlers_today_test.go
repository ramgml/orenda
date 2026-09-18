package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/study"
	"github.com/ramgml/orenda/internal/domain/task"
	studysvc "github.com/ramgml/orenda/internal/service/study"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// buildTodayDeps wires Tasks + StudyService against a real SQLite
// DB so the today handler runs end-to-end. Tests build their own
// tasks via the DB handle to set due_at precisely (the handler
// compares against server-time UTC midnight; hardcoding "today"
// here would be flaky).
//
// The returned *sql.DB is exposed so tests can seed rows directly
// (the task repo's Create requires a Task struct, which has
// time.Time fields — easier to insert via raw SQL for the
// due_at-yesterday / due_at-today boundary cases).
func buildTodayDeps(t *testing.T) (*Dependencies, *sql.DB) {
	t.Helper()
	db := copyInternalTemplateDB(t)
	ctx := context.Background()

	studySvc := studysvc.New(
		sqlite.NewStudyProposalRepository(db),
		sqlite.NewTaskRepository(db),
		nil, nil,
	)

	deps := &Dependencies{
		Tasks:          sqlite.NewTaskRepository(db),
		StudyService:   studySvc,
		Courses:        sqlite.NewCourseRepository(db),
		StudyProposals: sqlite.NewStudyProposalRepository(db),
	}

	// Seed a single owner so the (unwired) active-timer path
	// doesn't blow up; we don't use it.
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES (?, ?, ?, ?)`,
		"u-today", "today@031.local", "x", "U")
	require.NoError(t, err)
	return deps, db
}

// loadToday issues a GET /today request and returns the decoded
// response. Tests call this once and assert on the populated
// fields rather than re-running the handler.
func loadToday(t *testing.T, deps *Dependencies) todayResponse {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/today", nil)
	w := httptest.NewRecorder()
	getTodayHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp todayResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// loadTodayAs issues a GET /today request authenticated as userID
// (Task 30: the courses slice is scoped to the session user, so the
// owner-filter test needs an actual identity on the request).
func loadTodayAs(t *testing.T, deps *Dependencies, userID string) todayResponse {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/today", nil)
	r = r.WithContext(WithIdentity(r.Context(), &Identity{UserID: userID}))
	w := httptest.NewRecorder()
	getTodayHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp todayResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// TestToday_Proposals_EmptyByDefault — the proposals field is
// always present (empty array) so the front-end can render the
// tray without a "loading" guard.
func TestToday_Proposals_EmptyByDefault(t *testing.T) {
	t.Parallel()
	deps, _ := buildTodayDeps(t)
	resp := loadToday(t, deps)
	assert.NotNil(t, resp.Proposals)
	assert.Empty(t, resp.Proposals)
}

// TestToday_Proposals_SurfacesPending — a pending proposal shows
// up in the proposals field of /today.
func TestToday_Proposals_SurfacesPending(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES (?, ?, ?, ?, '[]')`,
		"t-tok", "u-today", "seed", "h")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES (?, ?, ?, ?, 3)`,
		"a-planner", "planner", "[]", "t-tok")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-today", "Rust", "u-today")
	require.NoError(t, err)

	res, err := deps.StudyService.Propose(ctx, "a-planner", studysvc.ProposeInput{
		CourseID:   "c-today",
		Title:      "Read chapter 5",
		BodyMD:     "rust-book chapter 5",
		TargetDate: "2099-08-17",
	})
	require.NoError(t, err)
	p := res.Proposal

	resp := loadToday(t, deps)
	require.Len(t, resp.Proposals, 1)
	got := resp.Proposals[0]
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, "Read chapter 5", got.Title)
	assert.Equal(t, "c-today", got.CourseID)
	assert.Equal(t, "2099-08-17", got.TargetDate)
	assert.Equal(t, "a-planner", got.AgentID)
	assert.NotEmpty(t, got.CreatedAt)
}

// TestToday_StudyReminder_NotInOverdue — the cornerstone of
// Phase 31.7: a study-reminder with a due_at from yesterday is
// NOT in overdue. Missed day never turns red.
func TestToday_StudyReminder_NotInOverdue(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-t", "Rust", "u-today")
	require.NoError(t, err)
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, study_course_id, due_at) VALUES (?, ?, 'todo', ?, ?)`,
		"reminder-1", "Read chapter 5", "c-t", yesterday)
	require.NoError(t, err)

	resp := loadToday(t, deps)
	for _, taskItem := range resp.Overdue {
		assert.NotEqual(t, "reminder-1", taskItem.ID,
			"study reminders must never surface under overdue")
	}
}

// TestToday_StudyReminder_InDueToday — even with yesterday's
// due_at, the reminder appears in due_today today (so the user
// can ack/dismiss it).
func TestToday_StudyReminder_InDueToday(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-t", "Rust", "u-today")
	require.NoError(t, err)
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, study_course_id, due_at) VALUES (?, ?, 'todo', ?, ?)`,
		"reminder-1", "Read chapter 5", "c-t", yesterday)
	require.NoError(t, err)

	resp := loadToday(t, deps)
	found := false
	for _, taskItem := range resp.DueToday {
		if taskItem.ID == "reminder-1" {
			found = true
		}
	}
	assert.True(t, found,
		"study reminder with yesterday's due_at should surface in due_today (got %d tasks)", len(resp.DueToday))
}

// TestToday_RegularOverdue_StillOverdue — regression guard: a
// non-study task with yesterday's due_at STILL escalates to
// overdue. The "no escalation" rule is for reminders only.
func TestToday_RegularOverdue_StillOverdue(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, due_at) VALUES (?, ?, 'todo', ?)`,
		"regular-1", "Bug fix", yesterday)
	require.NoError(t, err)

	resp := loadToday(t, deps)
	found := false
	for _, taskItem := range resp.Overdue {
		if taskItem.ID == "regular-1" {
			found = true
		}
	}
	assert.True(t, found,
		"non-study tasks with yesterday's due_at must escalate to overdue")
}

// TestToday_RegularDueToday — regression guard: a non-study
// task with today's due_at shows up in due_today.
func TestToday_RegularDueToday(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	now := time.Now().UTC()
	inWindow := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, due_at) VALUES (?, ?, 'todo', ?)`,
		"regular-2", "Refactor", inWindow)
	require.NoError(t, err)

	resp := loadToday(t, deps)
	found := false
	for _, taskItem := range resp.DueToday {
		if taskItem.ID == "regular-2" {
			found = true
		}
	}
	assert.True(t, found, "regular task with today's due_at must be in due_today")
}

// TestToday_StudyReminder_TodayDueAt — boundary: a study
// reminder with due_at = today end-of-day (the typical accept
// path) shows up in due_today under the in-window filter.
func TestToday_StudyReminder_TodayDueAt(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	_, err := db.ExecContext(context.Background(),
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-t", "Rust", "u-today")
	require.NoError(t, err)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.UTC).Format(time.RFC3339)
	_, err = db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, study_course_id, due_at) VALUES (?, ?, 'todo', ?, ?)`,
		"reminder-today", "Read chapter 5", "c-t", today)
	require.NoError(t, err)

	resp := loadToday(t, deps)
	found := false
	for _, taskItem := range resp.DueToday {
		if taskItem.ID == "reminder-today" {
			found = true
		}
	}
	assert.True(t, found, "study reminder with today's due_at should be in due_today")
	// And not overdue.
	for _, taskItem := range resp.Overdue {
		assert.NotEqual(t, "reminder-today", taskItem.ID)
	}
}

// TestToday_Proposals_NoServiceWired — partial-router fixtures
// that don't wire StudyService still work (Proposals is an empty
// array, not a 500).
func TestToday_Proposals_NoServiceWired(t *testing.T) {
	t.Parallel()
	db := copyInternalTemplateDB(t)

	deps := &Dependencies{
		Tasks: sqlite.NewTaskRepository(db),
		// StudyService: nil — partial fixture.
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/today", nil)
	w := httptest.NewRecorder()
	getTodayHandler(deps).ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var resp todayResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Proposals)
}

// silence unused-import warning when running the test suite
// without study proposals.
var _ = study.StatusPending
var _ = task.StatusTodo

// --- Task 30: the courses section of /today ---
//
// Each active course of the owner surfaces as a lightweight row
// {id, title, drift}. Drift is the server-side classification
// (course.ClassifyDrift over the shared 14-day window), so the
// Today page never red-flags anything the planner wouldn't.

// seedTodayCourse inserts a course row directly — the Create
// service generates a planner task we don't need here. The number
// column is UNIQUE (migration 038) so each call draws the next one
// (atomic: seedTodayCourse runs under t.Parallel in several tests).
var todayCourseSeq atomic.Int64

func seedTodayCourse(t *testing.T, db *sql.DB, id, title, ownerID, status string) {
	t.Helper()
	n := todayCourseSeq.Add(1)
	_, err := db.Exec(`INSERT INTO courses (id, title, owner_id, status, number) VALUES (?, ?, ?, ?, ?)`,
		id, title, ownerID, status, n)
	require.NoError(t, err)
}

// seedDoneLesson inserts a module + done lesson with completed_at
// inside the 14-day pace window (raw SQL: the repo's Create path
// has no completed_at knob).
func seedDoneLesson(t *testing.T, db *sql.DB, moduleID, lessonID, courseID string, completedAt time.Time) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO course_modules (id, course_id, title) VALUES (?, ?, 'm')`,
		moduleID, courseID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO course_lessons (id, module_id, title, status, position, completed_at) VALUES (?, ?, ?, 'done', 0, ?)`,
		lessonID, moduleID, "lesson", completedAt.UTC().Format(time.RFC3339))
	require.NoError(t, err)
}

// seedAcceptedProposals inserts accepted study_proposals rows with
// created_at = now (direct INSERT: full service Accept would also
// create inbox tasks we don't need).
func seedAcceptedProposals(t *testing.T, db *sql.DB, courseID string, agentID string, n int) {
	t.Helper()
	require.NotEmpty(t, agentID, "proposals need an agent row (created_by_agent FK)")
	for i := 0; i < n; i++ {
		_, err := db.Exec(`INSERT INTO study_proposals (id, course_id, title, target_date, status, created_by_agent, created_at)
			VALUES (?, ?, 'p', '2099-01-01', 'accepted', ?, ?)`,
			fmt.Sprintf("sp-%s-%d", courseID, i), courseID, agentID, time.Now().UTC().Format(time.RFC3339))
		require.NoError(t, err)
	}
}

func findTodayCourse(resp todayResponse, id string) *todayCourseView {
	for i := range resp.Courses {
		if resp.Courses[i].ID == id {
			return &resp.Courses[i]
		}
	}
	return nil
}

// TestToday_Courses_BehindWhenDoneLagsAccepted — an active course
// with done lessons but MORE accepted proposals lands behind.
func TestToday_Courses_BehindWhenDoneLagsAccepted(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	// agent + token for the proposal FK
	_, err := db.Exec(`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES ('t-tok2', 'u-today', 'seed', 'h', '[]')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES ('a-planner2', 'planner2', '[]', 't-tok2', 3)`)
	require.NoError(t, err)

	seedTodayCourse(t, db, "c-behind", "Behind course", "u-today", "active")
	// 1 done lesson, 5 accepted proposals → actual << target → behind.
	seedDoneLesson(t, db, "m-behind", "l-behind", "c-behind", time.Now())
	seedAcceptedProposals(t, db, "c-behind", "a-planner2", 5)

	resp := loadToday(t, deps)
	require.NotNil(t, resp.Courses)
	require.Len(t, resp.Courses, 1)
	got := findTodayCourse(resp, "c-behind")
	require.NotNil(t, got)
	assert.Equal(t, "Behind course", got.Title)
	assert.Equal(t, "behind", got.Drift)
}

// TestToday_Courses_NotBehindWhenPaceMatches — same course but the
// accepted-proposal count no longer dominates actual velocity:
// drift flips to a non-behind value and (per the contract) the UI
// renders no marker.
func TestToday_Courses_NotBehindWhenPaceMatches(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)

	_, err := db.Exec(`INSERT INTO api_tokens (id, user_id, name, hash, scopes) VALUES ('t-tok3', 'u-today', 'seed', 'h', '[]')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO agents (id, name, type, token_id, max_concurrent) VALUES ('a-planner3', 'planner3', '[]', 't-tok3', 3)`)
	require.NoError(t, err)

	seedTodayCourse(t, db, "c-ok", "On track course", "u-today", "active")
	seedDoneLesson(t, db, "m-ok", "l-ok", "c-ok", time.Now())
	// 1 done lesson, 1 accepted proposal → ratio 1.0 → on_track.
	seedAcceptedProposals(t, db, "c-ok", "a-planner3", 1)

	resp := loadToday(t, deps)
	got := findTodayCourse(resp, "c-ok")
	require.NotNil(t, got)
	assert.Equal(t, "on_track", got.Drift)
	assert.NotEqual(t, "behind", got.Drift)
}

// TestToday_Courses_ExcludesNonActive — only status='active' rows
// show up in the dashboard slice.
func TestToday_Courses_ExcludesNonActive(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)
	seedTodayCourse(t, db, "c-draft", "Draft course", "u-today", "draft")
	seedTodayCourse(t, db, "c-done", "Done course", "u-today", "done")
	seedTodayCourse(t, db, "c-live", "Live course", "u-today", "active")

	resp := loadToday(t, deps)
	require.Len(t, resp.Courses, 1)
	assert.Equal(t, "c-live", resp.Courses[0].ID)
}

// TestToday_Courses_FiltersByOwner — another owner's course stays
// out of my Today page.
func TestToday_Courses_FiltersByOwner(t *testing.T) {
	t.Parallel()
	deps, db := buildTodayDeps(t)
	_, err := db.Exec(`INSERT INTO users (id, email, password_hash, display_name) VALUES ('u-other', 'other@t30.local', 'x', 'O')`)
	require.NoError(t, err)

	seedTodayCourse(t, db, "c-mine", "Mine", "u-today", "active")
	seedTodayCourse(t, db, "c-theirs", "Theirs", "u-other", "active")

	resp := loadTodayAs(t, deps, "u-today")
	require.Len(t, resp.Courses, 1)
	assert.Equal(t, "c-mine", resp.Courses[0].ID)
	assert.Equal(t, "Mine", resp.Courses[0].Title)
}

// TestToday_Courses_EmptyArrayNoRepo — partial fixtures without a
// Courses repo get an empty (non-null) array, not a 500.
func TestToday_Courses_EmptyArrayNoRepo(t *testing.T) {
	t.Parallel()
	db := copyInternalTemplateDB(t)
	deps := &Dependencies{
		Tasks: sqlite.NewTaskRepository(db),
	}

	resp := loadToday(t, deps)
	require.NotNil(t, resp.Courses)
	assert.Empty(t, resp.Courses)
}
