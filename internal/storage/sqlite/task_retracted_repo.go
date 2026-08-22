// Phase 33.2.1: tombstone repo for hard-deleted tasks.
//
// RetractProposal writes here instead of task_activity because
// task_activity.task_id has REFERENCES tasks(id) ON DELETE CASCADE —
// a row inserted after the task is gone either fails the FK (when
// ON DELETE CASCADE is paired with the default RESTRICT in some
// builds) or vanishes with the parent. The task_retracted table has
// NO FK on task_id, so the audit row survives.
//
// The repo is intentionally narrow: one write method
// (RecordRetracted) and a read for the owner-side audit tools that
// Phase 33.4+ will add. There's no List on production yet — the
// tombstone is a write-only lane until the audit-pull surface lands.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/activity"
)

// taskRetractedRepo persists retract tombstones.
type taskRetractedRepo struct {
	db *sql.DB
}

// NewTaskRetractedRepository returns a tombstone repo.
func NewTaskRetractedRepository(db *sql.DB) *taskRetractedRepo {
	return &taskRetractedRepo{db: db}
}

// RecordRetracted writes one row. snapshotJSON is the pre-delete task
// shape (id/title/project_id JSON, easily extended). actor_type is
// string-typed here because the service side doesn't pull in
// internal/domain/activity — kept as a plain string for surface
// simplicity.
func (r *taskRetractedRepo) RecordRetracted(ctx context.Context, taskID, snapshotJSON string, actorType activity.ActorType, actorID string) error {
	if taskID == "" || actorID == "" {
		return fmt.Errorf("task_retracted.Record: task_id and actor_id are required")
	}
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO task_retracted (id, task_id, snapshot_json, actor_type, actor_id)
		VALUES (?, ?, ?, ?, ?)
	`, newUUID(), taskID, snapshotJSON, string(actorType), actorID); err != nil {
		return fmt.Errorf("task_retracted.Record: %w", err)
	}
	return nil
}

// CountForTask is a cheap existence check used by tests. Production
// callers should not need this — the tombstone is append-only and
// the operator surfaces tombstones through a UI scan.
func (r *taskRetractedRepo) CountForTask(ctx context.Context, taskID string) (int, error) {
	if taskID == "" {
		return 0, nil
	}
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_retracted WHERE task_id = ?`, taskID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("task_retracted.Count: %w", err)
	}
	return n, nil
}

// GetForTask returns all tombstones for a given task_id. Newest
// first. Used by the test harness; the audit UI lands in a later
// phase.
func (r *taskRetractedRepo) GetForTask(ctx context.Context, taskID string) ([]RetractedRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, snapshot_json, actor_type, actor_id, retracted_at
		FROM task_retracted
		WHERE task_id = ?
		ORDER BY retracted_at DESC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("task_retracted.GetForTask: %w", err)
	}
	defer rows.Close()
	out := make([]RetractedRow, 0)
	for rows.Next() {
		var r RetractedRow
		if err := rows.Scan(&r.ID, &r.SnapshotJSON, &r.ActorType, &r.ActorID, &r.RetractedAt); err != nil {
			return nil, fmt.Errorf("task_retracted.GetForTask: scan: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RetractedRow is the read shape for the tombstone.
type RetractedRow struct {
	ID           string
	SnapshotJSON string
	ActorType    string
	ActorID      string
	RetractedAt  string
}

// _ keeps the import "database/sql" used (sql.ErrNoRows via CountForTask).
var _ = sql.ErrNoRows
