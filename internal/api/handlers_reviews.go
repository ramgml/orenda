// Package api — task 18: spaced-repetition review handlers.
//
// User-side surface (RequireUser):
//
//	GET  /api/v1/reviews/due          open (completed_at IS NULL) reviews
//	                                  of the signed-in user with
//	                                  due_at <= now.
//	POST /api/v1/reviews/{id}/result  record the aggregated session
//	                                  result; advances/resets the
//	                                  ladder.
//
// A repeat session means "answer the lesson's quizzes again" (exact via
// AnswerQuiz, open via the existing CreateQuizReviewTask flow); this
// endpoint only accepts the aggregated outcome — the per-quiz answer
// logic is NOT duplicated here. A recorded result never mutates lesson
// progress: the lesson row keeps status 'done' and its completed_at.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/service/review"
)

// reviewView is one row of the due list. Titles are joined server-side
// so the client renders without follow-up fetches.
type reviewView struct {
	ID          string  `json:"id"`
	LessonID    string  `json:"lesson_id"`
	LessonTitle string  `json:"lesson_title"`
	CourseID    string  `json:"course_id"`
	CourseTitle string  `json:"course_title"`
	Step        int     `json:"step"`
	DueAt       string  `json:"due_at"`
	LastResult  *string `json:"last_result"`
}

// listDueReviewsHandler serves GET /api/v1/reviews/due.
func listDueReviewsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ReviewService == nil {
			http.Error(w, "review service not wired", http.StatusServiceUnavailable)
			return
		}
		id, ok := IdentityFrom(r.Context())
		if !ok || id.UserID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		items, err := deps.ReviewService.ListDue(r.Context(), id.UserID, time.Now().UTC())
		if err != nil {
			writeError(w, err)
			return
		}
		views := make([]reviewView, 0, len(items))
		for _, it := range items {
			views = append(views, reviewView{
				ID:          it.ID,
				LessonID:    it.LessonID,
				LessonTitle: it.LessonTitle,
				CourseID:    it.CourseID,
				CourseTitle: it.CourseTitle,
				Step:        it.Step,
				DueAt:       it.DueAt.UTC().Format(time.RFC3339),
				LastResult:  reviewResultPtr(it.LastResult),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"reviews": views})
	}
}

// reviewResultRequest is the body of POST /reviews/{id}/result.
type reviewResultRequest struct {
	Result string `json:"result"`
}

// postReviewResultHandler serves POST /api/v1/reviews/{id}/result.
//
// Status codes: 200 recorded; 400 invalid/missing result; 404 unknown
// or foreign review id (both 404 — existence is not leaked); 409 the
// ladder is already closed (completed_at set).
func postReviewResultHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.ReviewService == nil {
			http.Error(w, "review service not wired", http.StatusServiceUnavailable)
			return
		}
		id, ok := IdentityFrom(r.Context())
		if !ok || id.UserID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var in reviewResultRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		result := course.LessonReviewResult(in.Result)
		if result != course.ReviewPass && result != course.ReviewFail {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_result"})
			return
		}
		rev, err := deps.ReviewService.RecordResult(r.Context(), chi.URLParam(r, "id"), id.UserID, result, time.Now().UTC())
		if err != nil {
			switch {
			case errors.Is(err, review.ErrNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "review_not_found"})
			case errors.Is(err, review.ErrConflict):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "review_completed"})
			case errors.Is(err, review.ErrInvalidInput):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_result"})
			default:
				writeError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":           rev.ID,
			"lesson_id":    rev.LessonID,
			"step":         rev.Step,
			"due_at":       rev.DueAt.UTC().Format(time.RFC3339),
			"last_result":  string(rev.LastResult),
			"completed_at": formatReviewTimePtr(rev.CompletedAt),
		})
	}
}

// reviewResultPtr maps the empty domain zero value (never attempted)
// to JSON null; pass/fail stay as-is.
func reviewResultPtr(r course.LessonReviewResult) *string {
	if r == "" {
		return nil
	}
	s := string(r)
	return &s
}

func formatReviewTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
