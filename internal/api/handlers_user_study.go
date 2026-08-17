// Package api — Phase 31.6 user-side study handlers.
//
// The Dashboard tray reads pending proposals and lets the user
// accept or dismiss each one. Accept materialises a real inbox
// task (see Phase 31.4); dismiss just marks the proposal resolved.
//
//	GET   /api/v1/study-proposals                list pending proposals
//	POST  /api/v1/study-proposals/{id}/accept    accept (idempotent)
//	POST  /api/v1/study-proposals/{id}/dismiss   dismiss
//
// Status codes:
//   - 200 ok
//   - 201 created (accept path: fresh materialised task)
//   - 400 invalid_input
//   - 404 not_found (proposal missing or not wired)
//   - 409 proposal_resolved (accept/dismiss on a resolved row)
//   - 503 service not wired (test/partial-router fixtures)
package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	studydomain "github.com/ramgml/orenda/internal/domain/study"
)

// listStudyProposalsHandler — GET /api/v1/study-proposals.
//
// Returns the pending proposals ordered by created_at ASC (oldest
// first — the planner has had the longest to think about those).
// Resolved proposals are kept in the table for audit but never
// surface here.
func listStudyProposalsHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.StudyService == nil {
			http.Error(w, "study service not wired", http.StatusServiceUnavailable)
			return
		}
		items, err := deps.StudyService.ListPending(r.Context())
		if err != nil {
			mapStudyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"proposals": items})
	}
}

// acceptStudyProposalHandler — POST /api/v1/study-proposals/{id}/accept.
//
// 201 on a fresh materialisation (the proposal was pending and a
// new inbox task was created).
// 200 on an idempotent re-accept (the proposal was already accepted;
// the response carries the original task id, not a duplicate).
// 409 proposal_resolved on accept of a dismissed proposal.
// 404 not_found on unknown id.
func acceptStudyProposalHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.StudyService == nil {
			http.Error(w, "study service not wired", http.StatusServiceUnavailable)
			return
		}
		res, err := deps.StudyService.Accept(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			mapStudyUserError(w, err)
			return
		}
		status := http.StatusOK
		if !res.AlreadyAccepted {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{
			"proposal":         res.Proposal,
			"task":             res.Task,
			"already_accepted": res.AlreadyAccepted,
		})
	}
}

// dismissStudyProposalHandler — POST /api/v1/study-proposals/{id}/dismiss.
//
// 200 ok on a fresh dismiss (proposal was pending).
// 200 with the existing proposal on a re-dismiss of an already-dismissed
// row — the service treats it as a no-op (the proposal is already
// in its terminal state).
// 409 proposal_resolved on accept of an already-accepted proposal.
// 404 not_found on unknown id.
func dismissStudyProposalHandler(deps *Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.StudyService == nil {
			http.Error(w, "study service not wired", http.StatusServiceUnavailable)
			return
		}
		p, err := deps.StudyService.Dismiss(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			mapStudyUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"proposal": p})
	}
}

// mapStudyUserError is the user-side sibling of mapStudyError.
// It widens the mapping with the 404 / 409 codes the user-facing
// tray expects, while leaving the agent-side narrower (the agent
// only deals with its own proposals — wrong id is the only 404
// case that matters there).
func mapStudyUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, studydomain.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_input"})
	case errors.Is(err, studydomain.ErrNotFound):
		http.Error(w, "proposal not found", http.StatusNotFound)
	case errors.Is(err, studydomain.ErrTransition):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "proposal_resolved"})
	default:
		writeError(w, err)
	}
}
