// Package api — Phase 31.5 agent-namespace study handlers.
//
// Three routes land here:
//
//	POST  /api/v1/agent/study-proposals
//	           External planner writes a pending proposal. created_by_agent
//	           is stamped from the bearer token's Identity.AgentID — the body
//	           never carries an actor field (single source of truth).
//
//	GET   /api/v1/agent/courses?status=active
//	           Lists courses with extra progress fields. The "active" filter
//	           is the planner's bread-and-butter (everything else is draft
//	           or terminal). Other statuses return plain rows.
//
//	PATCH /api/v1/agent/courses/{id}
//	           Narrow PATCH: only `pace_notes_md`. pace_notes_md is the
//	           narrow surface the planner cares about — title/status/etc.
//	           are owned by the human.
//
// The handlers mirror the Phase 29 (agent courses) pattern:
// same JSON shape on both sides of the auth boundary where possible
// (the proposal returns the same `study.Proposal` shape the user
// side gets back via GET /study-proposals).
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	coursedomain "github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/study"
	studysvc "github.com/ramgml/orenda/internal/service/study"
)

// proposeStudyRequest is the body of POST /agent/study-proposals.
// Only the planner's input fields are accepted; the actor is the
// authenticated identity.
type proposeStudyRequest struct {
	CourseID   string `json:"course_id,omitempty"`
	Title      string `json:"title"`
	BodyMD     string `json:"body_md,omitempty"`
	TargetDate string `json:"target_date"` // YYYY-MM-DD
}

// proposeStudyHandlerAgent — POST /agent/study-proposals.
func proposeStudyHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.StudyService == nil {
			http.Error(w, "study service not wired", http.StatusServiceUnavailable)
			return
		}
		var in proposeStudyRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Title == "" || in.TargetDate == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_title_or_target_date"})
			return
		}
		// Resolve C<N> course_id from body to UUID.
		if in.CourseID != "" {
			cr, err := resolveCourseRef(r.Context(), deps, in.CourseID)
			if err != nil {
				writeCourseResolveError(w, err)
				return
			}
			in.CourseID = cr.ID
		}
		agentID := ""
		if id, ok := IdentityFrom(r.Context()); ok && id != nil {
			agentID = id.AgentID
		}
		if agentID == "" {
			http.Error(w, "missing agent identity", http.StatusUnauthorized)
			return
		}
		res, err := deps.StudyService.Propose(r.Context(), agentID, studysvc.ProposeInput{
			CourseID:   in.CourseID,
			Title:      in.Title,
			BodyMD:     in.BodyMD,
			TargetDate: in.TargetDate,
		})
		if err != nil {
			mapStudyError(w, err)
			return
		}
		// Phase 32.9 dedup: if the call collapsed onto an existing
		// pending proposal, Refreshed=true. The agent API
		// distinguishes 200 OK (refreshed, no new row) from
		// 201 Created (new row) so the planner can decide whether
		// to log/skip the "new suggestion" event.
		status := http.StatusCreated
		if res.Refreshed {
			status = http.StatusOK
		}
		writeJSON(w, status, res.Proposal)
	}
}

// patchCoursePaceNotesRequest is the body of PATCH /agent/courses/{id}.
// Only pace_notes_md lands in the repo — any other field is a
// noop (the handler ignores it). The repo updates pace_notes_md
// after running Course.Validate (caps at 64 KiB and trims).
type patchCoursePaceNotesRequest struct {
	PaceNotesMD string `json:"pace_notes_md"`
}

// patchCoursePaceNotesHandlerAgent — PATCH /agent/courses/{id}.
// Narrow update: pace_notes_md only. Any status (draft/review/active/
// done/archived) is accepted — the planner writes pace notes at
// any point in the course lifecycle (most often before activation).
func patchCoursePaceNotesHandlerAgent(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Courses == nil {
			http.Error(w, "course repo not wired", http.StatusServiceUnavailable)
			return
		}
		cr, err := resolveCourseRef(r.Context(), deps, chi.URLParam(r, "id"))
		if err != nil {
			writeCourseResolveError(w, err)
			return
		}
		var req patchCoursePaceNotesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		err = deps.Courses.UpdatePaceNotesMD(r.Context(), cr.ID, req.PaceNotesMD)
		if err != nil {
			if errors.Is(err, coursedomain.ErrNotFound) {
				http.Error(w, "course not found", http.StatusNotFound)
				return
			}
			// UpdatePaceNotesMD runs through course.Course.Validate
			// (caps + trim). Anything other than ErrNotFound is a
			// validation failure; 400 is right for that.
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_pace_notes_md"})
			return
		}
		// Read-back so the agent gets the trimmed, validated value
		// (the validator in the repo applies the trim).
		got, gerr := deps.Courses.GetCourse(r.Context(), cr.ID)
		if gerr != nil {
			writeError(w, gerr)
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

// mapStudyError translates study service sentinels to HTTP codes
// and JSON error keys. Used by both the agent-side propose handler
// (above) and the user-side accept/dismiss handlers (31.6).
func mapStudyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, study.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
	default:
		writeError(w, err)
	}
}
