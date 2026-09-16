// Package api — T16 dialog tutor endpoints.
//
// User side (RequireUser, cookie):
//   - GET  /api/v1/lessons/{id}/tutor — thread history (200; an
//     empty thread is an empty list, unknown lesson 404).
//   - POST /api/v1/lessons/{id}/tutor — ask a question (201; makes
//     the thread pending; empty body 400; already-pending 409).
//
// Agent side (RequireAgent, bearer):
//   - GET  /api/v1/agent/tutor/pending — queue of pending questions
//     with the lesson context embedded (content_md + quizzes), so
//     one call is enough to answer.
//   - POST /api/v1/agent/tutor/{lesson_id}/reply — answer the
//     pending question (201; no pending question 409).
//
// Both POSTs fan the new message out on the WS topic "tutor" (nil-
// safe). The body carries user_id so the hub filters per recipient,
// plus lesson_id/role so the LessonPage panel can drop events from
// other lessons.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/course"
	tutorsvc "github.com/ramgml/orenda/internal/service/tutor"
)

// tutorAskBody is the wire shape of POST /lessons/{id}/tutor.
type tutorAskBody struct {
	QuestionMD string `json:"question_md"`
}

// tutorReplyBody is the wire shape of POST /agent/tutor/{lesson_id}/reply.
type tutorReplyBody struct {
	BodyMD string `json:"body_md"`
}

// tutorAskHandler answers the student's question POST.
func tutorAskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tutor == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tutor_not_wired"})
			return
		}
		var body tutorAskBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		userID := userIDFromCtx(r)
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		m, err := deps.Tutor.Ask(r.Context(), chi.URLParam(r, "id"), userID, body.QuestionMD)
		if err != nil {
			writeTutorError(w, err)
			return
		}
		publishTutor(r.Context(), deps, m)
		recordTutorActivity(r.Context(), deps, r, course.ActivityTutorQuestion, m.BodyMD)
		writeJSON(w, http.StatusCreated, m)
	}
}

// tutorHistoryHandler returns the student's thread (replay on page
// load). An empty thread is 200 with an empty list — the lesson
// exists, the conversation just hasn't started.
func tutorHistoryHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tutor == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tutor_not_wired"})
			return
		}
		userID := userIDFromCtx(r)
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		msgs, err := deps.Tutor.History(r.Context(), chi.URLParam(r, "id"), userID)
		if err != nil {
			writeTutorError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"messages": msgs})
	}
}

// tutorPendingHandler lists the pending questions with lesson
// context for the agent. Open to any valid agent token: the single-
// owner install has one tutor agent; per-agent scoping is a future
// concern.
func tutorPendingHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tutor == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tutor_not_wired"})
			return
		}
		pending, err := deps.Tutor.ListPending(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"pending": pending})
	}
}

// tutorReplyHandler records the agent's answer. The pending user is
// derived inside the service (the lesson's pending thread).
func tutorReplyHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tutor == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tutor_not_wired"})
			return
		}
		var body tutorReplyBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		m, err := deps.Tutor.Reply(r.Context(), chi.URLParam(r, "lesson_id"), body.BodyMD)
		if err != nil {
			writeTutorError(w, err)
			return
		}
		publishTutor(r.Context(), deps, m)
		recordTutorActivity(r.Context(), deps, r, course.ActivityTutorReply, m.BodyMD)
		writeJSON(w, http.StatusCreated, m)
	}
}

// writeTutorError maps tutor service sentinels onto HTTP statuses:
// 404 / 400 / 409, everything else through the shared writeError.
func writeTutorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tutorsvc.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, tutorsvc.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
	case errors.Is(err, tutorsvc.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "conflict"})
	default:
		writeError(w, err)
	}
}

// publishTutor fans one message out on the WS topic "tutor".
// nil-safe. user_id routes the event through the hub's per-user
// filter; lesson_id + role let the LessonPage panel ignore events
// from other lessons.
func publishTutor(ctx context.Context, deps *Dependencies, m *tutorsvc.Message) {
	if deps.WSHub == nil || m == nil {
		return
	}
	deps.WSHub.Publish(ctx, ws.Event{
		Topic: "tutor",
		Body: map[string]any{
			"user_id":   m.UserID,
			"lesson_id": m.LessonID,
			"role":      string(m.Role),
			"message":   m,
		},
	})
}

// recordTutorActivity writes the course-activity row for a tutor
// turn (question or reply). The endpoints know only the lesson id,
// so the course is resolved lesson → module → course via the same
// repo the service used. Best-effort: audit gaps must not fail the
// user-visible action.
func recordTutorActivity(ctx context.Context, deps *Dependencies, r *http.Request, kind course.ActivityKind, payload string) {
	if deps.CourseActivityRecorder == nil || deps.Courses == nil {
		return
	}
	courseID := tutorCourseID(r, deps)
	if courseID == "" {
		return
	}
	_ = deps.CourseActivityRecorder.RecordCourseAuto(ctx, courseID, kind, payload)
}

// tutorCourseID resolves the URL's lesson ref (user group "{id}",
// agent group "{lesson_id}") to the owning course id; "" when the
// walk fails (the lesson was just verified by the service, so this
// is a data-integrity case, not a request error).
func tutorCourseID(r *http.Request, deps *Dependencies) string {
	lessonID := chi.URLParam(r, "id")
	if lessonID == "" {
		lessonID = chi.URLParam(r, "lesson_id")
	}
	if lessonID == "" {
		return ""
	}
	lesson, err := resolveLessonRef(r.Context(), deps, lessonID)
	if err != nil {
		return ""
	}
	mod, err := deps.Courses.GetModule(r.Context(), lesson.ModuleID)
	if err != nil {
		return ""
	}
	return mod.CourseID
}
