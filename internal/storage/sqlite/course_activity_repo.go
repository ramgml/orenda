// Course activity repository — Phase 32.5 pilot task #2.
//
// Persists rows in course_activity (migration 023). Read side is
// ListByCourse newest-first with optional limit; write side is Create.
// The full feed is bounded by user intent (limit param), not by
// pagination — operators see "last N events" without scrolling
// history.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/course"
)

// ErrNotFound is returned when the requested activity row doesn't
// exist (a rare case — by ID lookup is not part of the public API
// yet, but the storage layer exposes it for tests).
var ErrNotFound = errors.New("sqlite: course activity not found")

// CourseActivityRepository is the storage interface for course
// activity rows. Constructed once in cmd/orenda/main.go and passed
// to courseService as an optional dependency.
type CourseActivityRepository struct {
	db *sql.DB
}

// NewCourseActivityRepository wires a repo backed by db.
func NewCourseActivityRepository(db *sql.DB) *CourseActivityRepository {
	return &CourseActivityRepository{db: db}
}

// Create inserts an activity row. Validates first so we don't
// accept empty IDs or unknown enum values from upstream callers.
func (r *CourseActivityRepository) Create(ctx context.Context, a *course.Activity) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("course activity: create: %w", err)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO course_activity (id, course_id, actor_type, actor_id, kind, payload, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.CourseID, string(a.ActorType), a.ActorID, string(a.Kind), nullPayload(a.Payload), a.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("course activity: insert: %w", err)
	}
	return nil
}

// ListByCourse returns activity rows for the given course, newest
// first. limit=0 means "all rows" — used by tests; production
// callers always pass a limit.
func (r *CourseActivityRepository) ListByCourse(ctx context.Context, courseID string, limit int) ([]*course.Activity, error) {
	q := `SELECT id, course_id, actor_type, actor_id, kind, payload, created_at
	      FROM course_activity WHERE course_id = ?
	      ORDER BY created_at DESC, id DESC`
	args := []interface{}{courseID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("course activity: list: %w", err)
	}
	defer rows.Close()
	out := make([]*course.Activity, 0)
	for rows.Next() {
		a := &course.Activity{}
		var actorType, kind string
		var payload sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.CourseID, &actorType, &a.ActorID, &kind, &payload, &createdAt); err != nil {
			return nil, fmt.Errorf("course activity: scan: %w", err)
		}
		a.ActorType = course.ActorType(actorType)
		a.Kind = course.ActivityKind(kind)
		if payload.Valid {
			a.Payload = payload.String
		}
		ts, perr := time.Parse(time.RFC3339, createdAt)
		if perr != nil {
			return nil, fmt.Errorf("course activity: parse created_at %q: %w", createdAt, perr)
		}
		a.CreatedAt = ts
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("course activity: rows: %w", err)
	}
	return out, nil
}

// nullPayload converts an empty payload string to nil so it
// reads back as null in SQLite (consistent with the rest of the
// project; nullable columns are null, not "").
func nullPayload(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
