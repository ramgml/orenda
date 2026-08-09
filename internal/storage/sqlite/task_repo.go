// Package sqlite — Task repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ramgml/orenda/internal/domain/task"
)

// taskRepo persists tasks and subtasks.
type taskRepo struct {
	db *sql.DB
}

// NewTaskRepository returns a task.Repository backed by db.
func NewTaskRepository(db *sql.DB) task.Repository {
	return &taskRepo{db: db}
}

func (r *taskRepo) Create(ctx context.Context, t *task.Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.ID == "" {
		t.ID = newUUID()
	}

	const q = `
		INSERT INTO tasks (
			id, project_id, parent_task_id, column_id, title, description,
			status, priority, assignee_type, assignee_id, awaiting,
			context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
			time_estimate_s, time_spent_s, position,
			start_at, end_at, all_day, color,
			created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			datetime('now'), datetime('now')
		)
	`
	_, err := r.db.ExecContext(ctx, q,
		t.ID, t.ProjectID, nullString(t.ParentTaskID), nullString(t.ColumnID),
		t.Title, t.Description,
		string(t.Status), string(t.Priority), nullString(string(t.AssigneeType)),
		nullString(t.AssigneeID), string(t.Awaiting),
		nullString(t.ContextMD), nullString(t.AgentNotes),
		nullString(formatTimePtr(t.DueAt)), nullString(formatTimePtr(t.StartedAt)),
		nullString(formatTimePtr(t.ClaimedAt)), nullString(formatTimePtr(t.CompletedAt)),
		nullIntPtr(t.TimeEstimateS), t.TimeSpentS, t.Position,
		nullString(formatTimePtr(t.StartAt)), nullString(formatTimePtr(t.EndAt)),
		boolToInt(t.AllDay), nullString(t.Color),
	)
	if err != nil {
		return fmt.Errorf("task.Create: %w", err)
	}
	got, err := r.GetByID(ctx, t.ID)
	if err != nil {
		return err
	}
	*t = *got
	return nil
}

func (r *taskRepo) GetByID(ctx context.Context, id string) (*task.Task, error) {
	const q = selectTaskColumns + " WHERE id = ?"
	return scanTask(r.db.QueryRowContext(ctx, q, id))
}

func (r *taskRepo) ListByProject(ctx context.Context, f task.Filter) ([]*task.Task, error) {
	if f.ProjectID == "" {
		return nil, task.ErrInvalidInput
	}

	var (
		clauses []string
		args    []any
	)
	clauses = append(clauses, "project_id = ?")
	args = append(args, f.ProjectID)

	if f.ColumnID != "" {
		clauses = append(clauses, "column_id = ?")
		args = append(args, f.ColumnID)
	}
	if f.Status != "" {
		clauses = append(clauses, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.AssigneeType != "" {
		clauses = append(clauses, "assignee_type = ?")
		args = append(args, string(f.AssigneeType))
	}
	if f.AssigneeID != "" {
		clauses = append(clauses, "assignee_id = ?")
		args = append(args, f.AssigneeID)
	}

	q := selectTaskColumns +
		" WHERE " + strings.Join(clauses, " AND ") +
		" ORDER BY column_id ASC, position ASC, created_at ASC"

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("task.ListByProject: %w", err)
	}
	defer rows.Close()

	out := make([]*task.Task, 0)
	for rows.Next() {
		tr, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

func (r *taskRepo) Update(ctx context.Context, t *task.Task) error {
	if err := t.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE tasks SET
			parent_task_id = ?, column_id = ?, title = ?, description = ?,
			status = ?, priority = ?, assignee_type = ?, assignee_id = ?,
			awaiting = ?, context_md = ?, agent_notes = ?,
			due_at = ?, started_at = ?, claimed_at = ?, completed_at = ?,
			time_estimate_s = ?, time_spent_s = ?, position = ?,
			start_at = ?, end_at = ?, all_day = ?, color = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q,
		nullString(t.ParentTaskID), nullString(t.ColumnID),
		t.Title, t.Description,
		string(t.Status), string(t.Priority),
		nullString(string(t.AssigneeType)), nullString(t.AssigneeID),
		string(t.Awaiting), nullString(t.ContextMD), nullString(t.AgentNotes),
		nullString(formatTimePtr(t.DueAt)), nullString(formatTimePtr(t.StartedAt)),
		nullString(formatTimePtr(t.ClaimedAt)), nullString(formatTimePtr(t.CompletedAt)),
		nullIntPtr(t.TimeEstimateS), t.TimeSpentS, t.Position,
		nullString(formatTimePtr(t.StartAt)), nullString(formatTimePtr(t.EndAt)),
		boolToInt(t.AllDay), nullString(t.Color),
		t.ID,
	)
	if err != nil {
		return fmt.Errorf("task.Update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return task.ErrNotFound
	}
	got, err := r.GetByID(ctx, t.ID)
	if err != nil {
		return err
	}
	*t = *got
	return nil
}

func (r *taskRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("task.Delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return task.ErrNotFound
	}
	return nil
}

func (r *taskRepo) AddSubtask(ctx context.Context, s *task.Subtask) error {
	if s.ID == "" {
		s.ID = newUUID()
	}
	if s.Title == "" {
		return task.ErrInvalidInput
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO subtasks (id, task_id, title, done, position) VALUES (?, ?, ?, ?, ?)`,
		s.ID, s.TaskID, s.Title, boolToInt(s.Done), s.Position,
	)
	if err != nil {
		return fmt.Errorf("task.AddSubtask: %w", err)
	}
	return nil
}

func (r *taskRepo) ListSubtasks(ctx context.Context, taskID string) ([]*task.Subtask, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, task_id, title, done, position FROM subtasks WHERE task_id = ? ORDER BY position ASC`,
		taskID)
	if err != nil {
		return nil, fmt.Errorf("task.ListSubtasks: %w", err)
	}
	defer rows.Close()

	out := make([]*task.Subtask, 0)
	for rows.Next() {
		var (
			s task.Subtask
			d int
		)
		if err := rows.Scan(&s.ID, &s.TaskID, &s.Title, &d, &s.Position); err != nil {
			return nil, err
		}
		s.Done = d != 0
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (r *taskRepo) UpdateSubtask(ctx context.Context, s *task.Subtask) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE subtasks SET title = ?, done = ?, position = ? WHERE id = ?`,
		s.Title, boolToInt(s.Done), s.Position, s.ID,
	)
	if err != nil {
		return fmt.Errorf("task.UpdateSubtask: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return task.ErrNotFound
	}
	return nil
}

func (r *taskRepo) DeleteSubtask(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM subtasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("task.DeleteSubtask: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return task.ErrNotFound
	}
	return nil
}

// CountByColumn returns the number of tasks in the given column.
//
// Includes tasks in any status (backlog, todo, in_progress, review, done).
// Phase 2.8 keeps it simple — the kanban WIP limit reflects total tasks
// visible in the column, not "active" ones, so users don't lose count
// when dragging between statuses.
func (r *taskRepo) CountByColumn(ctx context.Context, columnID string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE column_id = ?`, columnID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("task.CountByColumn: %w", err)
	}
	return n, nil
}

// FirstColumnID returns the id of the first column of the project's
// default board, ordered by position ASC. Empty string if the
// project has no board (e.g. an unfinished bootstrap). Used by the
// event service to park new calendar tasks into a real kanban
// column so they show up on the project page.
func (r *taskRepo) FirstColumnID(ctx context.Context, projectID string) (string, error) {
	var colID string
	err := r.db.QueryRowContext(ctx, `
		SELECT c.id
		FROM columns c
		JOIN boards  b ON c.board_id = b.id
		WHERE b.project_id = ?
		ORDER BY c.position ASC
		LIMIT 1`, projectID).Scan(&colID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("task.FirstColumnID: %w", err)
	}
	return colID, nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// selectTaskColumns is the column list used by every task query; keeping it
// in one place ensures the scanner below stays in sync with the SQL.
const selectTaskColumns = `
SELECT id, project_id, parent_task_id, column_id, title, description,
       status, priority, assignee_type, assignee_id, awaiting,
       context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
       time_estimate_s, time_spent_s, position,
       start_at, end_at, all_day, color, created_at, updated_at
FROM tasks
`

// scanTask reads one row (sql.Row) into a Task.
func scanTask(row *sql.Row) (*task.Task, error) {
	var t task.Task
	var (
		parent, columnID               sql.NullString
		desc, assigneeType, assigneeID sql.NullString
		contextMD, agentNotes          sql.NullString
		due, started, claimed, compl   sql.NullString
		calStart, calEnd, color        sql.NullString
		allDay                         int
		estS                           sql.NullInt64
		status, priority, awaiting     string
		created, updated               string
	)
	err := row.Scan(
		&t.ID, &t.ProjectID, &parent, &columnID, &t.Title, &desc,
		&status, &priority, &assigneeType, &assigneeID, &awaiting,
		&contextMD, &agentNotes,
		&due, &started, &claimed, &compl,
		&estS, &t.TimeSpentS, &t.Position,
		&calStart, &calEnd, &allDay, &color, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, task.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("task.Scan: %w", err)
	}
	t.ParentTaskID = parent.String
	t.ColumnID = columnID.String
	t.Description = desc.String
	t.Status = task.Status(status)
	t.Priority = task.Priority(priority)
	t.AssigneeType = task.AssigneeType(assigneeType.String)
	t.AssigneeID = assigneeID.String
	t.Awaiting = task.Awaiting(awaiting)
	t.ContextMD = contextMD.String
	t.AgentNotes = agentNotes.String
	t.DueAt = parseTimePtr(due)
	t.StartedAt = parseTimePtr(started)
	t.ClaimedAt = parseTimePtr(claimed)
	t.CompletedAt = parseTimePtr(compl)
	t.StartAt = parseTimePtr(calStart)
	t.EndAt = parseTimePtr(calEnd)
	t.AllDay = allDay != 0
	t.Color = color.String
	if estS.Valid {
		v := int(estS.Int64)
		t.TimeEstimateS = &v
	}
	t.CreatedAt = parseTime(created)
	t.UpdatedAt = parseTime(updated)
	return &t, nil
}

// ListInRange returns every timed task (start_at NOT NULL) whose
// interval [start_at, end_at] overlaps the [from, to] window. Used by
// the calendar view; the partial idx_tasks_time index keeps it cheap.
//
// projectID="" means "all projects the owner has access to" (which in
// Phase 11 single-owner mode means everything).
func (r *taskRepo) ListInRange(ctx context.Context, from, to time.Time, projectID string) ([]*task.Task, error) {
	const base = `
		SELECT id, project_id, parent_task_id, column_id, title, description,
		       status, priority, assignee_type, assignee_id, awaiting,
		       context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
		       time_estimate_s, time_spent_s, position,
		       start_at, end_at, all_day, color, created_at, updated_at
		FROM tasks
		WHERE start_at IS NOT NULL AND end_at IS NOT NULL
		  AND start_at < ? AND end_at > ?`
	// Bind args as UTC strings. formatTime writes UTC strings like
	// "2026-08-09 09:30:00", and modernc.org/sqlite serialises a bound
	// time.Time in its own way (e.g. "2026-08-09 12:30:00 +0400 +04")
	// which is lexicographically larger than our format. Forcing both
	// sides to UTC strings makes the comparison deterministic.
	args := []any{to.UTC().Format("2006-01-02 15:04:05"), from.UTC().Format("2006-01-02 15:04:05")}
	q := base
	if projectID != "" {
		q += " AND project_id = ?"
		args = append(args, projectID)
	}
	q += " ORDER BY start_at ASC"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("task.ListInRange: %w", err)
	}
	defer rows.Close()
	out := make([]*task.Task, 0)
	for rows.Next() {
		tr, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}
func scanTaskRow(rows *sql.Rows) (*task.Task, error) {
	var t task.Task
	var (
		parent, columnID               sql.NullString
		desc, assigneeType, assigneeID sql.NullString
		contextMD, agentNotes          sql.NullString
		due, started, claimed, compl   sql.NullString
		calStart, calEnd, color        sql.NullString
		allDay                         int
		estS                           sql.NullInt64
		status, priority, awaiting     string
		created, updated               string
	)
	err := rows.Scan(
		&t.ID, &t.ProjectID, &parent, &columnID, &t.Title, &desc,
		&status, &priority, &assigneeType, &assigneeID, &awaiting,
		&contextMD, &agentNotes,
		&due, &started, &claimed, &compl,
		&estS, &t.TimeSpentS, &t.Position,
		&calStart, &calEnd, &allDay, &color, &created, &updated,
	)
	if err != nil {
		return nil, fmt.Errorf("task.ScanRow: %w", err)
	}
	t.ParentTaskID = parent.String
	t.ColumnID = columnID.String
	t.Description = desc.String
	t.Status = task.Status(status)
	t.Priority = task.Priority(priority)
	t.AssigneeType = task.AssigneeType(assigneeType.String)
	t.AssigneeID = assigneeID.String
	t.Awaiting = task.Awaiting(awaiting)
	t.ContextMD = contextMD.String
	t.AgentNotes = agentNotes.String
	t.DueAt = parseTimePtr(due)
	t.StartedAt = parseTimePtr(started)
	t.ClaimedAt = parseTimePtr(claimed)
	t.CompletedAt = parseTimePtr(compl)
	t.StartAt = parseTimePtr(calStart)
	t.EndAt = parseTimePtr(calEnd)
	t.AllDay = allDay != 0
	t.Color = color.String
	if estS.Valid {
		v := int(estS.Int64)
		t.TimeEstimateS = &v
	}
	t.CreatedAt = parseTime(created)
	t.UpdatedAt = parseTime(updated)
	return &t, nil
}

// nullString converts a Go string to sql.NullString; empty string becomes
// a NULL value, which is the convention for optional foreign keys.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullIntPtr converts a Go *int to sql.NullInt64.
func nullIntPtr(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

// formatTimePtr renders *time.Time as the SQLite string format, "" if nil.
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
}

// parseTimePtr parses a SQLite timestamp string into a *time.Time.
// Returns nil if the input is empty/NULL.
func parseTimePtr(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTime(s.String)
	if t.IsZero() {
		return nil
	}
	return &t
}
