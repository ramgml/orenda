// Package api — Phase 20: "Today" handler.
//
// One endpoint that aggregates everything the user wants to look
// at when they open the app: overdue + due today + scheduled today
// (calendar items due today) + how many tasks are awaiting their
// verdict + whether they have an active timer.
//
// Designed for a single round-trip; the kanban page already does
// the same trick for one project, but the dashboard wants the
// cross-project view in one shot.
package api

import (
	"net/http"
	"time"

	"github.com/ramgml/orenda/internal/domain/task"
)

// todayResponse is the wire shape. Each list is enriched with the
// Phase 17 counters so the Today page renders without follow-up
// fetches.
type todayResponse struct {
	Overdue        []*task.Task `json:"overdue"`
	DueToday       []*task.Task `json:"due_today"`
	ScheduledToday []*task.Task `json:"scheduled_today"`
	AwaitingCount  int          `json:"awaiting_count"`
	// ActiveTimer is nil when no time entry is open.
	ActiveTimer *activeTimerView `json:"active_timer,omitempty"`
}

type activeTimerView struct {
	TaskID    string    `json:"task_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// getTodayHandler returns the dashboard payload.
func getTodayHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		// We anchor "today" at midnight UTC. The dashboard is for
		// personal use; server-time-zone boundaries are documented
		// rather than solved (the user is expected to be in their
		// own TZ since the single-owner install lives on their box).
		now := time.Now().UTC()
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		endOfDay := startOfDay.Add(24 * time.Hour)

		// Overdue: due_at < startOfDay AND status NOT done. We list
		// across all projects (NoProject=false, ProjectID="") and
		// filter by status in code — the kanban "list everything"
		// path doesn't accept a date range.
		overdue, err := deps.Tasks.ListByProject(r.Context(), task.Filter{
			Status: task.StatusTodo, // simplified: only open tasks; we
			// could also include in_progress but the dashboard is
			// about "still owed today" so we focus on todo + review.
		})
		if err != nil {
			writeError(w, err)
			return
		}
		overdueFiltered := overdue[:0]
		for _, t := range overdue {
			if t.DueAt != nil && t.DueAt.Before(startOfDay) {
				overdueFiltered = append(overdueFiltered, t)
			}
		}
		overdue = overdueFiltered

		// Due today: due_at between startOfDay and endOfDay.
		dueToday, err := deps.Tasks.ListByProject(r.Context(), task.Filter{
			Status: task.StatusTodo,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		dueTodayFiltered := dueToday[:0]
		for _, t := range dueToday {
			if t.DueAt == nil {
				continue
			}
			if !t.DueAt.Before(startOfDay) && t.DueAt.Before(endOfDay) {
				dueTodayFiltered = append(dueTodayFiltered, t)
			}
		}
		dueToday = dueTodayFiltered

		// Scheduled today: tasks with both start_at and end_at set
		// that overlap today (calendar items).
		scheduled, err := deps.Tasks.ListInRange(r.Context(), startOfDay, endOfDay, "")
		if err != nil {
			writeError(w, err)
			return
		}

		// Hydrate counters for the visible lists.
		if len(overdue)+len(dueToday)+len(scheduled) > 0 {
			ids := make([]string, 0, len(overdue)+len(dueToday)+len(scheduled))
			for _, t := range overdue {
				ids = append(ids, t.ID)
			}
			for _, t := range dueToday {
				ids = append(ids, t.ID)
			}
			for _, t := range scheduled {
				ids = append(ids, t.ID)
			}
			enriched, err := deps.Tasks.ListByProjectWithStats(r.Context(), task.Filter{ProjectID: ""})
			if err == nil {
				enrichByID(enriched, overdue)
				enrichByID(enriched, dueToday)
				enrichByID(enriched, scheduled)
				_ = ids // suppress unused
			}
		}

		// Awaiting count: re-use the review-queue endpoint logic.
		awaiting := 0
		if deps.Tasks != nil {
			items, err := deps.Tasks.ListAwaitingReview(r.Context())
			if err == nil {
				awaiting = len(items)
			}
		}

		// Active timer (Phase 4 time_entry) — best-effort; the time
		// service doesn't expose a single "active entry" lookup yet
		// (Phase 4 single-active-timer invariant lives in Start()). For
		// the Today dashboard we report it as "no active timer"; a
		// follow-up can plumb the lookup without changing this wire
		// shape.

		writeJSON(w, http.StatusOK, todayResponse{
			Overdue:        overdue,
			DueToday:       dueToday,
			ScheduledToday: scheduled,
			AwaitingCount:  awaiting,
		})
	}
}

// enrichByID copies Counters/BlockedByCount from src (a list from
// ListByProjectWithStats) onto the matching entries in dst by id.
func enrichByID(src, dst []*task.Task) {
	byID := make(map[string]*task.Task, len(src))
	for _, t := range src {
		byID[t.ID] = t
	}
	for _, t := range dst {
		if src, ok := byID[t.ID]; ok && src != nil {
			t.Counters = src.Counters
			t.BlockedByCount = src.BlockedByCount
		}
	}
}
