package study

import "context"

// Repository persists study proposals.
//
// The proposal table is small (dozens of rows per agent session,
// bounded by the user's accept/dismiss cadence) so a hand-rolled
// repo is fine; no need for a generic query-builder. Each method
// maps to one statement; MarkAccepted and MarkDismissed are paired
// lifecycle transitions.
type Repository interface {
	// Create inserts a new pending proposal. c.ID is filled if empty.
	// The lifecycle starts at pending; status and resolved_at are
	// set later by MarkAccepted / MarkDismissed.
	Create(ctx context.Context, p *Proposal) error
	// ListPending returns every pending proposal ordered by
	// created_at ASC (oldest first — the oldest nudge is the one
	// the user has had longest to think about).
	ListPending(ctx context.Context) ([]*Proposal, error)
	// Get fetches a proposal by id, including resolved ones. ErrNotFound
	// when no row matches.
	Get(ctx context.Context, id string) (*Proposal, error)
	// MarkAccepted flips status to 'accepted' and stamps resolved_at
	// + accepted_task_id atomically. Idempotency: calling with the
	// already-accepted proposal is a no-op (returns nil) — the
	// service layer keeps the proposal's existing accepted_task_id
	// in that path.
	MarkAccepted(ctx context.Context, id, taskID string) error
	// MarkDismissed flips status to 'dismissed' and stamps
	// resolved_at atomically. Idempotent on already-dismissed.
	MarkDismissed(ctx context.Context, id string) error
	// FindPendingEquivalent returns the pending proposal (if any)
	// matching the dedup key (agent_id, course_id, normalized_title).
	// Phase 32.9: called by Service.Propose before Create so the
	// planner's evening+morning runs don't multiply rows.
	// Returns study.ErrNotFound when no equivalent pending proposal exists.
	// course_id="" matches the NULL/empty-course proposals (so two
	// course-less proposals with the same normalized title collide).
	FindPendingEquivalent(ctx context.Context, agentID, courseID, normalizedTitle string) (*Proposal, error)
}
