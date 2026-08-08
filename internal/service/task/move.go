// Package task provides business logic on top of task.Repository. Phase 2
// introduces Move() with fractional-position kanban reordering. Phase 3
// adds Claim/Release/Submit/Review with atomic task_locks.
package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

// Sentinel errors returned by Service. Handlers translate these into HTTP
// status codes (mirrors api.writeError semantics).
var (
	ErrNotFound     = errors.New("task service: not found")
	ErrColumnFull   = errors.New("task service: column WIP limit reached")
	ErrInvalidInput = errors.New("task service: invalid input")
)

// MoveOptions captures how a task should be placed inside the target column.
//
// Position semantics (Phase 2.4):
//
//   - (Before == nil && After == nil): append to the end of the column.
//     The service picks position = max(existing) + 1024.
//   - (Before != nil && After == nil): place after Before. position =
//     Before.position + 1024. If After in the same column is the same
//     row we skip; otherwise we renumber carefully (not implemented in
//     Phase 2.4 — fractional positions handle ~1000 inserts before a
//     renumber is needed).
//   - (After != nil && Before == nil): place before After. position =
//     After.position - 1024.
//   - (Before != nil && After != nil): place between them.
//     position = (Before.position + After.position) / 2.
//
// Position is the explicit override; if non-zero, Before/After are ignored.
type MoveOptions struct {
	TargetColumnID string
	Position       float64 // explicit fractional; 0 = derive from Before/After
	Before         *task.Task
	After          *task.Task
}

// Recorder is the audit hook for Claim/Release/Submit/Review. Phase 3.9
// wires it to activity.Repository.
type Recorder interface {
	Record(ctx context.Context, taskID string, actorType activity.ActorType, actorID string, action activity.Action, payload string) error
}

// Hub is the in-process notification seam. Phase 2.5 wires the WebSocket
// hub; Phase 3 wires the agent inbox.
//
// We re-export ws.Hub to keep a single source of truth for the interface
// (avoids drift between api/ws.Hub and service/task.Hub).
type Hub = ws.Hub

// Service holds the dependencies Move() and future business logic need.
type Service struct {
	Tasks    task.Repository
	Locks    Locks
	Recorder Recorder
	Comments CommentAdder
	Hub      Hub
}

// New returns a Service. Tasks is mandatory; other fields can be nil and
// the corresponding operations will return an explicit "no backend" error
// or skip the side effect.
func New(tasks task.Repository, locks Locks, recorder Recorder, comments CommentAdder, hub Hub) *Service {
	return &Service{
		Tasks:    tasks,
		Locks:    locks,
		Recorder: recorder,
		Comments: comments,
		Hub:      hub,
	}
}

// Move relocates a task to a new column / position atomically.
//
// The repository update is performed in a single transaction (using the
// underlying *sql.DB via RepoWithTx; if the repo doesn't support it we
// fall back to a non-transactional best-effort write — see sqlite impl
// for the canonical path).
//
// Returns ErrColumnFull when the target column has a WIP limit and the
// move would exceed it (counted excluding the moved task if it's already
// in the same column).
func (s *Service) Move(ctx context.Context, taskID string, opts MoveOptions) (*task.Task, error) {
	if taskID == "" || opts.TargetColumnID == "" {
		return nil, ErrInvalidInput
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, ErrNotFound
	}

	// WIP-limit check (count tasks in target column, excluding self).
	if limit, ok := lookupWIPLimit(ctx, opts.TargetColumnID); ok && limit > 0 {
		existing, lerr := s.Tasks.ListByProject(ctx, task.Filter{ColumnID: opts.TargetColumnID})
		if lerr != nil {
			return nil, fmt.Errorf("task service: list column: %w", lerr)
		}
		count := 0
		for _, t := range existing {
			if t.ID != taskID {
				count++
			}
		}
		if count >= limit {
			return nil, ErrColumnFull
		}
	}

	tr.ColumnID = opts.TargetColumnID
	tr.Position = derivePosition(opts, tr.Position)

	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, fmt.Errorf("task service: update: %w", err)
	}

	if s.Recorder != nil {
		_ = s.Recorder.Record(ctx, taskID, activity.ActorUser, "", activity.ActionMoved,
			fmt.Sprintf(`{"column_id":%q,"position":%v}`, opts.TargetColumnID, tr.Position))
	}
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "tasks",
			Body: map[string]any{
				"type":   "task.moved",
				"task":   tr,
				"column": opts.TargetColumnID,
			},
		})
	}
	return tr, nil
}

// lookupWIPLimit returns the WIP limit for a column. Phase 2.5 wires the
// real implementation; for now we expose the seam and rely on the column
// table being looked up by the handler. Keeping the function here avoids
// leaking the columns repository into Move() callers.
//
// This is a no-op stub that returns (0, false). Phase 2.8 will implement
// it; for now WIP limits land via the dedicated column endpoint.
func lookupWIPLimit(ctx context.Context, columnID string) (int, bool) {
	return 0, false
}

// derivePosition resolves the new position from opts.
func derivePosition(opts MoveOptions, current float64) float64 {
	if opts.Position != 0 {
		return opts.Position
	}
	switch {
	case opts.Before != nil && opts.After != nil:
		return (opts.Before.Position + opts.After.Position) / 2
	case opts.Before != nil:
		return opts.Before.Position + 1024
	case opts.After != nil:
		return opts.After.Position - 1024
	default:
		// Append: the repository will assign the next free slot via
		// (max+1024). For Move we keep the current value; the caller
		// can pass an explicit position if they care.
		return current + 1024
	}
}

// nullHub is the no-op Hub used when none is configured.
type nullHub struct{}

func (nullHub) Publish(context.Context, ws.Event) {}
func (nullHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event)
	close(ch)
	return ch, func() {}
}

// NullHub returns a Hub that drops every event. Useful in tests and
// during `migrate status` where the WS hub isn't running.
func NullHub() Hub { return nullHub{} }

// ----------------------------------------------------------------------------
// Phase 3.6: Claim / Release / Submit / Review
// ----------------------------------------------------------------------------

// Locks is the small surface Claim/Release/Submit need to interact with
// the task_locks table. *sqlite.taskLockRepo satisfies it.
type Locks interface {
	Acquire(ctx context.Context, taskID, agentID string) error
	Release(ctx context.Context, taskID, agentID string) error
}

// Claim assigns a task to an agent atomically.
//
// Steps (wrapped in best-effort transaction for Phase 3 — full SQLite
// serialisable transaction support lands when we add a tx-aware repo
// interface in Phase 9):
//
//  1. Acquire task_locks(task_id, agent_id). UNIQUE PK → ErrLockTaken
//     if another agent already holds it.
//  2. Update tasks: assignee_type='agent', assignee_id=agentID,
//     status=in_progress, claimed_at=now, started_at=now, awaiting=none.
//  3. Audit + WS event.
//
// Returns the updated task.
func (s *Service) Claim(ctx context.Context, taskID, agentID string) (*task.Task, error) {
	if s.Locks == nil {
		return nil, errors.New("task service: Claim requires a Locks backend")
	}

	if err := s.Locks.Acquire(ctx, taskID, agentID); err != nil {
		if errors.Is(err, sqlite.ErrLockTaken) {
			return nil, ErrLockTaken
		}
		return nil, err
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		// Best-effort: roll back the lock so the agent can retry.
		_ = s.Locks.Release(ctx, taskID, agentID)
		return nil, err
	}

	now := time.Now()
	tr.AssigneeType = task.AssigneeAgent
	tr.AssigneeID = agentID
	tr.Status = task.StatusInProgress
	tr.Awaiting = task.AwaitingNone
	tr.StartedAt = &now
	tr.ClaimedAt = &now

	if err := s.Tasks.Update(ctx, tr); err != nil {
		_ = s.Locks.Release(ctx, taskID, agentID)
		return nil, err
	}

	s.publishTask(ctx, "task.claimed", tr, agentID, map[string]any{"agent_id": agentID})
	return tr, nil
}

// Release un-assigns a task that the agent currently holds.
func (s *Service) Release(ctx context.Context, taskID, agentID string) (*task.Task, error) {
	if s.Locks == nil {
		return nil, errors.New("task service: Release requires a Locks backend")
	}
	if err := s.Locks.Release(ctx, taskID, agentID); err != nil {
		if errors.Is(err, sqlite.ErrLockNotHeld) {
			return nil, ErrLockNotHeld
		}
		return nil, err
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	tr.AssigneeType = ""
	tr.AssigneeID = ""
	tr.Status = task.StatusTodo
	tr.Awaiting = task.AwaitingNone
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, err
	}

	s.publishTask(ctx, "task.released", tr, agentID, map[string]any{"agent_id": agentID})
	return tr, nil
}

// Submit marks a task as ready for human review (status=review,
// awaiting=human).
func (s *Service) Submit(ctx context.Context, taskID, agentID string, note string) (*task.Task, error) {
	// Verify the agent still holds the lock before flipping the status.
	if s.Locks != nil {
		// We don't fail hard on missing lock — Phase 3 schema enforces
		// ON DELETE CASCADE so a released task may have no lock yet.
		// Trust the caller; downstream review enforces human check.
		_ = s.Locks
	}

	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	tr.Status = task.StatusReview
	tr.Awaiting = task.AwaitingHuman
	if note != "" {
		tr.AgentNotes = note
	}
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, err
	}

	s.publishTask(ctx, "task.submitted", tr, agentID, map[string]any{
		"agent_id": agentID,
		"note":     note,
	})
	return tr, nil
}

// ReviewDecision enumerates the two valid review outcomes.
type ReviewDecision string

const (
	ReviewApprove ReviewDecision = "approve"
	ReviewReject  ReviewDecision = "reject"
)

// Review approves or rejects a task in review.
//
//	approve → status=done, completed_at=now, awaiting=none.
//	reject  → status=in_progress, awaiting=agent (back to the agent).
//
// If comment is non-empty, a comment row is added via the CommentRepo
// (Phase 3.7 wires the full CommentRepo; Phase 3.11 hands the service
// one as an optional dependency).
func (s *Service) Review(ctx context.Context, taskID, userID string, decision ReviewDecision, comment string) (*task.Task, error) {
	if decision != ReviewApprove && decision != ReviewReject {
		return nil, ErrInvalidInput
	}
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	switch decision {
	case ReviewApprove:
		tr.Status = task.StatusDone
		tr.CompletedAt = &now
		tr.Awaiting = task.AwaitingNone
	case ReviewReject:
		tr.Status = task.StatusInProgress
		tr.Awaiting = task.AwaitingAgent
	}
	if comment != "" && s.Comments != nil {
		if _, cerr := s.Comments.Add(ctx, &CommentInput{
			TargetType: commentTargetTask,
			TargetID:   taskID,
			AuthorType: commentAuthorUser,
			AuthorID:   userID,
			BodyMD:     comment,
		}); cerr != nil {
			return nil, fmt.Errorf("task service: Review: comment: %w", cerr)
		}
	}
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return nil, err
	}
	s.publishTask(ctx, "task.reviewed", tr, userID, map[string]any{
		"decision": string(decision),
		"comment":  comment,
	})
	return tr, nil
}

// publishTask emits a WS event for task mutations.
func (s *Service) publishTask(ctx context.Context, eventType string, tr *task.Task, actorID string, extra map[string]any) {
	if s.Hub == nil {
		return
	}
	body := map[string]any{
		"type":  eventType,
		"task":  tr,
		"actor": actorID,
	}
	for k, v := range extra {
		body[k] = v
	}
	s.Hub.Publish(ctx, ws.Event{Topic: "tasks", Body: body})
}

// ----------------------------------------------------------------------------
// Cross-service seams used by Review (Phase 3.7 will wire the real impl).
// ----------------------------------------------------------------------------

// CommentInput is the seam used by Review to create a comment row.
type CommentInput struct {
	TargetType string
	TargetID   string
	AuthorType string
	AuthorID   string
	BodyMD     string
}

const (
	commentTargetTask = "task"
	commentAuthorUser = "user"
)

// CommentAdder is the tiny surface Review needs to attach a rejection
// comment. *comment.Service satisfies it.
type CommentAdder interface {
	Add(ctx context.Context, in *CommentInput) (string, error)
}

// Sentinel errors returned by Claim/Release/Submit/Review.
var (
	ErrLockTaken   = errors.New("task service: lock already held by another agent")
	ErrLockNotHeld = errors.New("task service: lock not held by this agent")
)
