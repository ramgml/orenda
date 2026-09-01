// Package task — Task 115: blocker edges with automatic `blocked`
// status transitions.
//
// This file is the single core for every write path that adds or
// removes a blocker edge:
//
//   - POST /tasks/{id}/blocks        → AddBlocker (single edge)
//   - DELETE /tasks/{id}/blocks/{b}  → RemoveBlocker (single edge)
//   - PUT /tasks/{id}/dependencies   → SetTaskDependencies (replace set)
//   - agent PATCH blocked_by         → replace via SetTaskDependencies
//
// All four funnel through blockerEdgeAdded / blockerEdgeRemoved so the
// auto-status bookkeeping and the activity rows behave identically no
// matter which surface the write came from.
//
// The state machine (fixed by PM, T115):
//
//	Auto-block on ADD  — if the dependent task's status ∉ {done,
//	  blocked}, save status into blocked_prev_status and flip status to
//	  `blocked`. An already-blocked task keeps its existing prev value.
//	  Done tasks are never auto-blocked.
//	Auto-unblock on REMOVE/completion — when an edge goes away (removed
//	  here, or the blocker task closed — see onCloseUnblockDependents),
//	  a dependent leaves `blocked` only when NO unfinished blocker
//	  remains: status = blocked_prev_status (fallback `todo` for NULL/
//	  legacy rows) and the column is cleared. Other open blockers →
//	  stays blocked.
//	Manual move wins — an explicit user move (Service.Move) or status
//	  change out of `blocked` clears blocked_prev_status; that happens
//	  at the Move/applyTaskPatch call sites, not here.
package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/task"
	"go.uber.org/zap"
)

// AddBlocker adds ONE blocker edge (taskID depends on blockerID) and
// applies the auto-block state machine to taskID.
//
// Idempotent: adding an edge that already exists is a no-op success
// (the domain repo reports ErrDependencyExists and we swallow it —
// the resulting state is identical). The cycle/self checks are the
// same ones PUT /dependencies uses, so error semantics match
// (ErrSelfDependency / ErrDependencyCycle / ErrNotFound).
func (s *Service) AddBlocker(ctx context.Context, taskID, blockerID string) (*task.Task, error) {
	if taskID == "" || blockerID == "" {
		return nil, ErrInvalidInput
	}
	if taskID == blockerID {
		return nil, ErrSelfDependency
	}
	// Both rows must exist; a clean 404 beats a raw FK error.
	if _, err := s.Tasks.GetByID(ctx, taskID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if _, err := s.Tasks.GetByID(ctx, blockerID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// Cycle check on the proposed single edge (DFS over graph + edge).
	if err := s.checkDependencyCycles(ctx, taskID, []string{blockerID}); err != nil {
		return nil, err
	}

	if err := s.Tasks.AddDependency(ctx, taskID, blockerID); err != nil {
		if errors.Is(err, task.ErrDependencyExists) {
			// Idempotent no-op — but still return the task so the
			// handler can render the current blockers list + status.
			return s.reloadForBlocks(ctx, taskID)
		}
		if errors.Is(err, task.ErrSelfDependency) {
			return nil, ErrSelfDependency
		}
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("task service: AddBlocker: %w", err)
	}

	tr, err := s.blockerEdgeAdded(ctx, taskID, blockerID)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

// RemoveBlocker removes ONE blocker edge (taskID no longer depends on
// blockerID) and applies the auto-unblock state machine to taskID.
//
// Removing a non-existent edge is 404 (the handler contract: unknown
// edge → 404). Returns the updated task.
func (s *Service) RemoveBlocker(ctx context.Context, taskID, blockerID string) (*task.Task, error) {
	if taskID == "" || blockerID == "" {
		return nil, ErrInvalidInput
	}
	// The edge must exist — DELETE on an unknown edge is 404.
	blockers, err := s.Tasks.Blockers(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task service: RemoveBlocker: blockers: %w", err)
	}
	edgeExists := false
	for _, b := range blockers {
		if b.BlockerID == blockerID {
			edgeExists = true
			break
		}
	}
	if !edgeExists {
		return nil, ErrNotFound
	}

	if err := s.Tasks.RemoveDependency(ctx, taskID, blockerID); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("task service: RemoveBlocker: %w", err)
	}

	tr, err := s.blockerEdgeRemoved(ctx, taskID, blockerID)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

// blockerEdgeAdded is the shared post-insert core: auto-block flip +
// task.blocked activity + WS events. Called after the edge row exists.
//
// actor describes who drove the write (user hand / agent proposal /
// agent blocked_by update); it lands on the activity row only.
func (s *Service) blockerEdgeAdded(ctx context.Context, taskID, blockerID string) (*task.Task, error) {
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task service: blockerEdgeAdded: %w", err)
	}

	changed := false
	if tr.Status != task.StatusDone && tr.Status != task.StatusBlocked {
		// Auto-block: remember where the task was, flip to blocked.
		tr.BlockedPrevStatus = tr.Status
		tr.Status = task.StatusBlocked
		changed = true
	}
	// An already-blocked task keeps its existing prev value (first
	// blocker wins); done tasks are never auto-blocked.

	if changed {
		s.syncColumnToStatus(ctx, tr)
		if err := s.Tasks.Update(ctx, tr); err != nil {
			return nil, fmt.Errorf("task service: blockerEdgeAdded: update: %w", err)
		}
		s.mirrorSave(ctx, tr)
	}
	if s.Recorder != nil {
		// ActorID must be non-empty (Activity.Validate) — the
		// auto-block is the system state machine acting, so the
		// actor id is the literal "system" and the payload names
		// the blocker edge that triggered the flip.
		_ = s.Recorder.Record(ctx, taskID, activity.ActorSystem, "system",
			activity.ActionBlocked, fmt.Sprintf(`{"blocked_by":%q}`, blockerID))
	}
	s.publishTask(ctx, "task.blocked", tr, "", map[string]any{
		"blocked_by": blockerID,
		"task_id":    taskID,
	})
	return tr, nil
}

// blockerEdgeRemoved is the shared post-delete core: auto-unblock
// check + task.unblocked activity + WS events.
func (s *Service) blockerEdgeRemoved(ctx context.Context, taskID, blockerID string) (*task.Task, error) {
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("task service: blockerEdgeRemoved: %w", err)
	}

	restored, err := s.restoreIfLastBlocker(ctx, tr, blockerID)
	if err != nil {
		return nil, err
	}
	_ = restored
	s.publishTask(ctx, "task.unblocked", tr, "", map[string]any{
		"blocked_by": blockerID,
		"task_id":    taskID,
	})
	return tr, nil
}

// restoreIfLastBlocker implements the auto-unblock check for ONE
// dependent task after an edge loss: if no unfinished blocker remains
// and the task is currently `blocked`, restore status from
// BlockedPrevStatus (fallback `todo`) and clear the column. Stays
// blocked when other unfinished blockers remain. Persists + mirrors +
// records task.unblocked when the restore happens.
//
// Returns true when the task was restored out of `blocked`.
func (s *Service) restoreIfLastBlocker(ctx context.Context, tr *task.Task, blockerID string) (bool, error) {
	if tr.Status != task.StatusBlocked {
		// Not auto-blocked (done via manual move, never blocked, …) —
		// nothing to restore. The edge change is still real.
		return false, nil
	}
	// Any unfinished blocker left?
	blockers, err := s.Tasks.Blockers(ctx, tr.ID)
	if err != nil {
		return false, fmt.Errorf("task service: restoreIfLastBlocker: %w", err)
	}
	for _, b := range blockers {
		if !b.Done {
			return false, nil // still blocked by someone else
		}
	}
	// Last blocker gone: restore prev status (fallback todo), clear.
	prev := tr.BlockedPrevStatus
	if prev == "" || prev == task.StatusBlocked {
		prev = task.StatusTodo
	}
	tr.Status = prev
	tr.BlockedPrevStatus = ""
	s.syncColumnToStatus(ctx, tr)
	if err := s.Tasks.Update(ctx, tr); err != nil {
		return false, fmt.Errorf("task service: restoreIfLastBlocker: update: %w", err)
	}
	s.mirrorSave(ctx, tr)
	if s.Recorder != nil {
		_ = s.Recorder.Record(ctx, tr.ID, activity.ActorSystem, "system",
			activity.ActionUnblocked, fmt.Sprintf(`{"blocked_by":%q}`, blockerID))
	}
	return true, nil
}

// OnCloseUnblockDependents runs when a task transitions to `done`
// (Review approve, PATCH status=done): every dependent that just lost
// its last unfinished blocker leaves `blocked`. Exported because the
// API layer calls it after the closing write path has persisted the
// done status; the service-internal callers use the same method.
//
// blockerID is the closing task's id; it lands in the task.unblocked
// payload so the audit trail shows WHICH closure freed the dependent.
// Errors are logged, never propagated — an unblock failure must not
// fail the close itself.
func (s *Service) OnCloseUnblockDependents(ctx context.Context, closedTaskID string) {
	dependents, err := s.Tasks.Dependents(ctx, closedTaskID)
	if err != nil {
		if s.Logger != nil {
			s.Logger.Warn("onCloseUnblockDependents: dependents lookup failed",
				zap.String("task_id", closedTaskID), zap.Error(err))
		}
		return
	}
	for _, depID := range dependents {
		tr, err := s.Tasks.GetByID(ctx, depID)
		if err != nil {
			continue // dependent vanished mid-run; skip
		}
		if _, err := s.restoreIfLastBlocker(ctx, tr, closedTaskID); err != nil {
			if s.Logger != nil {
				s.Logger.Warn("onCloseUnblockDependents: restore failed",
					zap.String("task_id", depID), zap.Error(err))
			}
			continue
		}
		s.publishTask(ctx, "task.unblocked", tr, "", map[string]any{
			"blocked_by": closedTaskID,
			"task_id":    depID,
		})
	}
}

// reloadForBlocks re-reads the task and fires the deps-changed WS
// event; shared tail of AddBlocker/RemoveBlocker so handlers get a
// fresh task for the response body.
func (s *Service) reloadForBlocks(ctx context.Context, taskID string) (*task.Task, error) {
	tr, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{Topic: "tasks", Body: map[string]any{
			"type":    "task.deps_changed",
			"task_id": taskID,
		}})
	}
	return tr, nil
}
