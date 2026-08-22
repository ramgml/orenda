// Package api — agent-namespace project handlers
// (wiki:agent-project-description + wiki:project-wiki-link).
//
// Pre-this-phase, the project description was a user-only surface:
// an agent token got 401 on GET /api/v1/projects/{id} and had no
// write path at all. The DOGFOOD convention has agents maintain
// project documentation (roadmap, постановки, decision log), so the
// agent must be able to read and update the description — and, after
// project-wiki-link merges, link the project to its wiki page by
// writing wiki_slug.
//
//	GET   /api/v1/agent/projects/{id}   read the project
//	PATCH /api/v1/agent/projects/{id}   update description + wiki_slug
//
// Design decisions:
//   - v1 of agent-project-description exposed only description. project-
//     wiki-link adds wiki_slug on top — same handler, separate
//     ActivityWikiSlugChanged kind keeps the audit feed diff readable.
//   - Any authenticated agent may read/write (local-first,
//     single-owner; agents are global, not project-scoped).
//     Audit instead of ACL: a project_activity row with
//     actor_type=agent and a before/after diff payload.
//   - Namespace split is symmetric: cookie sessions 401 on agent
//     routes (RequireAgent only accepts bearer API tokens), agent
//     tokens 401 on user routes (RequireUser only accepts JWTs).
//   - name / color / archived stay user-only — see the wiki
//     постановка for the rationale.
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/wiki"
)

// agentGetProjectHandler returns one project to the bearer agent.
// Same shape as the user-side getProjectHandler — the agent needs
// the full row (name, color, description, archived, wiki_slug) to
// reason about the project; there is no per-field ACL in the
// single-owner model.
func agentGetProjectHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id == nil || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		p, err := resolveProjectRef(r.Context(), deps, chi.URLParam(r, "id"))
		if err != nil {
			writeProjectResolveError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

// agentPatchProjectRequest is the JSON body of
// PATCH /api/v1/agent/projects/{id}.
//
// Pointer fields distinguish "leave alone" (nil) from "set to empty
// string" (non-nil pointing at "") — same semantics as the user-side
// projectInput. Description and wiki_slug are the two writable
// fields in the agent namespace.
type agentPatchProjectRequest struct {
	Description *string `json:"description"`
	WikiSlug    *string `json:"wiki_slug"`
}

// agentPatchProjectHandler updates the project description and / or
// wiki_slug on behalf of the bearer agent.
//
// For each changed field the handler writes a project_activity row
// (description_changed or wiki_slug_changed) with a before/after diff
// payload, and publishes a single WS event on the "projects" topic
// so the UI refreshes without a reload. No-ops (request body with
// neither key) return the current row and skip the activity row —
// keeping the audit feed clean.
//
// Validation:
//   - description: any string (including "" — clears the field).
//   - wiki_slug: trimmed whitespace; "" → unlink; non-empty → must
//     reference an existing wiki page or 422. Mirrors the user-side
//     patchProjectHandler semantics so the two surfaces stay in sync.
func agentPatchProjectHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id == nil || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		p, err := resolveProjectRef(r.Context(), deps, chi.URLParam(r, "id"))
		if err != nil {
			writeProjectResolveError(w, err)
			return
		}
		var in agentPatchProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if in.Description == nil && in.WikiSlug == nil {
			// Nothing to change — return the current row rather than
			// write a no-op activity row.
			writeJSON(w, http.StatusOK, p)
			return
		}
		beforeDesc := p.Description
		beforeSlug := p.WikiSlug
		descChanged := false
		slugChanged := false
		if in.Description != nil {
			p.Description = *in.Description
			descChanged = beforeDesc != p.Description
		}
		if in.WikiSlug != nil {
			slug := strings.TrimSpace(*in.WikiSlug)
			if slug == "" {
				p.WikiSlug = ""
			} else {
				if _, gerr := deps.WikiService.GetBySlug(r.Context(), slug); gerr != nil {
					status := http.StatusInternalServerError
					body := map[string]string{"error": "wiki_slug_lookup_failed"}
					if isWikiNotFound(gerr) {
						status = http.StatusUnprocessableEntity
						body = map[string]string{"error": "wiki_slug_not_found", "slug": slug}
					}
					writeJSON(w, status, body)
					return
				}
				p.WikiSlug = slug
			}
			slugChanged = beforeSlug != p.WikiSlug
		}
		if err := deps.Projects.UpdateProject(r.Context(), p); err != nil {
			writeError(w, err)
			return
		}
		// Audit: one activity row per changed field. Log-and-continue on
		// recorder failure — an audit gap must not fail the user-visible
		// mutation (same convention as ActivityRecorder callers).
		if deps.ProjectActivityRecorder != nil {
			if descChanged {
				payload, _ := json.Marshal(map[string]string{
					"before": beforeDesc,
					"after":  p.Description,
				})
				if rerr := deps.ProjectActivityRecorder.RecordProjectAuto(
					r.Context(), p.ID,
					project.ActivityDescriptionChanged, string(payload),
				); rerr != nil && deps.Logger != nil {
					deps.Logger.Warn("project activity record failed",
						zap.String("project_id", p.ID),
						zap.String("kind", "description_changed"),
						zap.Error(rerr),
					)
				}
			}
			if slugChanged {
				payload, _ := json.Marshal(map[string]string{
					"before": beforeSlug,
					"after":  p.WikiSlug,
				})
				if rerr := deps.ProjectActivityRecorder.RecordProjectAuto(
					r.Context(), p.ID,
					project.ActivityWikiSlugChanged, string(payload),
				); rerr != nil && deps.Logger != nil {
					deps.Logger.Warn("project activity record failed",
						zap.String("project_id", p.ID),
						zap.String("kind", "wiki_slug_changed"),
						zap.Error(rerr),
					)
				}
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

// isWikiNotFound returns true for wiki.ErrNotFound, ignoring other
// errors so the caller can keep its existing writeError fallback
// path. The check is loose on purpose — anything that isn't a
// well-formed "not found" bubbles up to the operator as a 500 rather
// than silently turning into a 422.
func isWikiNotFound(err error) bool {
	return err != nil && (err == wiki.ErrNotFound ||
		strings.Contains(err.Error(), wiki.ErrNotFound.Error()))
}
