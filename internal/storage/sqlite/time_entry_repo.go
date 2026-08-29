// Package sqlite — time_entries repository.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ramgml/orenda/internal/domain/timeentry"
)

// timeEntryRepo persists time_entries.
type timeEntryRepo struct {
	db *sql.DB
}

// NewTimeEntryRepository returns a Phase 4 repo.
func NewTimeEntryRepository(db *sql.DB) timeentry.Repository {
	return &timeEntryRepo{db: db}
}

func (r *timeEntryRepo) Create(ctx context.Context, e *timeentry.TimeEntry) (*timeentry.TimeEntry, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if e.ID == "" {
		e.ID = newUUID()
	}

	// Enforce the single-active-timer invariant at the storage layer:
	// if the new row has ended_at IS NULL, no other row with
	// ended_at IS NULL may exist for the same agent.
	if e.EndedAt == nil {
		open, err := r.FindOpen(ctx, e.AgentID)
		if err != nil {
			return nil, err
		}
		if open != nil {
			return nil, timeentry.ErrAlreadyOpen
		}
	}

	const q = `
		INSERT INTO time_entries (id, task_id, agent_id, started_at, ended_at, duration_s, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	_, err := r.db.ExecContext(ctx, q,
		e.ID, e.TaskID, e.AgentID, formatTime(e.StartedAt),
		nullString(formatTimePtr(e.EndedAt)), nullInt64Ptr(e.DurationS),
		string(e.Source),
	)
	if err != nil {
		// Race: another open timer was created between our check and
		// our insert. Translate the UNIQUE/PK or any FK error to the
		// domain's "already open" sentinel.
		return nil, fmt.Errorf("timeentry.Create: %w", err)
	}
	return r.GetByID(ctx, e.ID)
}

func (r *timeEntryRepo) GetByID(ctx context.Context, id string) (*timeentry.TimeEntry, error) {
	const q = timeEntrySelectColumns + " WHERE id = ?"
	row := r.db.QueryRowContext(ctx, q, id)
	return scanTimeEntry(row)
}

// CloseAndAccrue implements timeentry.Repository. The UPDATE of the
// time_entries row and the increment of tasks.time_spent_s share one
// transaction: either both land or neither does, so the stored
// per-task total can never drift from the entries behind it. The
// accrual is a relative UPDATE (time_spent_s = time_spent_s + ?) so
// concurrent closes on the same task cannot lose an increment.
func (r *timeEntryRepo) CloseAndAccrue(ctx context.Context, e *timeentry.TimeEntry) error {
	if err := e.Validate(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("timeentry.CloseAndAccrue: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `
		UPDATE time_entries
		SET task_id = ?, agent_id = ?, started_at = ?, ended_at = ?, duration_s = ?, source = ?
		WHERE id = ?
	`, e.TaskID, e.AgentID, formatTime(e.StartedAt),
		nullString(formatTimePtr(e.EndedAt)), nullInt64Ptr(e.DurationS),
		string(e.Source), e.ID); err != nil {
		err = fmt.Errorf("timeentry.CloseAndAccrue: update: %w", err)
		return err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE tasks SET time_spent_s = time_spent_s + ? WHERE id = ?`,
		entryDuration(e), e.TaskID); err != nil {
		err = fmt.Errorf("timeentry.CloseAndAccrue: accrue: %w", err)
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("timeentry.CloseAndAccrue: commit: %w", err)
	}
	return nil
}

// CreateAndAccrue implements timeentry.Repository. The INSERT of the
// closed manual entry and the task accrual share one transaction —
// same atomicity contract as CloseAndAccrue. A non-nil FK error on a
// missing task rolls the tx back and surfaces as a plain wrapped
// error; the caller sees no partial state.
func (r *timeEntryRepo) CreateAndAccrue(ctx context.Context, e *timeentry.TimeEntry) (*timeentry.TimeEntry, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if e.ID == "" {
		e.ID = newUUID()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("timeentry.CreateAndAccrue: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO time_entries (id, task_id, agent_id, started_at, ended_at, duration_s, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, e.ID, e.TaskID, e.AgentID, formatTime(e.StartedAt),
		nullString(formatTimePtr(e.EndedAt)), nullInt64Ptr(e.DurationS),
		string(e.Source)); err != nil {
		err = fmt.Errorf("timeentry.CreateAndAccrue: insert: %w", err)
		return nil, err
	}
	if _, err = tx.ExecContext(ctx,
		`UPDATE tasks SET time_spent_s = time_spent_s + ? WHERE id = ?`,
		entryDuration(e), e.TaskID); err != nil {
		err = fmt.Errorf("timeentry.CreateAndAccrue: accrue: %w", err)
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("timeentry.CreateAndAccrue: commit: %w", err)
	}
	return r.GetByID(ctx, e.ID)
}

func (r *timeEntryRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM time_entries WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("timeentry.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return timeentry.ErrNotFound
	}
	return nil
}

func (r *timeEntryRepo) FindOpen(ctx context.Context, agentID string) (*timeentry.TimeEntry, error) {
	const q = timeEntrySelectColumns + " WHERE agent_id = ? AND ended_at IS NULL ORDER BY started_at DESC LIMIT 1"
	row := r.db.QueryRowContext(ctx, q, agentID)
	te, err := scanTimeEntry(row)
	if errors.Is(err, timeentry.ErrNotFound) {
		//nolint:nilnil // Find-semantics: no open entry is a normal outcome; callers branch on nil.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return te, nil
}

func (r *timeEntryRepo) ListByTask(ctx context.Context, taskID string) ([]*timeentry.TimeEntry, error) {
	const q = timeEntrySelectColumns + " WHERE task_id = ? ORDER BY started_at DESC"
	rows, err := r.db.QueryContext(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("timeentry.ListByTask: %w", err)
	}
	return scanTimeEntryList(rows)
}

func (r *timeEntryRepo) ListByAgent(ctx context.Context, agentID string, from, to time.Time) ([]*timeentry.TimeEntry, error) {
	const q = timeEntrySelectColumns +
		" WHERE agent_id = ? AND started_at < ? AND (ended_at IS NULL OR ended_at > ?) ORDER BY started_at DESC"
	rows, err := r.db.QueryContext(ctx, q, agentID, formatTime(to), formatTime(from))
	if err != nil {
		return nil, fmt.Errorf("timeentry.ListByAgent: %w", err)
	}
	return scanTimeEntryList(rows)
}

// ListAll implements timeentry.Repository. Same window predicate as
// ListByAgent, minus the actor filter: agent_id carries both agent
// UUIDs and user ids, so the unfiltered variant is "all actors".
func (r *timeEntryRepo) ListAll(ctx context.Context, from, to time.Time) ([]*timeentry.TimeEntry, error) {
	const q = timeEntrySelectColumns +
		" WHERE started_at < ? AND (ended_at IS NULL OR ended_at > ?) ORDER BY started_at DESC"
	rows, err := r.db.QueryContext(ctx, q, formatTime(to), formatTime(from))
	if err != nil {
		return nil, fmt.Errorf("timeentry.ListAll: %w", err)
	}
	return scanTimeEntryList(rows)
}

func (r *timeEntryRepo) ListByDay(ctx context.Context, agentID string, day time.Time) ([]*timeentry.TimeEntry, error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	dayEnd := dayStart.Add(24 * time.Hour)
	return r.ListByAgent(ctx, agentID, dayStart, dayEnd)
}

const timeEntrySelectColumns = `
SELECT id, task_id, agent_id, started_at, ended_at, duration_s, source
FROM time_entries
`

func scanTimeEntry(row *sql.Row) (*timeentry.TimeEntry, error) {
	var (
		e        timeentry.TimeEntry
		started  string
		endedAt  sql.NullString
		duration sql.NullInt64
	)
	err := row.Scan(&e.ID, &e.TaskID, &e.AgentID, &started, &endedAt, &duration, &e.Source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, timeentry.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("timeentry.Scan: %w", err)
	}
	e.StartedAt = parseTime(started)
	if endedAt.Valid {
		t := parseTime(endedAt.String)
		e.EndedAt = &t
	}
	if duration.Valid {
		v := duration.Int64
		e.DurationS = &v
	}
	return &e, nil
}

func scanTimeEntryList(rows *sql.Rows) ([]*timeentry.TimeEntry, error) {
	defer rows.Close()
	out := make([]*timeentry.TimeEntry, 0)
	for rows.Next() {
		var (
			e        timeentry.TimeEntry
			started  string
			endedAt  sql.NullString
			duration sql.NullInt64
		)
		if err := rows.Scan(&e.ID, &e.TaskID, &e.AgentID, &started, &endedAt, &duration, &e.Source); err != nil {
			return nil, fmt.Errorf("timeentry.ScanRows: %w", err)
		}
		e.StartedAt = parseTime(started)
		if endedAt.Valid {
			t := parseTime(endedAt.String)
			e.EndedAt = &t
		}
		if duration.Valid {
			v := duration.Int64
			e.DurationS = &v
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

func nullInt64Ptr(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

// entryDuration returns the entry duration in seconds, or 0 when the
// entry is still open (nil DurationS). Open entries carry no time to
// accrue.
func entryDuration(e *timeentry.TimeEntry) int64 {
	if e.DurationS == nil {
		return 0
	}
	return *e.DurationS
}
