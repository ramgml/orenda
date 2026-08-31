// Package api — Task 107: system overview endpoint for the Dashboard.
//
// GET /api/v1/overview aggregates system-level metrics: entity counts
// (projects, tasks by status, wiki pages, calendar events) and a
// created-vs-completed task activity series. Unlike /api/v1/today
// (the personal daily slice) this is the "readings of the system"
// surface the Dashboard screen renders.
package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/wiki"
)

// overviewResponse is the wire shape of GET /api/v1/overview.
type overviewResponse struct {
	// Projects is the total number of projects (including archived).
	Projects int `json:"projects"`
	// TasksByStatus counts tasks per status key.
	TasksByStatus map[string]int `json:"tasks_by_status"`
	// WikiPages is the total number of wiki pages.
	WikiPages int `json:"wiki_pages"`
	// Events is the number of timed items (calendar events) in the
	// last 30 days — cheap proxy for calendar load.
	Events int `json:"events"`
	// Activity is the per-day created/completed series over the last
	// 30 days, oldest first. Days with zero activity are included so
	// the client can draw a continuous chart without gap-filling.
	Activity []activityDay `json:"activity"`
}

// activityDay is one row of the activity series.
type activityDay struct {
	// Date is an ISO 8601 date (YYYY-MM-DD).
	Date string `json:"date"`
	// Created is how many tasks were created that day.
	Created int `json:"created"`
	// Completed is how many tasks reached done that day.
	Completed int `json:"completed"`
}

// getOverviewHandler returns the system-overview payload. Every
// dependency is nil-safe: a partial test fixture gets zeros, not a 500.
func getOverviewHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		windowStart := now.AddDate(0, 0, -29)
		startOfDay := time.Date(windowStart.Year(), windowStart.Month(), windowStart.Day(), 0, 0, 0, 0, time.UTC)

		resp := overviewResponse{
			TasksByStatus: map[string]int{},
			Activity:      []activityDay{},
		}

		if deps.Tasks != nil {
			all, err := deps.Tasks.ListByProject(r.Context(), task.Filter{})
			if err == nil {
				byDay := newActivityBuckets(startOfDay, 30)
				for _, t := range all {
					resp.TasksByStatus[string(t.Status)]++
					if d, ok := dayKey(t.CreatedAt, startOfDay, now); ok {
						byDay[d].Created++
					}
					if t.CompletedAt != nil {
						if d, ok := dayKey(*t.CompletedAt, startOfDay, now); ok {
							byDay[d].Completed++
						}
					}
				}
				resp.Activity = flattenActivity(byDay)
			}
		}

		if deps.Projects != nil && deps.Tasks != nil {
			// ListProjects is scoped to an owner; the single-owner
			// convention (same as /today) means any known user id
			// sees the full set. We resolve the caller's id.
			if id, ok := IdentityFrom(r.Context()); ok {
				projects, err := deps.Projects.ListProjects(r.Context(), id.UserID)
				if err == nil {
					resp.Projects = len(projects)
				}
			}
		}

		if deps.WikiService != nil {
			if tree, err := deps.WikiService.Tree(r.Context()); err == nil {
				resp.WikiPages = countTreeNodes(tree)
			}
		}

		if deps.Tasks != nil {
			if evs, err := deps.Tasks.ListInRange(r.Context(), startOfDay, now.Add(24*time.Hour), ""); err == nil {
				resp.Events = len(evs)
			}
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// activityBuckets holds per-day counters keyed by YYYY-MM-DD.
type activityBuckets map[string]*activityDay

// newActivityBuckets pre-creates n day buckets starting at start so
// zero-activity days render as a continuous series.
func newActivityBuckets(start time.Time, n int) activityBuckets {
	m := make(activityBuckets, n)
	for i := 0; i < n; i++ {
		d := start.AddDate(0, 0, i)
		m[d.Format("2006-01-02")] = &activityDay{Date: d.Format("2006-01-02")}
	}
	return m
}

// dayKey returns the YYYY-MM-DD key for t when it falls inside the
// window [start, endExclusive).
func dayKey(t time.Time, start, end time.Time) (string, bool) {
	if t.Before(start) || !t.Before(end) {
		return "", false
	}
	return t.UTC().Format("2006-01-02"), true
}

// flattenActivity returns the buckets sorted by date, oldest first.
func flattenActivity(m activityBuckets) []activityDay {
	out := make([]activityDay, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// countTreeNodes counts every page in a wiki tree.
func countTreeNodes(nodes []*wiki.TreeNode) int {
	n := 0
	for _, node := range nodes {
		n++
		n += countTreeNodes(node.Children)
	}
	return n
}
