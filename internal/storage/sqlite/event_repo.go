// Package sqlite — event repository.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/event"
)

// eventRepo persists calendar events.
type eventRepo struct {
	db *sql.DB
}

// NewEventRepository returns a Phase 4 event repo.
func NewEventRepository(db *sql.DB) event.Repository {
	return &eventRepo{db: db}
}

func (r *eventRepo) Create(ctx context.Context, e *event.Event) (*event.Event, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if e.ID == "" {
		e.ID = newUUID()
	}
	const q = `
		INSERT INTO events (
			id, title, description, start_at, end_at, all_day, color,
			project_id, recurrence_rule, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), datetime('now'))
	`
	_, err := r.db.ExecContext(ctx, q,
		e.ID, e.Title, e.Description, formatTime(e.StartAt), formatTime(e.EndAt),
		boolToInt(e.AllDay), e.Color, nullString(e.ProjectID), nullString(e.Recurrence),
	)
	if err != nil {
		return nil, fmt.Errorf("event.Create: %w", err)
	}
	return r.GetByID(ctx, e.ID)
}

func (r *eventRepo) GetByID(ctx context.Context, id string) (*event.Event, error) {
	const q = eventSelectColumns + " WHERE id = ?"
	row := r.db.QueryRowContext(ctx, q, id)
	return scanEvent(row)
}

func (r *eventRepo) Update(ctx context.Context, e *event.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE events
		SET title = ?, description = ?, start_at = ?, end_at = ?,
		    all_day = ?, color = ?, project_id = ?, recurrence_rule = ?,
		    updated_at = datetime('now')
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q,
		e.Title, e.Description, formatTime(e.StartAt), formatTime(e.EndAt),
		boolToInt(e.AllDay), e.Color, nullString(e.ProjectID), nullString(e.Recurrence), e.ID,
	)
	if err != nil {
		return fmt.Errorf("event.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return event.ErrNotFound
	}
	return nil
}

func (r *eventRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("event.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return event.ErrNotFound
	}
	return nil
}

func (r *eventRepo) ListInRange(ctx context.Context, from, to time.Time, projectID string) ([]*event.Event, error) {
	q := eventSelectColumns + " WHERE start_at < ? AND end_at > ?"
	args := []any{formatTime(to), formatTime(from)}
	if projectID != "" {
		q += " AND project_id = ?"
		args = append(args, projectID)
	}
	q += " ORDER BY start_at ASC"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("event.ListInRange: %w", err)
	}
	defer rows.Close()
	out := make([]*event.Event, 0)
	for rows.Next() {
		e, err := scanEventRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const eventSelectColumns = `
SELECT id, title, description, start_at, end_at, all_day, color,
       project_id, recurrence_rule, created_at, updated_at
FROM events
`

func scanEvent(row *sql.Row) (*event.Event, error) {
	var (
		e          event.Event
		desc       sql.NullString
		startAt    string
		endAt      string
		allDay     int
		color      sql.NullString
		projectID  sql.NullString
		recurrence sql.NullString
		cAt        string
		uAt        string
	)
	err := row.Scan(&e.ID, &e.Title, &desc, &startAt, &endAt, &allDay, &color,
		&projectID, &recurrence, &cAt, &uAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, event.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("event.Scan: %w", err)
	}
	e.StartAt = parseTime(startAt)
	e.EndAt = parseTime(endAt)
	e.Description = desc.String
	e.AllDay = allDay != 0
	e.Color = color.String
	e.ProjectID = projectID.String
	e.Recurrence = recurrence.String
	e.CreatedAt = parseTime(cAt)
	e.UpdatedAt = parseTime(uAt)
	return &e, nil
}

func scanEventRows(rows *sql.Rows) (*event.Event, error) {
	var (
		e          event.Event
		desc       sql.NullString
		startAt    string
		endAt      string
		allDay     int
		color      sql.NullString
		projectID  sql.NullString
		recurrence sql.NullString
		cAt        string
		uAt        string
	)
	if err := rows.Scan(&e.ID, &e.Title, &desc, &startAt, &endAt, &allDay, &color,
		&projectID, &recurrence, &cAt, &uAt); err != nil {
		return nil, fmt.Errorf("event.ScanRows: %w", err)
	}
	e.StartAt = parseTime(startAt)
	e.EndAt = parseTime(endAt)
	e.Description = desc.String
	e.AllDay = allDay != 0
	e.Color = color.String
	e.ProjectID = projectID.String
	e.Recurrence = recurrence.String
	e.CreatedAt = parseTime(cAt)
	e.UpdatedAt = parseTime(uAt)
	return &e, nil
}
