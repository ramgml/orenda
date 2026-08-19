// Package study implements the service layer for study proposals.
//
// The flow:
//  1. An external planner agent posts a proposal (Propose).
//  2. The user sees it in the Dashboard tray and accepts or dismisses.
//  3. Accept materialises a real inbox task with study_course_id set
//
// and due_at = max(target_date, today). Dismiss just marks the
// proposal resolved.
//
// The WS layer piggy-backs on the existing "tasks" topic — TodayPage
// and the kanban already subscribe; adding a new topic would require
// a UI change. We emit three event shapes under Topic="tasks":
//
//	study.proposed   — {proposal_id, course_id?, agent_id}
//	study.accepted   — {proposal_id, course_id?, task_id}
//	study.dismissed  — {proposal_id, course_id?}
//
// All three also surface the underlying proposal row in `body.proposal`
// for clients that want to render the tray without a second fetch.
//
// Idempotency: Accept on an already-accepted proposal returns the
// existing accepted_task_id (no duplicate row). Dismiss on a resolved
// proposal is a no-op. The repository does the conditional UPDATE;
// the service re-reads to figure out which branch we're in.
package study

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/study"
	"github.com/ramgml/orenda/internal/domain/task"
)

// ActivityRecorder is the narrow seam the service uses to log the
// "task.created" audit row when an accepted proposal materialises
// as an inbox task. Mirrors the pattern in service/task; nil is
// tolerated (logs nothing — useful in tests).
type ActivityRecorder interface {
	RecordTask(ctx context.Context, taskID string, actorType activity.ActorType, actorID string, action activity.Action, payload string) error
}

// Service holds the dependencies Propose/Accept/Dismiss need.
type Service struct {
	Proposals study.Repository
	Tasks     task.Repository
	Hub       ws.Hub
	Recorder  ActivityRecorder
}

// New returns a Service. Recorder and Hub can be nil — the
// corresponding side-effect is a no-op. Tasks is required (Accept
// materialises inbox tasks).
func New(proposals study.Repository, tasks task.Repository, hub ws.Hub, recorder ActivityRecorder) *Service {
	return &Service{
		Proposals: proposals,
		Tasks:     tasks,
		Hub:       hub,
		Recorder:  recorder,
	}
}

// ProposeInput is the wire shape the agent REST endpoint accepts.
// It mirrors the proposal fields the agent is allowed to set — the
// service fills CreatedByAgent from the authenticated identity, not
// from the body.
type ProposeInput struct {
	CourseID   string `json:"course_id,omitempty"`
	Title      string `json:"title"`
	BodyMD     string `json:"body_md,omitempty"`
	TargetDate string `json:"target_date"` // YYYY-MM-DD
}

// ProposeResult is the return shape of Propose. Refreshed is true
// when the call collapsed onto an existing pending proposal (no
// new row, no new WS event); false when a new proposal was
// created. The planner agent can use the flag for logging or to
// skip its "new proposal" notification.
type ProposeResult struct {
	Proposal  *study.Proposal
	Refreshed bool
}

// Propose persists a new pending proposal. createdByAgent is the
// authenticated agent's id (the REST handler stamps it on the way
// in — the body never carries an actor field).
//
// Phase 32.9 dedup contract:
//   - Before Create, Propose asks the repo for an existing pending
//     proposal from the same agent with the same (course_id,
//     normalized_title). If one exists, Propose returns it with
//     Refreshed=true — no new row, no new WS event.
//   - Resolved proposals (accepted/dismissed) do NOT dedup — the
//     user already triaged them; a fresh Propose after dismiss
//     creates a new pending row (the agent's new suggestion is a
//     separate entity from the user's rejected old one).
//   - Different course_id or different normalized_title → new
//     row, Refreshed=false.
//
// Normalization (study.NormalizeTitle): trim + collapse
// whitespace + lowercase ASCII. See wiki:study-proposals-dedup.
func (s *Service) Propose(ctx context.Context, createdByAgent string, in ProposeInput) (*ProposeResult, error) {
	if createdByAgent == "" {
		return nil, fmt.Errorf("study.Propose: createdByAgent is required")
	}
	if in.Title == "" {
		return nil, study.ErrInvalidInput
	}
	normalized := study.NormalizeTitle(in.Title)
	existing, err := s.Proposals.FindPendingEquivalent(ctx, createdByAgent, in.CourseID, normalized)
	if err != nil {
		return nil, fmt.Errorf("study.Propose: dedup lookup: %w", err)
	}
	if existing != nil {
		return &ProposeResult{Proposal: existing, Refreshed: true}, nil
	}

	p := &study.Proposal{
		CourseID:       in.CourseID,
		Title:          in.Title,
		BodyMD:         in.BodyMD,
		TargetDate:     in.TargetDate,
		CreatedByAgent: createdByAgent,
	}
	if err := s.Proposals.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("study.Propose: %w", err)
	}
	s.publish(ctx, "study.proposed", p, map[string]any{
		"proposal_id": p.ID,
		"course_id":   p.CourseID,
		"agent_id":    p.CreatedByAgent,
	})
	return &ProposeResult{Proposal: p, Refreshed: false}, nil
}

// AcceptResult is the wire shape the user REST endpoint returns.
// Task is non-nil on success; Proposal is always present (so the UI
// can move the row out of the tray without a second fetch).
type AcceptResult struct {
	Proposal *study.Proposal
	Task     *task.Task
	// AlreadyAccepted is true when this call was an idempotent re-accept
	// of a previously-accepted proposal — the caller can use the same
	// 200-OK response, but the UI may want to skip the "created a new
	// reminder" animation.
	AlreadyAccepted bool
}

// Accept materialises an inbox task from a pending proposal. Idempotent:
// calling twice on the same proposal returns the same task id. Calling
// on a dismissed proposal returns ErrTransition (409 on the API).
func (s *Service) Accept(ctx context.Context, proposalID string) (*AcceptResult, error) {
	p, err := s.Proposals.Get(ctx, proposalID)
	if err != nil {
		return nil, fmt.Errorf("study.Accept: get: %w", err)
	}

	// Lifecycle guard: accepted proposals are idempotent (return
	// the existing task), dismissed proposals are final (409).
	switch p.Status {
	case study.StatusPending:
		// fall through to materialise
	case study.StatusAccepted:
		return s.returnExistingAccepted(ctx, p, "second accept on already-accepted")
	case study.StatusDismissed:
		return nil, study.ErrTransition
	default:
		return nil, study.ErrInvalidInput
	}

	// Compute due_at = max(target_date, today) — never earlier
	// than today, so a proposal for "2025-12-01" filed on
	// "2026-08-17" lands with due_at=2026-08-17 23:59:59 UTC.
	dueAt, err := computeDueAt(p.TargetDate, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("study.Accept: %w", err)
	}

	// Build the inbox task: no project, no column, status=todo,
	// study_course_id set. The accept's idempotency is anchored by
	// MarkAccepted: conditional UPDATE matches zero rows on a
	// concurrent second accept, the service re-reads, returns the
	// existing accepted_task_id.
	tr := &task.Task{
		Title:         p.Title,
		Description:   p.BodyMD,
		Status:        task.StatusTodo,
		Priority:      task.PriorityMedium,
		Awaiting:      task.AwaitingNone,
		StudyCourseID: p.CourseID,
		DueAt:         dueAt,
	}
	if err := s.Tasks.Create(ctx, tr); err != nil {
		return nil, fmt.Errorf("study.Accept: create task: %w", err)
	}

	if err := s.Proposals.MarkAccepted(ctx, p.ID, tr.ID); err != nil {
		// The conditional WHERE on MarkAccepted returned 0 rows —
		// someone else (or a previous Accept call) already
		// accepted this proposal. Roll back the task we just
		// created (we are the idempotent loser) and return the
		// canonical task id from the existing accepted row.
		if err := s.Tasks.Delete(ctx, tr.ID); err != nil {
			return nil, fmt.Errorf("study.Accept: rollback task: %w", err)
		}
		got, err := s.Proposals.Get(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("study.Accept: re-get: %w", err)
		}
		if got.Status != study.StatusAccepted || got.AcceptedTaskID == "" {
			return nil, study.ErrTransition
		}
		existing, err := s.Tasks.GetByID(ctx, got.AcceptedTaskID)
		if err != nil {
			return nil, fmt.Errorf("study.Accept: existing task: %w", err)
		}
		return &AcceptResult{Proposal: got, Task: existing, AlreadyAccepted: true}, nil
	}

	// Activity: the task was created from a study-proposal. We
	// stamp actor=agent so the timeline groups reminder creations
	// with other planner actions.
	if s.Recorder != nil {
		activityPayload := map[string]any{
			"source":      "study_proposal_accept",
			"proposal_id": p.ID,
			"agent_id":    p.CreatedByAgent,
			"course_id":   p.CourseID,
		}
		buf, _ := json.Marshal(activityPayload)
		_ = s.Recorder.RecordTask(ctx, tr.ID, activity.ActorAgent, p.CreatedByAgent, activity.ActionCreated, string(buf))
	}

	// Re-read so the proposal's resolved_at/accepted_task_id are
	// populated from the row (the proposal struct is by-value).
	got, err := s.Proposals.Get(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("study.Accept: re-get after mark: %w", err)
	}
	s.publish(ctx, "study.accepted", got, map[string]any{
		"proposal_id": p.ID,
		"course_id":   p.CourseID,
		"task_id":     tr.ID,
	})
	return &AcceptResult{Proposal: got, Task: tr}, nil
}

// returnExistingAccepted is the idempotent fast-path: the row is
// already accepted, just return the recorded accepted_task_id.
// MarkAccepted/Dismiss are no-ops here — no event is emitted
// either, since the UI already has the proposal in its tray.
func (s *Service) returnExistingAccepted(ctx context.Context, p *study.Proposal, _ string) (*AcceptResult, error) {
	if p.AcceptedTaskID == "" {
		// Edge — accepted but missing task id (corruption or a
		// downgrade). Refuse rather than 500-ing.
		return nil, study.ErrNotFound
	}
	existing, err := s.Tasks.GetByID(ctx, p.AcceptedTaskID)
	if err != nil {
		return nil, fmt.Errorf("study.Accept: existing task: %w", err)
	}
	return &AcceptResult{Proposal: p, Task: existing, AlreadyAccepted: true}, nil
}

// Dismiss marks a pending proposal dismissed. Idempotent on
// already-dismissed proposals (no-op). Calling on an accepted
// proposal returns ErrTransition.
func (s *Service) Dismiss(ctx context.Context, proposalID string) (*study.Proposal, error) {
	p, err := s.Proposals.Get(ctx, proposalID)
	if err != nil {
		return nil, fmt.Errorf("study.Dismiss: get: %w", err)
	}
	if !p.DismissAllowed() {
		return nil, study.ErrTransition
	}
	if err := s.Proposals.MarkDismissed(ctx, p.ID); err != nil {
		return nil, fmt.Errorf("study.Dismiss: %w", err)
	}
	got, err := s.Proposals.Get(ctx, p.ID)
	if err != nil {
		return nil, fmt.Errorf("study.Dismiss: re-get: %w", err)
	}
	s.publish(ctx, "study.dismissed", got, map[string]any{
		"proposal_id": p.ID,
		"course_id":   p.CourseID,
	})
	return got, nil
}

// ListPending returns every pending proposal — the user-side tray
// reads this to render the Dashboard reminder block. Order is by
// created_at ASC (oldest first).
func (s *Service) ListPending(ctx context.Context) ([]*study.Proposal, error) {
	return s.Proposals.ListPending(ctx)
}

// publish fans out a study.* event to the "tasks" topic so the
// Dashboard tray and the kanban both update live. nil-safe.
func (s *Service) publish(ctx context.Context, kind string, p *study.Proposal, body map[string]any) {
	if s.Hub == nil {
		return
	}
	// Embed the proposal under "proposal" so consumers that want
	// to render the row don't have to refetch.
	body["proposal"] = p
	s.Hub.Publish(ctx, ws.Event{Topic: "tasks", Body: body})
	//// Keep the event-name visible to subscribers that filter on
	//// it: a flat key avoids the type field the proposal payload
	//// already carries.
	_ = kind
}

// computeDueAt returns the max(target_date, today) as a UTC *time.Time
// at end-of-day (23:59:59). Both inputs are calendar dates — the
// planner picks a target day, and the due_at lands on that day or
// later. A proposal for tomorrow that the user accepts today gets
// tomorrow's end-of-day; a proposal for "2024-01-01" accepted in
// 2026 lands on today.
func computeDueAt(targetDate string, now time.Time) (*time.Time, error) {
	t, err := time.Parse("2006-01-02", targetDate)
	if err != nil {
		return nil, fmt.Errorf("study.computeDueAt: %w", err)
	}
	// today at end-of-day UTC. We anchor the comparison on the
	// calendar date, not the instant — accepting at 00:01 UTC
	// for a target_date of "today" must still give today.
	today := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, time.UTC)
	target := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
	if target.Before(today) {
		return &today, nil
	}
	return &target, nil
}
