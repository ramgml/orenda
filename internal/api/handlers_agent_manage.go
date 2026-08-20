// Package api — Phase 33.2: agent-side task management endpoints.
//
//	GET    /api/v1/agent/tasks/{id}              — single-task read (TODO)
//	PATCH  /api/v1/agent/tasks/{id}              — edit own un-triaged proposal
//	                                            or update agent_notes when holder
//	DELETE /api/v1/agent/tasks/{id}              — retract own un-triaged proposal
//	GET    /api/v1/agent/tasks/{id}/context      — context snapshot (any agent)
//
// The agent namespace (RequireAgent) means a cookie session can never
// reach these handlers — they all resolve the agent identity from the
// bearer token. Permission decisions live in the service layer
// (internal/service/task/manage.go) so this file stays transport-only:
//
//   - decode wire → service patch
//   - call service
//   - translate sentinel errors into 4xx
//   - audit + WS broadcast are already wired inside the service
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ramgml/orenda/internal/domain/task"
	taskservice "github.com/ramgml/orenda/internal/service/task"
)

// agentTaskPatchInput is the wire shape for PATCH /api/v1/agent/tasks/{id}.
// Pointer fields distinguish "leave alone" (nil) from "explicitly
// clear / set". The service translates this into EditProposalPatch.
//
// Per the wiki:agent-task-management spec:
//
//   - title, description_md, priority, due_at, parent_task_id
//     apply when the caller is the agent who originally proposed the
//     task and the task is still un-triaged (status=backlog +
//     awaiting=human).
//   - agent_notes applies when the caller currently holds task_locks
//     for the task. Mixed PATCHes (notes + anything else) are
//     rejected unless the caller also meets the proposal-gate — the
//     service decides which path to take.
type agentTaskPatchInput struct {
	Title         string        `json:"title"`
	DescriptionMD string        `json:"description_md"`
	Priority      task.Priority `json:"priority"`
	DueAt         *string       `json:"due_at"` // RFC3339 or "" = clear
	ParentTaskID  string        `json:"parent_task_id"`
	AgentNotes    string        `json:"agent_notes"`
}

// agentPatchTaskHandler applies a PATCH to /api/v1/agent/tasks/{id}.
//
// Permission routing is the service's job, not ours: we decode the
// patch into EditProposalPatch and let the service pick the gate.
//
//   - Holder-only update (agent_notes, no other fields) →
//     Service.UpdateAgentNotes.
//   - Mixed or full-field update → Service.EditProposal.
//
// The first case short-circuits because a holder writing
// agent_notes has nothing to do with the proposal-gate (a triaged
// task is never the agent's proposal anymore).
func agentPatchTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		var in agentTaskPatchInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
			return
		}
		taskID := chi.URLParam(r, "id")

		// Holder-only short-circuit: a PATCH carrying only
		// agent_notes routes to UpdateAgentNotes (which checks the
		// task_locks gate). A holder-only write on a triaged task
		// is the one legitimate escape from the proposal-gate.
		holderOnly := in.AgentNotes != "" &&
			in.Title == "" && in.DescriptionMD == "" &&
			in.Priority == "" && in.DueAt == nil && in.ParentTaskID == ""
		if holderOnly {
			tr, err := deps.TaskService.UpdateAgentNotes(r.Context(), taskID, id.AgentID, in.AgentNotes)
			if err != nil {
				translateManageError(w, err, "patch_task")
				return
			}
			writeJSON(w, http.StatusOK, tr)
			return
		}

		patch, perr := buildEditProposalPatch(in)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": perr.Error()})
			return
		}
		diff, err := deps.TaskService.EditProposal(r.Context(), taskID, id.AgentID, patch)
		if err != nil {
			translateManageError(w, err, "patch_task")
			return
		}
		// Audit + WS are written inside the service. Return the
		// after-state so the caller can confirm the diff landed.
		writeJSON(w, http.StatusOK, diff.After)
	}
}

// agentDeleteTaskHandler removes the agent's un-triaged proposal.
//
// Same gate as PATCH; hard delete is the convention (soft delete is
// projects-only per AGENTS.md). The service writes the task.deleted
// activity row + WS event before returning.
func agentDeleteTaskHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok || id.AgentID == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if deps.TaskService == nil {
			http.Error(w, "task service not wired", http.StatusServiceUnavailable)
			return
		}
		taskID := chi.URLParam(r, "id")
		if err := deps.TaskService.RetractProposal(r.Context(), taskID, id.AgentID); err != nil {
			translateManageError(w, err, "delete_task")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// buildEditProposalPatch converts the wire shape into the service's
// transport-agnostic struct. Returns an error for invalid priority
// or malformed due_at so the handler can 400 without dragging the
// service into wire-format concerns.
func buildEditProposalPatch(in agentTaskPatchInput) (taskservice.EditProposalPatch, error) {
	var out taskservice.EditProposalPatch
	if in.Title != "" {
		v := in.Title
		out.Title = &v
	}
	if in.DescriptionMD != "" {
		v := in.DescriptionMD
		out.Description = &v
	}
	if in.Priority != "" {
		p := task.Priority(in.Priority)
		switch p {
		case task.PriorityLow, task.PriorityMedium, task.PriorityHigh, task.PriorityUrgent:
			out.Priority = &p
		default:
			return out, fmt.Errorf("invalid_priority")
		}
	}
	if in.DueAt != nil {
		t, err := parseAgentDueAt(*in.DueAt)
		if err != nil {
			return out, fmt.Errorf("invalid_due_at")
		}
		out.DueAt = &t
	}
	if in.ParentTaskID != "" {
		v := in.ParentTaskID
		out.ParentTaskID = &v
	}
	if in.AgentNotes != "" {
		// A mixed PATCH that includes agent_notes alongside other
		// fields is rejected. The contract is "notes-only is the
		// holder path; otherwise the proposal-gate must apply".
		// We catch this here so the caller sees a clear 400
		// instead of a service-level ErrNotOwnProposal (which
		// would look like a permission bug to a legitimate
		// holder).
		return out, fmt.Errorf("agent_notes_requires_holder_only")
	}
	return out, nil
}

// parseAgentDueAt accepts either an RFC3339 timestamp (clear value)
// or an empty string (no change). An explicit empty due_at is
// signalled by passing the JSON null or "" in the request; the
// pointer-to-time on the service side captures that distinction.
func parseAgentDueAt(s string) (time.Time, error) {
	if s == "" {
		// &time.Time{} → the service treats zero as "clear".
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

// translateManageError maps service sentinel errors to HTTP status
// codes. Centralised so PATCH and DELETE stay consistent and any new
// error case lands in exactly one place.
//
//   - ErrNotOwnProposal     → 403 not_your_proposal
//   - ErrNotLockHolder      → 403 not_lock_holder
//   - ErrNoPatchFields      → 400 no_patch_fields
//   - taskservice.ErrNotFound → 404 not_found
//   - anything else         → 500 + log
func translateManageError(w http.ResponseWriter, err error, op string) {
	switch {
	case errors.Is(err, taskservice.ErrNotOwnProposal):
		writeJSON(w, http.StatusForbidden,
			map[string]string{"error": "not_your_proposal"})
	case errors.Is(err, taskservice.ErrNotLockHolder):
		writeJSON(w, http.StatusForbidden,
			map[string]string{"error": "not_lock_holder"})
	case errors.Is(err, taskservice.ErrNoPatchFields):
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "no_patch_fields"})
	case errors.Is(err, taskservice.ErrNotFound),
		errors.Is(err, task.ErrNotFound):
		writeJSON(w, http.StatusNotFound,
			map[string]string{"error": "not_found"})
	default:
		// Defensive log + 500 — unrecognised errors must not
		// silently fall through to a successful response.
		writeError(w, err)
		if err != nil {
			zap.L().Warn("agent manage handler: unhandled error",
				zap.String("op", op), zap.Error(err))
		}
	}
}
