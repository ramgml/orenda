package api_test

// Task 18: /api/v1/reviews/due + /api/v1/reviews/{id}/result handlers.
//
// Pinned contracts:
//   - GET /reviews/due returns the signed-in user's due queue with
//     lesson/course titles joined (empty list, never null).
//   - POST /reviews/{id}/result: 200 pass advances the ladder; 400 on
//     invalid/missing body; 404 on unknown id; 404 on a foreign user's
//     review (existence is not leaked); 409 once completed_at is set.
//   - Recording a result NEVER mutates lesson progress: the lesson row
//     keeps status 'done' and the same completed_at (regression guard).
//   - GET /today carries due_reviews with the overdue flag; a missed
//     review stays in due_reviews and never appears in the red
//     overdue task list.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api"
	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/auth"
	"github.com/ramgml/orenda/internal/domain/user"
	coursesvc "github.com/ramgml/orenda/internal/service/course"
	reviewsvc "github.com/ramgml/orenda/internal/service/review"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// reviewFixture: router with review + course services against a real
// SQLite DB, a logged-in user, and a completed lesson tree.
type reviewFixture struct {
	router   http.Handler
	cookie   string
	userID   string
	reviewID string // seeded review
	lessonID string
	db       *sql.DB
}

func reviewGet(f reviewFixture, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", "orenda_session="+f.cookie)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func reviewPost(f reviewFixture, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Cookie", "orenda_session="+f.cookie)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

// setupReviewAPI wires everything; dueInPast controls whether the
// seeded review is already due.
func setupReviewAPI(t *testing.T, dueInPast bool) reviewFixture {
	t.Helper()
	db, _ := copyTemplateDB(t)
	ctx := context.Background()

	users := sqlite.NewUserRepository(db)
	u := &user.User{
		Email:        "rev@x.com",
		PasswordHash: mustHashFast(t),
		DisplayName:  "Rev",
	}
	require.NoError(t, users.Create(ctx, u))

	_, err := db.ExecContext(ctx,
		`INSERT INTO courses (id, title, owner_id, status) VALUES (?, ?, ?, 'active')`,
		"c-rev-api", "Go Deep", u.ID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_modules (id, course_id, title, position) VALUES (?, ?, ?, 1)`,
		"m-rev-api", "c-rev-api", "Core")
	require.NoError(t, err)
	const lessonID = "l-rev-api"
	completedAt := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC).Format(time.RFC3339)
	_, err = db.ExecContext(ctx,
		`INSERT INTO course_lessons (id, module_id, title, status, position, completed_at)
		 VALUES (?, ?, ?, 'done', 1, ?)`,
		lessonID, "m-rev-api", "Channels", completedAt)
	require.NoError(t, err)

	hub := ws.NewHub()
	t.Cleanup(func() {
		if c, ok := hub.(interface{ Close() }); ok {
			c.Close()
		}
	})
	reviewSvc := reviewsvc.New(sqlite.NewLessonReviewRepository(db))
	courseSvc := coursesvc.New(sqlite.NewCourseRepository(db)).WithReviews(reviewSvc)

	signer := auth.NewSigner("test-secret-32-bytes-long-xxxxx", time.Hour, "orenda")
	deps := api.Dependencies{
		Logger:        zap.NewNop(),
		Signer:        signer,
		Users:         users,
		Projects:      sqlite.NewProjectRepository(db),
		Tasks:         sqlite.NewTaskRepository(db),
		Tokens:        sqlite.NewAPITokenRepository(db),
		Courses:       sqlite.NewCourseRepository(db),
		CourseService: courseSvc,
		ReviewService: reviewSvc,
		WSHub:         hub,
		CookieName:    "orenda_session",
	}
	router := api.NewRouter(&deps)
	t.Cleanup(deps.RateLimitClose)

	body, _ := json.Marshal(map[string]string{"email": "rev@x.com", "password": "hunter2!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	cookie := rr.Result().Cookies()[0].Value

	due := time.Now().UTC().Add(24 * time.Hour)
	if dueInPast {
		due = time.Now().UTC().Add(-24 * time.Hour)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO lesson_reviews (id, lesson_id, user_id, step, due_at) VALUES (?, ?, ?, 0, ?)`,
		"rv-api-1", lessonID, u.ID, due.Format(time.RFC3339))
	require.NoError(t, err)

	return reviewFixture{
		router:   router,
		cookie:   cookie,
		userID:   u.ID,
		reviewID: "rv-api-1",
		lessonID: lessonID,
		db:       db,
	}
}

// TestReviewsDueHandler — GET /api/v1/reviews/due returns the due
// queue with joined titles; a not-yet-due review is excluded.
func TestReviewsDueHandler(t *testing.T) {
	f := setupReviewAPI(t, true)

	rr := reviewGet(f, "/api/v1/reviews/due")
	require.Equal(t, http.StatusOK, rr.Code)

	var out struct {
		Reviews []struct {
			ID          string `json:"id"`
			LessonID    string `json:"lesson_id"`
			LessonTitle string `json:"lesson_title"`
			CourseID    string `json:"course_id"`
			CourseTitle string `json:"course_title"`
			Step        int    `json:"step"`
			DueAt       string `json:"due_at"`
			LastResult  string `json:"last_result"`
		} `json:"reviews"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.Len(t, out.Reviews, 1)
	row := out.Reviews[0]
	assert.Equal(t, f.reviewID, row.ID)
	assert.Equal(t, f.lessonID, row.LessonID)
	assert.Equal(t, "Channels", row.LessonTitle)
	assert.Equal(t, "c-rev-api", row.CourseID)
	assert.Equal(t, "Go Deep", row.CourseTitle)
	assert.Equal(t, 0, row.Step)
	assert.NotEmpty(t, row.DueAt)
	assert.Equal(t, "", row.LastResult)

	// Future-due review is NOT in the queue.
	f2 := setupReviewAPI(t, false)
	rr = reviewGet(f2, "/api/v1/reviews/due")
	require.Equal(t, http.StatusOK, rr.Code)
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	assert.Empty(t, out.Reviews, "not-yet-due review must be excluded")
}

// TestReviewResultHandler_StatusCodes pins 200/400/404/409.
func TestReviewResultHandler_StatusCodes(t *testing.T) {
	t.Run("200_pass_advances", func(t *testing.T) {
		f := setupReviewAPI(t, true)
		rr := reviewPost(f, "/api/v1/reviews/"+f.reviewID+"/result", map[string]string{"result": "pass"})
		require.Equal(t, http.StatusOK, rr.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
		assert.Equal(t, float64(1), out["step"], "pass: step 0 → 1")
		assert.Nil(t, out["completed_at"], "ladder is still open")
		// Next due is now+3d.
		due, err := time.Parse(time.RFC3339, out["due_at"].(string))
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now().UTC().Add(3*24*time.Hour), due, time.Minute)
	})

	t.Run("400_missing_result", func(t *testing.T) {
		f := setupReviewAPI(t, true)
		rr := reviewPost(f, "/api/v1/reviews/"+f.reviewID+"/result", map[string]string{})
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("400_invalid_result", func(t *testing.T) {
		f := setupReviewAPI(t, true)
		rr := reviewPost(f, "/api/v1/reviews/"+f.reviewID+"/result", map[string]string{"result": "maybe"})
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("400_invalid_json", func(t *testing.T) {
		f := setupReviewAPI(t, true)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/reviews/"+f.reviewID+"/result", bytes.NewReader([]byte("{nope")))
		req.Header.Set("Cookie", "orenda_session="+f.cookie)
		rr := httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("404_unknown_id", func(t *testing.T) {
		f := setupReviewAPI(t, true)
		rr := reviewPost(f, "/api/v1/reviews/rv-nope/result", map[string]string{"result": "pass"})
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("404_foreign_review_not_leaked", func(t *testing.T) {
		f := setupReviewAPI(t, true)
		// A second user logs in; the review belongs to the first.
		users := sqlite.NewUserRepository(f.db)
		u2 := &user.User{Email: "rev2@x.com", PasswordHash: mustHashFast(t), DisplayName: "Rev2"}
		require.NoError(t, users.Create(context.Background(), u2))
		body, _ := json.Marshal(map[string]string{"email": "rev2@x.com", "password": "hunter2!"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		cookie2 := rr.Result().Cookies()[0].Value

		req = httptest.NewRequest(http.MethodPost, "/api/v1/reviews/"+f.reviewID+"/result",
			bytes.NewReader([]byte(`{"result":"pass"}`)))
		req.Header.Set("Cookie", "orenda_session="+cookie2)
		rr = httptest.NewRecorder()
		f.router.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code,
			"foreign review must look identical to a missing one")
	})

	t.Run("409_after_completion", func(t *testing.T) {
		f := setupReviewAPI(t, true)
		// Walk the whole ladder: 4 passes to reach the last step, then
		// the 5th pass closes it.
		for i := 0; i < 4; i++ {
			rr := reviewPost(f, "/api/v1/reviews/"+f.reviewID+"/result", map[string]string{"result": "pass"})
			require.Equal(t, http.StatusOK, rr.Code)
		}
		rr := reviewPost(f, "/api/v1/reviews/"+f.reviewID+"/result", map[string]string{"result": "pass"})
		require.Equal(t, http.StatusOK, rr.Code)
		var out map[string]any
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
		require.NotNil(t, out["completed_at"], "5th pass closes the ladder")

		// Any further result → 409.
		rr = reviewPost(f, "/api/v1/reviews/"+f.reviewID+"/result", map[string]string{"result": "pass"})
		assert.Equal(t, http.StatusConflict, rr.Code)
	})
}

// TestReviewResult_DoesNotTouchLessonProgress — the DoD invariant: a
// review result never mutates lesson progress (status stays done,
// completed_at unchanged).
func TestReviewResult_DoesNotTouchLessonProgress(t *testing.T) {
	f := setupReviewAPI(t, true)

	var statusBefore string
	var completedBefore string
	require.NoError(t, f.db.QueryRow(
		`SELECT status, COALESCE(completed_at, '') FROM course_lessons WHERE id = ?`,
		f.lessonID).Scan(&statusBefore, &completedBefore))
	require.Equal(t, "done", statusBefore)

	// Fail — the harshest path (a reset) — must still not touch it.
	rr := reviewPost(f, "/api/v1/reviews/"+f.reviewID+"/result", map[string]string{"result": "fail"})
	require.Equal(t, http.StatusOK, rr.Code)

	var statusAfter string
	var completedAfter string
	require.NoError(t, f.db.QueryRow(
		`SELECT status, COALESCE(completed_at, '') FROM course_lessons WHERE id = ?`,
		f.lessonID).Scan(&statusAfter, &completedAfter))
	assert.Equal(t, "done", statusAfter, "lesson status must stay done")
	assert.Equal(t, completedBefore, completedAfter, "lesson completed_at must not change")
}

// TestToday_CarriesDueReviews — DoD 3: due_reviews on /today with the
// overdue flag; a missed review stays in due_reviews (overdue=true)
// and never appears in the overdue TASK list.
func TestToday_CarriesDueReviews(t *testing.T) {
	f := setupReviewAPI(t, true)

	rr := reviewGet(f, "/api/v1/today")
	require.Equal(t, http.StatusOK, rr.Code)

	var out struct {
		Overdue    []json.RawMessage `json:"overdue"`
		DueReviews []struct {
			ID          string `json:"id"`
			LessonID    string `json:"lesson_id"`
			LessonTitle string `json:"lesson_title"`
			CourseID    string `json:"course_id"`
			CourseTitle string `json:"course_title"`
			Step        int    `json:"step"`
			DueAt       string `json:"due_at"`
			Overdue     bool   `json:"overdue"`
		} `json:"due_reviews"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	require.Len(t, out.DueReviews, 1)
	row := out.DueReviews[0]
	assert.Equal(t, f.reviewID, row.ID)
	assert.True(t, row.Overdue, "review due 24h ago is overdue")
	assert.Equal(t, "Channels", row.LessonTitle)

	// The review row never leaks into the task overdue list: scan the
	// raw overdue payload for the review id.
	for _, raw := range out.Overdue {
		assert.NotContains(t, string(raw), f.reviewID,
			"review rows must not migrate into the overdue task list")
	}
}
