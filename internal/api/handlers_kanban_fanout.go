// Package api — Phase 30.14 fan-out helper.
//
// When a project's column gets a new machine-key status, every task
// already living in that column must adopt it. Without the fan-out, the
// single-axis invariant (Phase 27.8: `task.status ≡ column.status`) is
// silently broken until the user manually re-drags every card.
//
// The fan-out runs through the same per-task side-effects that single
// PATCH would emit (DB update, status-changed activity row, task.updated
// WS event). The handler calls it after the column UPDATE so a partial
// failure leaves the column already carrying the new machine key — the
// recovered run will retry the per-task writes.
package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/activity"
	"github.com/ramgml/orenda/internal/domain/project"
	taskdomain "github.com/ramgml/orenda/internal/domain/task"
)

// fanOutColumnStatus rewrites task.status on every task currently in
// `col`, emitting the same activity row / WS event a manual single PATCH
// would. Returns the first error encountered (other tasks may still be
// updated) — handlers log it but don't fail the column PATCH, because
// the column itself already carries the new status. The next move or
// PATCH on the affected task picks up the canonical key again.
//
// Called only when a column's status was changed via patchColumnHandler.
// `col.Status` must be non-empty by the time we get here (the handler
// validates the empty-status-not-empty path separately, returning 422).
func fanOutColumnStatus(ctx context.Context, deps *Dependencies, col *project.Column) error {
	if col == nil || col.Status == "" || deps.Tasks == nil {
		return nil
	}
	tasks, err := deps.Tasks.ListByProject(ctx, taskdomain.Filter{ColumnID: col.ID})
	if err != nil {
		return err
	}
	var firstErr error
	for _, tr := range tasks {
		if tr == nil {
			continue
		}
		prev := tr.Status
		if string(prev) == col.Status {
			continue // already on the new key
		}
		tr.Status = taskdomain.Status(col.Status)
		if uerr := deps.Tasks.Update(ctx, tr); uerr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("task %s: %w", tr.ID, uerr)
			}
			continue
		}
		emitTaskStatusChangedActivity(ctx, deps, tr.ID, string(prev), col.Status)
		emitTaskUpdatedWS(ctx, deps, tr)
	}
	return firstErr
}

// emitTaskStatusChangedActivity writes a `task.status_changed` audit row
// mirroring single-PATCH behaviour (handlers_tasks.go).
func emitTaskStatusChangedActivity(ctx context.Context, deps *Dependencies, taskID, from, to string) {
	if deps.TaskService == nil {
		return
	}
	payload, err := json.Marshal(map[string]string{"from": from, "to": to})
	if err != nil {
		return
	}
	deps.TaskService.RecordActivity(ctx, taskID, "", activity.ActionStatusChanged, string(payload))
}

// emitTaskUpdatedWS broadcasts a `task.updated` event on the
// `tasks` topic. Mirrors publishTask in service/task/move.go (here so
// the handler-side helper doesn't have to depend on the service's
// internal publishTask, which would create an import cycle).
func emitTaskUpdatedWS(ctx context.Context, deps *Dependencies, tr *taskdomain.Task) {
	if deps.WSHub == nil || tr == nil {
		return
	}
	deps.WSHub.Publish(ctx, ws.Event{
		Topic: "tasks",
		Body: map[string]any{
			"type": "task.updated",
			"task": tr,
		},
	})
}
