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

	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/course"
	"github.com/ramgml/orenda/internal/domain/study"
	"github.com/ramgml/orenda/internal/domain/task"
)

// todayResponse is the wire shape. Each list is enriched with the
// Phase 17 counters so the Today page renders without follow-up
// fetches.
//
// Phase 20.3: UpcomingWeek is a compact "next 7 days" view grouped
// by date — the dashboard renders one row per day with the count
// of due tasks.
//
// Phase 31.7: Proposals is the pending-study-reminder tray that
// the user accepts/dismisses one by one. Study reminders that the
// user has already accepted surface under DueToday (read semantics,
// never under Overdue — a missed day never turns red).
type todayResponse struct {
	Overdue        []*task.Task        `json:"overdue"`
	DueToday       []*task.Task        `json:"due_today"`
	ScheduledToday []*task.Task        `json:"scheduled_today"`
	UpcomingWeek   []upcomingDay       `json:"upcoming_week"`
	AwaitingCount  int                 `json:"awaiting_count"`
	Proposals      []studyProposalView `json:"proposals"`
	// Courses is the Task 30 slice of the owner's active courses
	// with the server-side drift marker. Always present (empty
	// array, never null) so the front-end renders without a
	// "loading" guard.
	Courses []todayCourseView `json:"courses"`
	// DueReviews (task 18): the signed-in user's spaced-repetition
	// queue (completed_at IS NULL, due_at <= now). Missed reviews stay
	// here with overdue=true — a lapsed repeat never turns red and
	// never migrates into the Overdue task list.
	DueReviews []todayReviewView `json:"due_reviews"`
	// ActiveTimer is nil when no time entry is open.
	ActiveTimer *activeTimerView `json:"active_timer,omitempty"`
}

// todayReviewView is one due spaced-repetition row on the Today
// dashboard. Overdue flags a review whose due_at predates today's UTC
// midnight — informational only (slate/indigo section on the client,
// never red).
type todayReviewView struct {
	ID          string `json:"id"`
	LessonID    string `json:"lesson_id"`
	LessonTitle string `json:"lesson_title"`
	CourseID    string `json:"course_id"`
	CourseTitle string `json:"course_title"`
	Step        int    `json:"step"`
	DueAt       string `json:"due_at"`
	Overdue     bool   `json:"overdue"`
}

// studyProposalView is the projected shape of a pending study
// proposal as rendered on the Dashboard tray. Lightweight by
// design — the front-end only needs title/course/target_date
// plus the id and dismiss handling. The full Proposal entity
// stays in the agent namespace where the planner writes it.
type studyProposalView struct {
	ID         string `json:"id"`
	CourseID   string `json:"course_id,omitempty"`
	Title      string `json:"title"`
	BodyMD     string `json:"body_md,omitempty"`
	TargetDate string `json:"target_date"`
	AgentID    string `json:"agent_id"`
	CreatedAt  string `json:"created_at"`
}

// todayCourseView is the lightweight per-course projection the
// Today page renders (Task 30): id/title for the card link plus
// the drift classification computed server-side. Only active
// courses of the requesting owner appear; draft/done/archived
// ones stay out of the dashboard.
type todayCourseView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Drift string `json:"drift"` // ahead|on_track|behind
}

// todayCourses projects the owner's active courses into
// todayCourseView rows, classifying drift over the same 14-day
// window the agent-side enrichActiveCourse uses (Task 30 keeps the
// two surfaces consistent by construction — the per-week math and
// the classifier are shared).
//
// Best-effort: a velocity/target lookup error degrades to
// drift=on_track with a warn (a broken pace signal must never take
// the whole dashboard down). deps.StudyProposals is nil-safe —
// without the repo the target count is 0 and ClassifyDrift returns
// on_track per its "no data" rule.
func todayCourses(ctx context.Context, deps *Dependencies, userID string) []todayCourseView {
	if deps.Courses == nil {
		return []todayCourseView{}
	}
	items, err := deps.Courses.ListCourses(ctx, userID)
	if err != nil {
		if deps.Logger != nil {
			deps.Logger.Warn("today courses list failed", zap.Error(err))
		}
		return []todayCourseView{}
	}
	now := time.Now().UTC()
	since := now.Add(-14 * 24 * time.Hour)
	window := 14 * 24 * time.Hour

	out := make([]todayCourseView, 0)
	for _, c := range items {
		if c.Status != course.StatusActive {
			continue
		}

		// Actual leg: done lessons in the window. The count is what
		// PerWeek needs; LastCompletedAt is the tray's concern.
		actualCount := 0
		if v, verr := deps.Courses.VelocityStatsByCourse(ctx, c.ID, since); verr != nil {
			if deps.Logger != nil {
				deps.Logger.Warn("today course pace velocity lookup failed",
					zap.String("course_id", c.ID), zap.Error(verr))
			}
		} else {
			actualCount = v.LessonsDoneInWindow
		}

		// Target leg: accepted study proposals in the same window.
		var targetCount int
		if deps.StudyProposals != nil {
			if n, terr := deps.StudyProposals.CountAcceptedInWindow(ctx, c.ID, since); terr != nil {
				if deps.Logger != nil {
					deps.Logger.Warn("today course pace target lookup failed",
						zap.String("course_id", c.ID), zap.Error(terr))
				}
			} else {
				targetCount = n
			}
		}

		drift := course.ClassifyDrift(course.PerWeek(actualCount, window), course.PerWeek(targetCount, window))
		out = append(out, todayCourseView{ID: c.ID, Title: c.Title, Drift: string(drift)})
	}
	return out
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

		overdue, dueToday, scheduled, err := listTodayTasks(r.Context(), deps, startOfDay, endOfDay)
		if err != nil {
			writeError(w, err)
			return
		}

		// Hydrate counters for the visible lists. Phase 28.22: the
		// enrichment is restricted to the visible ids — previously it
		// ran ListByProjectWithStats over EVERY task in the DB (5
		// aggregate queries on the full table) just to decorate the
		// ~dozen visible ones.
		enrichTodayCounters(r.Context(), deps, overdue, dueToday, scheduled)

		// Awaiting count: re-use the review-queue endpoint logic.
		awaiting := todayAwaitingCount(r.Context(), deps)

		// Pending study proposals — the tray the user accepts
		// or dismisses one by one. nil-safe (deps.StudyService
		// is set by the production wiring but tests may omit it).
		proposals := todayProposals(r.Context(), deps)

		// Active timer — look up the owner's open entry via the
		// time-entry service. Phase 4's single-active-timer invariant
		// is per-agent; for single-owner installs we probe by the
		// owner id (Phase 9 will wire a proper owner→agent map).
		active := todayActiveTimer(r.Context(), deps, userID)

		// Upcoming week: due dates in (today, today+7d), bucketed by date.
		week := upcomingWeek(r.Context(), deps, endOfDay)

		// Active courses of the owner with the drift marker (Task 30).
		// nil-safe: an unwired Courses repo yields an empty array.
		courses := todayCourses(r.Context(), deps, userID)
		// Task 18: spaced-repetition queue. nil-safe — early fixtures
		// don't wire the review service and simply render an empty
		// section. overdue = due_at < startOfDay (informational; the
		// row stays in due_reviews, never in the red Overdue list).
		dueReviews := todayDueReviews(r.Context(), deps, userID, now, startOfDay)

		writeJSON(w, http.StatusOK, todayResponse{
			Overdue:        overdue,
			DueToday:       dueToday,
			ScheduledToday: scheduled,
			UpcomingWeek:   week,
			AwaitingCount:  awaiting,
			Proposals:      proposals,
			Courses:        courses,
			DueReviews:     dueReviews,
			ActiveTimer:    active,
		})
	}
}

// projectProposalViews turns the service-level Pending proposals
// into the lightweight Today-shape projection. Done in the
// handler so the wire shape stays decoupled from the domain
// entity's full field set.
func projectProposalViews(items []*study.Proposal) []studyProposalView {
	out := make([]studyProposalView, 0, len(items))
	for _, p := range items {
		out = append(out, studyProposalView{
			ID:         p.ID,
			CourseID:   p.CourseID,
			Title:      p.Title,
			BodyMD:     p.BodyMD,
			TargetDate: p.TargetDate,
			AgentID:    p.CreatedByAgent,
			CreatedAt:  p.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
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

// listTodayTasks lists and filters the three task collections the
// dashboard shows: overdue (due before today, study reminders
// excluded — "missed day never turns red", Phase 31.7), due today
// (due in [today, tomorrow), plus study reminders filed on a
// previous day so the user can still ack/dismiss them), and
// scheduled today (calendar items overlapping today).
func listTodayTasks(ctx context.Context, deps *Dependencies, startOfDay, endOfDay time.Time) (overdue, dueToday, scheduled []*task.Task, err error) {
	// Overdue: due_at < startOfDay AND status NOT done. We list
	// across all projects (NoProject=false, ProjectID="") and
	// filter by status in code — the kanban "list everything"
	// path doesn't accept a date range.
	//
	// Phase 31.7: study-reminders (tasks with study_course_id
	// set) are EXCLUDED here. The product rule is "missed day
	// never turns red" — only genuine project tasks escalate
	// into overdue. The reminder still shows up under due_today
	// for the current day.
	overdue, err = deps.Tasks.ListByProject(ctx, task.Filter{
		Status: task.StatusTodo, // simplified: only open tasks; we
		// could also include in_progress but the dashboard is
		// about "still owed today" so we focus on todo + review.
	})
	if err != nil {
		return nil, nil, nil, err
	}
	overdueFiltered := overdue[:0]
	for _, t := range overdue {
		if t.StudyCourseID != "" {
			continue
		}
		if t.DueAt != nil && t.DueAt.Before(startOfDay) {
			overdueFiltered = append(overdueFiltered, t)
		}
	}
	overdue = overdueFiltered

	// Due today: due_at between startOfDay and endOfDay.
	// Study-reminders with due_at <= today are included so the
	// user sees them in the "today" list even if they were
	// filed on a previous day (no escalation, no missed-entry).
	dueToday, err = deps.Tasks.ListByProject(ctx, task.Filter{
		Status: task.StatusTodo,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	dueTodayFiltered := dueToday[:0]
	for _, t := range dueToday {
		if t.DueAt == nil {
			continue
		}
		// Include if due in the [today, tomorrow) window, OR
		// if it's a study-reminder filed on a previous day
		studyCarry := t.StudyCourseID != "" && !t.DueAt.After(startOfDay)
		inWindow := !t.DueAt.Before(startOfDay) && t.DueAt.Before(endOfDay)
		if studyCarry || inWindow {
			dueTodayFiltered = append(dueTodayFiltered, t)
		}
	}
	dueToday = dueTodayFiltered

	// Scheduled today: tasks with both start_at and end_at set
	// that overlap today (calendar items).
	scheduled, err = deps.Tasks.ListInRange(ctx, startOfDay, endOfDay, "")
	if err != nil {
		return nil, nil, nil, err
	}
	return overdue, dueToday, scheduled, nil
}

// enrichTodayCounters hydrates Counters/BlockedByCount for the
// visible lists. Phase 28.22: the enrichment is restricted to the
// visible ids — previously it ran ListByProjectWithStats over EVERY
// task in the DB (5 aggregate queries on the full table) just to
// decorate the ~dozen visible ones. A stats failure is tolerated:
// the lists render undecorated.
func enrichTodayCounters(ctx context.Context, deps *Dependencies, overdue, dueToday, scheduled []*task.Task) {
	if len(overdue)+len(dueToday)+len(scheduled) == 0 {
		return
	}
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
	enriched, err := deps.Tasks.ListByProjectWithStats(ctx, task.Filter{IDs: ids})
	if err == nil {
		enrichByID(enriched, overdue)
		enrichByID(enriched, dueToday)
		enrichByID(enriched, scheduled)
	}
}

// todayAwaitingCount returns how many tasks await the owner's
// review verdict. Re-uses the review-queue endpoint logic; a
// failure counts as zero (the dashboard tolerates a broken queue).
func todayAwaitingCount(ctx context.Context, deps *Dependencies) int {
	awaiting := 0
	if deps.Tasks != nil {
		items, err := deps.Tasks.ListAwaitingReview(ctx)
		if err == nil {
			awaiting = len(items)
		}
	}
	return awaiting
}

// todayProposals lists the pending study proposals — the tray the
// user accepts or dismisses one by one. nil-safe (deps.StudyService
// is set by the production wiring but tests may omit it); a listing
// failure degrades to an empty tray.
func todayProposals(ctx context.Context, deps *Dependencies) []studyProposalView {
	proposals := []studyProposalView{}
	if deps.StudyService != nil {
		pending, err := deps.StudyService.ListPending(ctx)
		if err == nil {
			proposals = projectProposalViews(pending)
		}
	}
	return proposals
}

// todayActiveTimer returns the owner's open time entry, if any.
// Phase 4's single-active-timer invariant is per-agent; for
// single-owner installs we probe by the owner id (Phase 9 will
// wire a proper owner→agent map).
func todayActiveTimer(ctx context.Context, deps *Dependencies, userID string) *activeTimerView {
	if deps.TimeService != nil && userID != "" {
		if te, err := deps.TimeService.ActiveTimer(ctx, userID); err == nil && te != nil {
			return &activeTimerView{
				TaskID:    te.TaskID,
				StartedAt: te.StartedAt,
			}
		}
	}
	return nil
}

// todayDueReviews projects the review service's due queue onto the
// Today wire shape (task 18). nil-safe on deps.ReviewService.
func todayDueReviews(ctx context.Context, deps *Dependencies, userID string, now, startOfDay time.Time) []todayReviewView {
	if deps.ReviewService == nil || userID == "" {
		return []todayReviewView{}
	}
	items, err := deps.ReviewService.ListDue(ctx, userID, now)
	if err != nil {
		// The dashboard must render even if the review queue hiccups;
		// an empty section is the conservative degradation.
		return []todayReviewView{}
	}
	out := make([]todayReviewView, 0, len(items))
	for _, it := range items {
		out = append(out, todayReviewView{
			ID:          it.ID,
			LessonID:    it.LessonID,
			LessonTitle: it.LessonTitle,
			CourseID:    it.CourseID,
			CourseTitle: it.CourseTitle,
			Step:        it.Step,
			DueAt:       it.DueAt.UTC().Format(time.RFC3339),
			Overdue:     it.DueAt.Before(startOfDay),
		})
	}
	return out
}
