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
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/ramgml/orenda/internal/domain/task"
)

// todayResponse is the wire shape. Each list is enriched with the
// Phase 17 counters so the Today page renders without follow-up
// fetches.
//
// Phase 20.3: UpcomingWeek is a compact "next 7 days" view grouped
// by date — the dashboard renders one row per day with the count
// of due tasks.
type todayResponse struct {
	Overdue        []*task.Task  `json:"overdue"`
	DueToday       []*task.Task  `json:"due_today"`
	ScheduledToday []*task.Task  `json:"scheduled_today"`
	UpcomingWeek   []upcomingDay `json:"upcoming_week"`
	AwaitingCount  int           `json:"awaiting_count"`
	// ActiveTimer is nil when no time entry is open.
	ActiveTimer *activeTimerView `json:"active_timer,omitempty"`
}

// upcomingDay is one row in the "next 7 days" section.
//
// Date is an ISO 8601 date string (YYYY-MM-DD) so the client can
// format it in the user's locale without timezone arithmetic.
// Count is the number of due tasks falling on that day.
type upcomingDay struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type activeTimerView struct {
	TaskID    string    `json:"task_id"`
	AgentID   string    `json:"agent_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// getTodayHandler returns the dashboard payload.
func getTodayHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		userID := ""
		if id, ok := IdentityFrom(r.Context()); ok {
			userID = id.UserID
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

		// Active timer — look up the owner's open entry via the
		// time-entry service. Phase 4's single-active-timer invariant
		// is per-agent; for single-owner installs we probe by the
		// owner id (Phase 9 will wire a proper owner→agent map).
		var active *activeTimerView
		if deps.TimeService != nil && userID != "" {
			if te, err := deps.TimeService.ActiveTimer(r.Context(), userID); err == nil && te != nil {
				active = &activeTimerView{
					TaskID:    te.TaskID,
					StartedAt: te.StartedAt,
				}
			}
		}

		// Upcoming week: due dates in (today, today+7d), bucketed by date.
		week := upcomingWeek(r.Context(), deps, endOfDay)

		writeJSON(w, http.StatusOK, todayResponse{
			Overdue:        overdue,
			DueToday:       dueToday,
			ScheduledToday: scheduled,
			UpcomingWeek:   week,
			AwaitingCount:  awaiting,
			ActiveTimer:    active,
		})
	}
}

// upcomingWeek groups tasks by their due date over the next 7
// days (exclusive of today — today is already in due_today).
//
// Phase 20.3 ships a compact, flat response: one row per day with
// the day label as YYYY-MM-DD. The client renders a one-line-per-
// day section without timezone arithmetic.
func upcomingWeek(ctx context.Context, deps *Dependencies, endOfDay time.Time) []upcomingDay {
	// Window: [tomorrow, today+7d).
	windowStart := endOfDay
	windowEnd := endOfDay.Add(7 * 24 * time.Hour)

	all, err := deps.Tasks.ListByProject(ctx, task.Filter{
		Status: task.StatusTodo,
	})
	if err != nil {
		return nil
	}
	bucket := map[string]int{}
	for _, t := range all {
		if t.DueAt == nil {
			continue
		}
		if t.DueAt.Before(windowStart) || !t.DueAt.Before(windowEnd) {
			continue
		}
		key := t.DueAt.UTC().Format("2006-01-02")
		bucket[key]++
	}
	out := make([]upcomingDay, 0, len(bucket))
	for k, v := range bucket {
		out = append(out, upcomingDay{Date: k, Count: v})
	}
	// Stable order: by date ascending.
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
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
