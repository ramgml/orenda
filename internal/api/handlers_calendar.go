// Package api — Phase 4 handlers: events CRUD + timer endpoints + time
// report.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/event"
	"github.com/ramgml/orenda/internal/service/timeentry"
)

// ----------------------------------------------------------------------------
// Events
// ----------------------------------------------------------------------------

// eventInput is the JSON body for create/update.
type eventInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	StartAt     string `json:"start_at"` // RFC3339 or "2006-01-02 15:04:05"
	EndAt       string `json:"end_at"`
	AllDay      bool   `json:"all_day"`
	Color       string `json:"color"`
	ProjectID   string `json:"project_id"`
	Recurrence  string `json:"recurrence"`
}

// listEventsHandler returns events in [from, to).
//
// Query params: from, to (RFC3339), project_id (optional).
func listEventsHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		from := parseOptionalTime(r.URL.Query().Get("from"))
		to := parseOptionalTime(r.URL.Query().Get("to"))
		if from == nil || to == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from_and_to_required"})
			return
		}
		projectID := r.URL.Query().Get("project_id")

		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		events, err := deps.EventService.ListInRange(r.Context(), *from, *to, projectID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	}
}

// createEventHandler inserts an event.
func createEventHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		var in eventInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		e := &event.Event{
			Title:       in.Title,
			Description: in.Description,
			AllDay:      in.AllDay,
			Color:       in.Color,
			ProjectID:   in.ProjectID,
			Recurrence:  in.Recurrence,
		}
		e.StartAt = *parseOptionalTime(in.StartAt)
		e.EndAt = *parseOptionalTime(in.EndAt)
		if err := e.Validate(); err != nil {
			writeError(w, err)
			return
		}
		got, err := deps.EventService.Create(r.Context(), e)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, got)
	}
}

// getEventHandler returns one event.
func getEventHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		e, err := deps.EventService.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, e)
	}
}

// updateEventHandler mutates an event.
func updateEventHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		existing, err := deps.EventService.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		var in eventInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Title != "" {
			existing.Title = in.Title
		}
		if in.Description != "" {
			existing.Description = in.Description
		}
		if in.StartAt != "" {
			existing.StartAt = *parseOptionalTime(in.StartAt)
		}
		if in.EndAt != "" {
			existing.EndAt = *parseOptionalTime(in.EndAt)
		}
		if in.Color != "" {
			existing.Color = in.Color
		}
		if in.ProjectID != "" {
			existing.ProjectID = in.ProjectID
		}
		if in.Recurrence != "" {
			existing.Recurrence = in.Recurrence
		}
		existing.AllDay = in.AllDay

		if err := deps.EventService.Update(r.Context(), existing); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, existing)
	}
}

// deleteEventHandler removes an event.
func deleteEventHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		if err := deps.EventService.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ----------------------------------------------------------------------------
// Timer endpoints (Phase 4.5)
// ----------------------------------------------------------------------------

// startTimerHandler opens a timer for the current user (or agent) on a
// task. The actor is the user id when called from the UI; the agent id
// when called via /agent/tasks/:id/timer/start (Phase 4.6 adds the
// agent-facing route).
func startTimerHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TimeService == nil {
			http.Error(w, "time service not wired", http.StatusServiceUnavailable)
			return
		}
		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		actorID := id.UserID
		if actorID == "" {
			actorID = id.AgentID
		}
		got, err := deps.TimeService.Start(r.Context(), chi.URLParam(r, "id"), actorID)
		if err != nil {
			if err == timeentry.ErrAlreadyOpen {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "already_open"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

// stopTimerHandler closes the caller's open timer.
func stopTimerHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TimeService == nil {
			http.Error(w, "time service not wired", http.StatusServiceUnavailable)
			return
		}
		id, ok := IdentityFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_identity"})
			return
		}
		actorID := id.UserID
		if actorID == "" {
			actorID = id.AgentID
		}
		got, err := deps.TimeService.Stop(r.Context(), actorID)
		if err != nil {
			if err == timeentry.ErrNotFound {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no_open_timer"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, got)
	}
}

// manualTimeInput is the JSON body of POST /tasks/:id/time.
type manualTimeInput struct {
	AgentID string `json:"agent_id"` // optional; defaults to current user
	StartAt string `json:"start_at"`
	EndAt   string `json:"end_at"`
}

// addManualTimeHandler creates a closed entry.
func addManualTimeHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TimeService == nil {
			http.Error(w, "time service not wired", http.StatusServiceUnavailable)
			return
		}
		var in manualTimeInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		start := parseOptionalTime(in.StartAt)
		end := parseOptionalTime(in.EndAt)
		if start == nil || end == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_time_range"})
			return
		}
		actorID := in.AgentID
		if actorID == "" {
			if id, ok := IdentityFrom(r.Context()); ok {
				actorID = id.UserID
			}
		}
		if actorID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id_required"})
			return
		}
		got, err := deps.TimeService.ManualAdd(r.Context(),
			chi.URLParam(r, "id"), actorID, *start, *end)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, got)
	}
}

// ----------------------------------------------------------------------------
// Time report (Phase 4.5/4.9)
// ----------------------------------------------------------------------------

// reportTimeHandler returns the per-task aggregation for a window.
//
// Query params: agent_id (optional, defaults to current user), from, to
// (RFC3339). If from/to are missing, the current day is used.
func reportTimeHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.TimeService == nil {
			http.Error(w, "time service not wired", http.StatusServiceUnavailable)
			return
		}
		actorID := r.URL.Query().Get("agent_id")
		if actorID == "" {
			if id, ok := IdentityFrom(r.Context()); ok {
				actorID = id.UserID
			}
		}
		if actorID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id_required"})
			return
		}
		from := parseOptionalTime(r.URL.Query().Get("from"))
		to := parseOptionalTime(r.URL.Query().Get("to"))
		if from == nil || to == nil {
			now := time.Now()
			dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			from = &dayStart
			dayEnd := dayStart.Add(24 * time.Hour)
			to = &dayEnd
		}
		rep, err := deps.TimeService.Report(r.Context(), actorID, *from, *to)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}
}
