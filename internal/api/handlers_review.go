// Package api — Phase 19 review-queue handler.
//
// Closes the agent → human half of the delegation loop. Until now
// "task.review_needed" was a one-off notification that was easy to
// miss; the review queue gives the human one screen with everything
// waiting for their verdict, plus inline accept / reject buttons.
//
// Endpoints (all under RequireUser):
//
//	GET /api/v1/review-queue           — {tasks: [...], count: N}
//	GET /api/v1/review-queue/count     — {count: N} (cheap, used by sidebar badge)
//
// The accept/reject actions themselves are POST /api/v1/tasks/{id}/review,
// which has been around since Phase 3 — we just surface it on this page.
package api

import (
	"net/http"
)

// listReviewQueueHandler returns every task awaiting human action,
// newest-first.
//
// The response carries both a `tasks` array (for the page) and a
// `count` (for the sidebar badge). The two come from the same query,
// so the page renders in one round-trip.
func listReviewQueueHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		items, err := deps.Tasks.ListAwaitingReview(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": items,
			"count": len(items),
		})
	}
}

// reviewQueueCountHandler returns just the count. The sidebar badge
// polls this on every WS event so it stays in sync without rendering
// the full list.
//
// In practice the underlying query is the same; the lighter response
// keeps the badge cheap when the queue grows.
func reviewQueueCountHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		items, err := deps.Tasks.ListAwaitingReview(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(items)})
	}
}

// ---------------------------------------------------------------------------
// Agent-starved queue (T336)
// ---------------------------------------------------------------------------
//
// The owner-side half of the Task 140 project-scope filter: tasks
// with awaiting='agent' in projects where agents_allowed = 0 and no
// grant rows exist. Those rows can never surface through
// /agent/tasks?ready=true — the agent queue answers "no work" while
// the board says agents have work. These endpoints make the
// starvation visible instead of silent:
//
//	GET /api/v1/agent-starved        — {tasks: [...], count: N}
//	GET /api/v1/agent-starved/count  — {count: N} (badge/announcement use)
//
// Same owner-only posture as /review-queue (RequireUser).

// listAgentStarvedHandler returns stranded agent-awaiting work with
// the project name and grant count denormalised per row.
func listAgentStarvedHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		items, err := deps.Tasks.ListAgentStarved(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"tasks": items,
			"count": len(items),
		})
	}
}

// agentStarvedCountHandler returns just the count.
func agentStarvedCountHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Tasks == nil {
			http.Error(w, "task repo not wired", http.StatusServiceUnavailable)
			return
		}
		items, err := deps.Tasks.ListAgentStarved(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"count": len(items)})
	}
}
