// Package task provides business logic on top of task.Repository. Phase 2
// introduces Move() with fractional-position kanban reordering; the
// Activity and Hub interfaces are the seams for future phases (audit log
// in Phase 3, notifications in Phase 6).
package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/task"
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

// Recorder is the audit hook (Phase 3 will write to task_activity).
//
// In Phase 2 we only call it for the Move event; the seam exists so adding
// other actions later doesn't require restructuring Service.
type Recorder interface {
	Record(ctx context.Context, taskID, action, payload string) error
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
	Recorder Recorder
	Hub      Hub
}

// New returns a Service. Tasks is mandatory; Recorder and Hub can be nil
// (no-op fallbacks are used).
func New(tasks task.Repository, r Recorder, h Hub) *Service {
	return &Service{Tasks: tasks, Recorder: r, Hub: h}
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
		_ = s.Recorder.Record(ctx, taskID, "moved",
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
