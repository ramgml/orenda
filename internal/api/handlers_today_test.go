package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	ctx := context.Background()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := sqlite.Open(ctx, dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	require.NoError(t, sqlite.Migrate(ctx, db, sqlite.MigrationsFS, "migrations"))
	t.Cleanup(func() { _ = db.Close() })

	studySvc := studysvc.New(
		sqlite.NewStudyProposalRepository(db),
		sqlite.NewTaskRepository(db),
		nil, nil,
	)

	deps := &Dependencies{
		Tasks:        sqlite.NewTaskRepository(db),
		StudyService: studySvc,
	}

	// Seed a single owner so the (unwired) active-timer path
	// doesn't blow up; we don't use it.
	_, err = db.ExecContext(ctx,
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

// TestToday_Proposals_EmptyByDefault — the proposals field is
// always present (empty array) so the front-end can render the
// tray without a "loading" guard.
func TestToday_Proposals_EmptyByDefault(t *testing.T) {
	deps, _ := buildTodayDeps(t)
	resp := loadToday(t, deps)
	assert.NotNil(t, resp.Proposals)
	assert.Empty(t, resp.Proposals)
}

// TestToday_Proposals_SurfacesPending — a pending proposal shows
// up in the proposals field of /today.
func TestToday_Proposals_SurfacesPending(t *testing.T) {
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
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orenda.db")
	db, err := sqlite.Open(context.Background(), dbPath, sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))
	t.Cleanup(func() { _ = db.Close() })

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
