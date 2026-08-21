// Package api — Phase 4 handlers: events CRUD + timer endpoints + time
// report.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/domain/event"
	"github.com/ramgml/orenda/internal/service/timeentry"
)

// resolveMasterID strips the synthetic "::N" occurrence suffix that
// listEventsHandler stamps on recurring-event expansions. A bare UUID
// passes through unchanged. This is the documented round-trip: the
// calendar UI sends the synthetic id back, and the handler resolves it
// to the master event id so GET / PATCH / DELETE operate on the master.
func resolveMasterID(id string) string {
	if master, _, ok := strings.Cut(id, "::"); ok {
		return master
	}
	return id
}

// ----------------------------------------------------------------------------
// Events
// ----------------------------------------------------------------------------

// eventInput is the JSON body for create/update.
//
// Phase 16: ProjectID is *string so the API can distinguish "absent"
// (leave project alone) from "explicit empty" (file the event in the
// Inbox). Create accepts both: omitted = whatever's on the URL or ""
// for inbox; explicit = use it as-is. Update treats omission as
// "leave alone" (so an event update that doesn't touch the project
// field doesn't unfile the event).
type eventInput struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	StartAt     string  `json:"start_at"` // RFC3339 or "2006-01-02 15:04:05"
	EndAt       string  `json:"end_at"`
	AllDay      bool    `json:"all_day"`
	Color       string  `json:"color"`
	ProjectID   *string `json:"project_id"` // Phase 16: nullable semantics
	Recurrence  string  `json:"recurrence"`
}

// listEventsHandler returns events in [from, to).
//
// Query params: from, to (RFC3339), project_id (optional).
//
// Phase 23.3: master events with a recurrence rule are expanded into
// the [from, to) window via Service.ExpandRecurrence. Each occurrence
// is returned as an event.Event with StartAt/EndAt shifted and the
// master id preserved in the synthetic id (masterID::occurrenceIndex)
// — that way the calendar UI can render the recurring series without
// storing N rows, while still allowing a "click to open" on a
// specific occurrence (which round-trips to the master via the
// synthetic id). The front-end treats the synthetic id as opaque.
//
// Events without a recurrence rule still appear as-is. The window
// boundary is half-open: occurrences whose start is exactly at `to`
// are excluded so a calendar day view doesn't double-count the
// last slot.
func listEventsHandler(deps *Dependencies) http.HandlerFunc {
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
		masters, err := deps.EventService.ListInRange(r.Context(), *from, *to, projectID)
		if err != nil {
			writeError(w, err)
			return
		}

		out := make([]*event.Event, 0, len(masters))
		for _, m := range masters {
			occs, oerr := deps.EventService.ExpandRecurrence(m, *from, *to)
			if oerr != nil {
				// Tolerate one bad RRULE: skip the expansion and emit
				// the master as a single occurrence so the calendar
				// still shows the event for the day. The user can
				// edit the rule from the event page.
				out = append(out, m)
				continue
			}
			for i := range occs {
				occ := &occs[i]
				// Synthetic id so the UI can render each
				// occurrence distinctly but the master remains
				// addressable via the prefix. The double-colon is
				// the same separator RecurrenceDetail docs use in
				// libraries like rrule.js — easy to grep.
				clone := occ.Event
				clone.ID = fmt.Sprintf("%s::%d", occ.Event.ID, i)
				clone.StartAt = occ.StartAt
				clone.EndAt = occ.EndAt
				// Preserve recurrence on the master row of the
				// first occurrence so the UI's "edit series"
				// affordance stays available. Subsequent
				// occurrences are pure displays.
				if i > 0 {
					clone.Recurrence = ""
				}
				out = append(out, &clone)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": out})
	}
}

// createEventHandler inserts an event.
func createEventHandler(deps *Dependencies) http.HandlerFunc {
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
			Recurrence:  in.Recurrence,
		}
		if in.ProjectID != nil {
			e.ProjectID = *in.ProjectID
		}
		startAt := parseOptionalTime(in.StartAt)
		endAt := parseOptionalTime(in.EndAt)
		if startAt == nil || endAt == nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "start_at and end_at are required",
			})
			return
		}
		e.StartAt = *startAt
		e.EndAt = *endAt
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
func getEventHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		id := resolveMasterID(chi.URLParam(r, "id"))
		e, err := deps.EventService.Get(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, e)
	}
}

// updateEventHandler mutates an event.
func updateEventHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		id := resolveMasterID(chi.URLParam(r, "id"))
		existing, err := deps.EventService.Get(r.Context(), id)
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
			if t := parseOptionalTime(in.StartAt); t != nil {
				existing.StartAt = *t
			} else {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
					"error": "start_at is not a valid RFC3339 timestamp",
				})
				return
			}
		}
		if in.EndAt != "" {
			if t := parseOptionalTime(in.EndAt); t != nil {
				existing.EndAt = *t
			} else {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
					"error": "end_at is not a valid RFC3339 timestamp",
				})
				return
			}
		}
		if in.Color != "" {
			existing.Color = in.Color
		}
		// Phase 16: PATCH /events/{id} with project_id absent leaves
		// the project alone (existing semantics, friendlier than the
		// older code's ""-means-leave-alone but only by accident).
		// PATCH with project_id: "" files the event in the Inbox
		// (the mergeEventIntoTask helper handles the cascade clear of
		// column_id when the project becomes empty).
		if in.ProjectID != nil {
			existing.ProjectID = *in.ProjectID
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
func deleteEventHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.EventService == nil {
			http.Error(w, "event service not wired", http.StatusServiceUnavailable)
			return
		}
		id := resolveMasterID(chi.URLParam(r, "id"))
		if err := deps.EventService.Delete(r.Context(), id); err != nil {
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
func startTimerHandler(deps *Dependencies) http.HandlerFunc {
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
func stopTimerHandler(deps *Dependencies) http.HandlerFunc {
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
func addManualTimeHandler(deps *Dependencies) http.HandlerFunc {
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
func reportTimeHandler(deps *Dependencies) http.HandlerFunc {
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
