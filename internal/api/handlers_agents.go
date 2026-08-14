// Package api — agent handlers.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/agent"
	agentsvc "github.com/ramgml/orenda/internal/service/agent"
)

// createAgentRequest is the JSON body for POST /api/v1/agents.
//
// Phase 28.19: Type is now a free-form label set, not a single string
// drawn from a fixed enum. Old payloads with `"type": "qwen"` are
// rejected with 400 — there is no implicit migration path because the
// caller can always resend the value as `["qwen"]`.
type createAgentRequest struct {
	Name        string   `json:"name"`
	Type        []string `json:"type"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
}

// listAgentsHandler returns every registered agent, optionally filtered
// by a query-string set of labels. Repeatable `?type=qwen&type=installer`
// applies OR-semantics: an agent matches when at least one of its labels
// appears in the filter set. An empty filter (no `type` params) returns
// every agent — the in-memory filter is cheap at the current scale.
func listAgentsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents, err := deps.Agents.List(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		if labels := r.URL.Query()["type"]; len(labels) > 0 {
			agents = filterAgentsByLabels(agents, labels)
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	}
}

// filterAgentsByLabels returns the subset of agents whose label set has
// at least one label in the requested filter. Both sides are normalised
// to lowercase so "Qwen" and "qwen" compare equal.
func filterAgentsByLabels(agents []*agent.Agent, filter []string) []*agent.Agent {
	want := make(map[string]struct{}, len(filter))
	for _, f := range filter {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" {
			want[f] = struct{}{}
		}
	}
	if len(want) == 0 {
		return agents
	}
	out := make([]*agent.Agent, 0, len(agents))
	for _, a := range agents {
		for _, l := range a.Type {
			if _, ok := want[strings.ToLower(strings.TrimSpace(l))]; ok {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// createAgentHandler mints a fresh API token and returns it (once) along
// with the new agent row.
func createAgentHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_name"})
			return
		}
		out, err := callAgentServiceRegister(r.Context(), deps, req.Name, req.Type, req.Description, req.Scopes)
		if err != nil {
			if errors.Is(err, agentsvc.ErrNameTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "name_taken"})
				return
			}
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"agent":       out.Agent,
			"plain_token": out.PlainToken,
		})
	}
}

// getAgentHandler returns one agent by id.
func getAgentHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := deps.Agents.GetByID(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// deleteAgentHandler removes an agent (cascades to api_tokens via FK).
func deleteAgentHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := deps.Agents.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// heartbeatRequest is the JSON body for POST /api/v1/agents/:id/heartbeat.
type heartbeatRequest struct {
	Status string `json:"status"`
}

// heartbeatHandler updates last_seen_at + status for an agent.
func heartbeatHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req heartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		a, err := deps.Agents.TouchLastSeen(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		if deps.WSHub != nil {
			deps.WSHub.Publish(r.Context(), ws.Event{
				Topic: "agents",
				Body:  map[string]any{"type": "agent.heartbeat", "agent": a},
			})
		}
		writeJSON(w, http.StatusOK, a)
	}
}

// callAgentServiceRegister centralises the agent-creation path so it can
// be overridden in tests. Production wires deps.AgentService directly.
func callAgentServiceRegister(ctx context.Context, deps *Dependencies, name string, labels []string, desc string, scopes []string) (*agentsvc.Registered, error) {
	if deps.AgentService != nil {
		return deps.AgentService.Register(ctx, name, labels, desc, scopes)
	}
	return nil, errors.New("agent service not wired")
}
