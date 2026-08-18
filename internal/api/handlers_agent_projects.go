// Package api — agent-namespace project handlers
// (wiki:agent-project-description).
//
// Pre-this-phase, the project description was a user-only surface:
// an agent token got 401 on GET /api/v1/projects/{id} and had no
// write path at all. The DOGFOOD convention has agents maintain
// project documentation (roadmap, постановки, decision log), so the
// agent must be able to read and update the description — e.g. to
// set a link to the project's wiki page (wiki:project-wiki-link).
//
//	GET   /api/v1/agent/projects/{id}   read the project
//	PATCH /api/v1/agent/projects/{id}   update description (v1)
//
// Design decisions (wiki:agent-project-description):
//   - v1 exposes ONLY the description field; name/color/archived
//     stay user-only. The wiki_slug field lands with the
//     project-wiki-link task and is added to this handler then.
//   - Any authenticated agent may read/write (local-first,
//     single-owner; agents are global, not project-scoped).
//     Audit instead of ACL: a project_activity row with
//     actor_type=agent and a before/after diff payload.
//   - Namespace split is symmetric: cookie sessions 401 on agent
//     routes (RequireAgent only accepts bearer API tokens), agent
//     tokens 401 on user routes (RequireUser only accepts JWTs).
package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/project"
)

// agentGetProjectHandler returns one project to the bearer agent.
// Same shape as the user-side getProjectHandler — the agent needs
// the full row (name, color, description, archived) to reason about
// the project; there is no per-field ACL in the single-owner model.
func agentGetProjectHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id == nil || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		p, err := deps.Projects.GetProject(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// agentPatchProjectRequest is the JSON body of
// PATCH /api/v1/agent/projects/{id}.
//
// Pointer field distinguishes "leave alone" (nil) from "set to empty
// string" (non-nil pointing at "") — same semantics as the user-side
// projectInput.Description. v1 carries only description; wiki_slug
// joins when wiki:project-wiki-link merges.
type agentPatchProjectRequest struct {
	Description *string `json:"description"`
}

// agentPatchProjectHandler updates the project description on behalf
// of the bearer agent. Writes a project_activity row
// (kind=description_changed, payload carries the before/after diff)
// and publishes a WS event on the "projects" topic so the UI
// refreshes live.
//
// Validation mirrors the user-side patchProjectHandler: the only
// rule the description field has is "any string is valid" (empty
// clears it). Name/color/archived are intentionally NOT accepted
// here — v1 scope per the wiki постановка.
func agentPatchProjectHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id == nil || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		projectID := chi.URLParam(r, "id")
		p, err := deps.Projects.GetProject(r.Context(), projectID)
		if err != nil {
			writeError(w, err)
			return
		}
		var in agentPatchProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Description == nil {
			// Nothing to change — return the current row rather than
			// write a no-op activity row.
			writeJSON(w, http.StatusOK, p)
			return
		}
		before := p.Description
		p.Description = *in.Description
		if err := deps.Projects.UpdateProject(r.Context(), p); err != nil {
			writeError(w, err)
			return
		}
		// Audit: project_activity row with the diff. Log-and-continue
		// on failure — an audit gap must not fail the user-visible
		// mutation (same convention as ActivityRecorder callers).
		if deps.ProjectActivityRecorder != nil && before != p.Description {
			payload, _ := json.Marshal(map[string]string{
				"before": before,
				"after":  p.Description,
			})
			if rerr := deps.ProjectActivityRecorder.RecordProjectAuto(
				r.Context(), projectID,
				project.ActivityDescriptionChanged, string(payload),
			); rerr != nil && deps.Logger != nil {
				deps.Logger.Warn("project activity record failed",
					zap.String("project_id", projectID),
					zap.Error(rerr),
				)
			}
		}
		// Live update: WS event on the "projects" topic so the
		// project page / settings refresh without a reload.
		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "projects",
				Body: map[string]any{
					"type":       "project.updated",
					"project":    p,
					"actor_type": "agent",
					"actor_id":   id.AgentID,
				},
			})
		}
		writeJSON(w, http.StatusOK, p)
	}
}
