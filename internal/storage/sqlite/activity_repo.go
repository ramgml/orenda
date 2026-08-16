// Package sqlite — Activity repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/activity"
)

// activityRepo persists task_activity rows. Append-only.
type activityRepo struct {
	db *sql.DB
}

// ActivityRepo is the exported alias so callers can name the type.
type ActivityRepo = activityRepo

// NewActivityRepository returns the Phase 3 activity repo.
func NewActivityRepository(db *sql.DB) activity.Repository {
	return &activityRepo{db: db}
}

func (r *activityRepo) Create(ctx context.Context, a *activity.Activity) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.ID == "" {
		a.ID = newUUID()
	}

	const q = `
		INSERT INTO task_activity (id, task_id, actor_type, actor_id, action, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q,
		a.ID, a.TaskID, string(a.ActorType), a.ActorID, string(a.Action), a.Payload,
	)
	if err != nil {
		return fmt.Errorf("activity.Create: %w", err)
	}
	return nil
}

func (r *activityRepo) ListByTask(ctx context.Context, taskID string) ([]*activity.Activity, error) {
	const q = activitySelectColumns + " WHERE task_id = ? ORDER BY created_at ASC"
	return r.query(ctx, q, taskID)
}

func (r *activityRepo) ListByActor(ctx context.Context, actorType activity.ActorType, actorID string) ([]*activity.Activity, error) {
	const q = activitySelectColumns + " WHERE actor_type = ? AND actor_id = ? ORDER BY created_at DESC"
	return r.query(ctx, q, string(actorType), actorID)
}

// ListByProject aggregates activity rows from every task that belongs
// to the given project. Newest first; the SQL LIMIT clamps runaway
// reads when a project accumulates tens of thousands of events.
//
// Implementation note: SQLite does not expose the joined columns
// directly via the shared `activitySelectColumns`, so this method
// writes its own SELECT. Keeping the column list in sync with the
// scan below is enforced by reading the same names — see
// `activitySelectColumns` if you add fields there.
func (r *activityRepo) ListByProject(ctx context.Context, projectID string, limit int) ([]*activity.ProjectActivityEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	const q = `
SELECT a.id, a.task_id, a.actor_type, a.actor_id, a.action, a.payload, a.created_at,
       t.title
FROM task_activity a
JOIN tasks t ON t.id = a.task_id
WHERE t.project_id = ?
ORDER BY a.created_at DESC
LIMIT ?
`
	rows, err := r.db.QueryContext(ctx, q, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("activity.ListByProject: %w", err)
	}
	defer rows.Close()
	out := make([]*activity.ProjectActivityEvent, 0)
	for rows.Next() {
		var (
			ev      activity.ProjectActivityEvent
			aType   string
			action  string
			payload sql.NullString
			cAt     string
		)
		if err := rows.Scan(
			&ev.ID, &ev.TaskID, &aType, &ev.ActorID, &action, &payload, &cAt,
			&ev.TaskTitle,
		); err != nil {
			return nil, fmt.Errorf("activity.ListByProject: scan: %w", err)
		}
		ev.ActorType = activity.ActorType(aType)
		ev.Action = activity.Action(action)
		if payload.Valid {
			ev.Payload = payload.String
		}
		ev.CreatedAt = parseTime(cAt)
		out = append(out, &ev)
	}
	return out, rows.Err()
}

func (r *activityRepo) query(ctx context.Context, q string, args ...any) ([]*activity.Activity, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("activity.query: %w", err)
	}
	defer rows.Close()
	out := make([]*activity.Activity, 0)
	for rows.Next() {
		var (
			a       activity.Activity
			aType   string
			action  string
			payload sql.NullString
			cAt     string
		)
		if err := rows.Scan(&a.ID, &a.TaskID, &aType, &a.ActorID, &action, &payload, &cAt); err != nil {
			return nil, fmt.Errorf("activity.query: scan: %w", err)
		}
		a.ActorType = activity.ActorType(aType)
		a.Action = activity.Action(action)
		if payload.Valid {
			a.Payload = payload.String
		}
		a.CreatedAt = parseTime(cAt)
		out = append(out, &a)
	}
	return out, rows.Err()
}

const activitySelectColumns = `
SELECT id, task_id, actor_type, actor_id, action, payload, created_at
FROM task_activity
`
