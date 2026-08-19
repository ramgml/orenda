// Project activity repository — wiki:agent-project-description.
//
// Persists rows in project_activity (migration 024). Write side is
// Create; read side is ListByProject newest-first. The read API is
// intentionally narrow: a future project-activity feed endpoint
// (Phase > 32.13) will reuse ListByProject with a bounded limit so
// the test surface stays small and the data shape is decided by the
// query, not the writer.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/project"
)

// ErrProjectActivityNotFound is returned when a single-row lookup
// misses. Currently unused by the public surface (we list, we don't
// fetch by id) but exposed so tests can assert persistence shape.
var ErrProjectActivityNotFound = errors.New("sqlite: project activity not found")

// ProjectActivityRepository is the storage interface for project
// activity rows. Constructed once in cmd/orenda/main.go and passed
// to the API layer as an optional dependency.
type ProjectActivityRepository struct {
	db *sql.DB
}

// NewProjectActivityRepository wires a repo backed by db.
func NewProjectActivityRepository(db *sql.DB) *ProjectActivityRepository {
	return &ProjectActivityRepository{db: db}
}

// Create inserts a project_activity row. Validates first so we don't
// accept empty IDs or unknown enum values from upstream callers.
func (r *ProjectActivityRepository) Create(ctx context.Context, a *project.Activity) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("project activity: create: %w", err)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO project_activity (id, project_id, actor_type, actor_id, kind, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.ProjectID, string(a.ActorType), a.ActorID, string(a.Kind), nullProjectActivityPayload(a.Payload), a.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("project activity: insert: %w", err)
	}
	return nil
}

// ListByProject returns activity rows for the given project, newest
// first. limit=0 means "all rows" — used by tests; production
// callers always pass a limit.
func (r *ProjectActivityRepository) ListByProject(ctx context.Context, projectID string, limit int) ([]*project.Activity, error) {
	q := `SELECT id, project_id, actor_type, actor_id, kind, payload, created_at
	      FROM project_activity WHERE project_id = ?
	      ORDER BY created_at DESC, id DESC`
	args := []interface{}{projectID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("project activity: list: %w", err)
	}
	defer rows.Close()
	out := make([]*project.Activity, 0)
	for rows.Next() {
		a := &project.Activity{}
		var actorType, kind string
		var payload sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.ProjectID, &actorType, &a.ActorID, &kind, &payload, &createdAt); err != nil {
			return nil, fmt.Errorf("project activity: scan: %w", err)
		}
		a.ActorType = project.ActorType(actorType)
		a.Kind = project.ActivityKind(kind)
		if payload.Valid {
			a.Payload = payload.String
		}
		ts, perr := time.Parse(time.RFC3339, createdAt)
		if perr != nil {
			return nil, fmt.Errorf("project activity: parse created_at %q: %w", createdAt, perr)
		}
		a.CreatedAt = ts
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("project activity: rows: %w", err)
	}
	return out, nil
}

// nullProjectActivityPayload converts an empty payload string to
// nil so it reads back as null in SQLite (consistent with
// course_activity: nullable columns are null, not "").
func nullProjectActivityPayload(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
