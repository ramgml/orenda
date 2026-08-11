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
			start_at, end_at, all_day, color, recurrence,
			created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?, ?,
			datetime('now'), datetime('now')
		)
	`
	_, err := r.db.ExecContext(ctx, q,
		t.ID, nullString(t.ProjectID), nullString(t.ParentTaskID), nullString(t.ColumnID),
		t.Title, t.Description,
		string(t.Status), string(t.Priority), nullString(string(t.AssigneeType)),
		nullString(t.AssigneeID), string(t.Awaiting),
		nullString(t.ContextMD), nullString(t.AgentNotes),
		nullString(formatTimePtr(t.DueAt)), nullString(formatTimePtr(t.StartedAt)),
		nullString(formatTimePtr(t.ClaimedAt)), nullString(formatTimePtr(t.CompletedAt)),
		nullIntPtr(t.TimeEstimateS), t.TimeSpentS, t.Position,
		nullString(formatTimePtr(t.StartAt)), nullString(formatTimePtr(t.EndAt)),
		boolToInt(t.AllDay), nullString(t.Color),
		nullString(t.Recurrence),
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
	// Phase 16: the lookup is no longer "always project-scoped". Three
	// shapes are accepted:
	//
	//   - f.NoProject == true                  → inbox (project_id IS NULL)
	//   - f.ProjectID != ""                    → that specific project
	//   - f.ProjectID == "" && !f.NoProject    → counted lookup (used
	//     by Service.Move to enumerate tasks in a single column,
	//     regardless of project — Phase 23.1 relies on this).
	//
	// Setting both NoProject and a non-empty ProjectID is rejected so
	// the caller has to make a choice.
	if f.NoProject && f.ProjectID != "" {
		return nil, task.ErrInvalidInput
	}

	var (
		clauses []string
		args    []any
	)
	switch {
	case f.NoProject:
		clauses = append(clauses, "project_id IS NULL")
	case f.ProjectID != "":
		clauses = append(clauses, "project_id = ?")
		args = append(args, f.ProjectID)
	default:
		// No filter — list everything across all projects. Service.Move
		// takes this branch with ColumnID set to enumerate a single
		// column; the inbox endpoint uses NoProject=true instead.
	}

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
	// ParentTaskID is tri-state. nil = no filter; &"" = top-level only;
	// &"abc" = direct children of task "abc". The kanban uses &"" so it
	// never shows nested children on the board.
	if f.ParentTaskID != nil {
		if *f.ParentTaskID == "" {
			clauses = append(clauses, "parent_task_id IS NULL")
		} else {
			clauses = append(clauses, "parent_task_id = ?")
			args = append(args, *f.ParentTaskID)
		}
	}

	q := selectTaskColumns
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY column_id ASC, position ASC, created_at ASC"

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
	// Phase 16 added `project_id` to the SET list so a PATCH that
	// files an inbox card under a project (or vice versa) actually
	// persists. The empty-string case stays valid: Validate()
	// accepts "" alongside ColumnID="". Phase 23.3 added
	// `recurrence` so the calendar's RRULE expansion can read it
	// back from the persisted row.
	const q = `
		UPDATE tasks SET
			project_id = ?, parent_task_id = ?, column_id = ?, title = ?, description = ?,
			status = ?, priority = ?, assignee_type = ?, assignee_id = ?,
			awaiting = ?, context_md = ?, agent_notes = ?,
			due_at = ?, started_at = ?, claimed_at = ?, completed_at = ?,
			time_estimate_s = ?, time_spent_s = ?, position = ?,
			start_at = ?, end_at = ?, all_day = ?, color = ?, recurrence = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q,
		nullString(t.ProjectID),
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
		nullString(t.Recurrence),
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

// ListChildren returns every direct child of parentID, ordered by
// position then created_at. Returns an empty slice when the parent
// has no children, ErrNotFound when the parent itself doesn't exist
// (so callers can distinguish "no children" from "bad parent id").
func (r *taskRepo) ListChildren(ctx context.Context, parentID string) ([]*task.Task, error) {
	// Confirm the parent exists first so we can return ErrNotFound
	// instead of a silently-empty slice when callers typo an id.
	var exists int
	if err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM tasks WHERE id = ?`, parentID,
	).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, task.ErrNotFound
		}
		return nil, fmt.Errorf("task.ListChildren: parent lookup: %w", err)
	}

	rows, err := r.db.QueryContext(ctx,
		selectTaskColumns+`
		WHERE parent_task_id = ?
		ORDER BY position ASC, created_at ASC`,
		parentID)
	if err != nil {
		return nil, fmt.Errorf("task.ListChildren: %w", err)
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

// ChildProgress returns the total number of children of parentID
// and how many of those have status='done'. Used to render a
// progress bar on the parent task view.
func (r *taskRepo) ChildProgress(ctx context.Context, parentID string) (total, done int, err error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT
		    COUNT(*),
		    COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0)
		FROM tasks
		WHERE parent_task_id = ?
	`, parentID)
	if err := row.Scan(&total, &done); err != nil {
		return 0, 0, fmt.Errorf("task.ChildProgress: %w", err)
	}
	return total, done, nil
}

// ---------------------------------------------------------------------------
// Checklists
// ---------------------------------------------------------------------------

func (r *taskRepo) AddChecklist(ctx context.Context, taskID, title string) (*task.ChecklistRow, error) {
	if taskID == "" || title == "" {
		return nil, errors.New("task.AddChecklist: empty taskID or title")
	}
	var pos int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM checklists WHERE task_id = ?`,
		taskID).Scan(&pos); err != nil {
		return nil, fmt.Errorf("task.AddChecklist: peek position: %w", err)
	}
	id := newUUID()
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO checklists (id, task_id, title, position) VALUES (?, ?, ?, ?)`,
		id, taskID, title, pos); err != nil {
		return nil, fmt.Errorf("task.AddChecklist: %w", err)
	}
	return &task.ChecklistRow{ID: id, TaskID: taskID, Title: title, Position: pos}, nil
}

func (r *taskRepo) ListChecklists(ctx context.Context, taskID string) ([]task.ChecklistRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, task_id, title, position FROM checklists
		 WHERE task_id = ? ORDER BY position ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("task.ListChecklists: %w", err)
	}
	defer rows.Close()
	out := make([]task.ChecklistRow, 0)
	for rows.Next() {
		var c task.ChecklistRow
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Title, &c.Position); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *taskRepo) DeleteChecklist(ctx context.Context, listID string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM checklists WHERE id = ?`, listID); err != nil {
		return fmt.Errorf("task.DeleteChecklist: %w", err)
	}
	return nil
}

func (r *taskRepo) AddChecklistItem(ctx context.Context, listID, title string) (*task.ChecklistItemRow, error) {
	if listID == "" || title == "" {
		return nil, errors.New("task.AddChecklistItem: empty listID or title")
	}
	var pos int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM checklist_items WHERE checklist_id = ?`,
		listID).Scan(&pos); err != nil {
		return nil, fmt.Errorf("task.AddChecklistItem: peek position: %w", err)
	}
	id := newUUID()
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO checklist_items (id, checklist_id, title, done, position)
		 VALUES (?, ?, ?, 0, ?)`,
		id, listID, title, pos); err != nil {
		return nil, fmt.Errorf("task.AddChecklistItem: %w", err)
	}
	return &task.ChecklistItemRow{
		ID:          id,
		ChecklistID: listID,
		Title:       title,
		Done:        false,
		Position:    pos,
	}, nil
}

func (r *taskRepo) ListChecklistItems(ctx context.Context, listID string) ([]task.ChecklistItemRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, checklist_id, title, done, position
		 FROM checklist_items WHERE checklist_id = ?
		 ORDER BY position ASC`, listID)
	if err != nil {
		return nil, fmt.Errorf("task.ListChecklistItems: %w", err)
	}
	defer rows.Close()
	out := make([]task.ChecklistItemRow, 0)
	for rows.Next() {
		var (
			it      task.ChecklistItemRow
			doneInt int
		)
		if err := rows.Scan(&it.ID, &it.ChecklistID, &it.Title, &doneInt, &it.Position); err != nil {
			return nil, err
		}
		it.Done = doneInt != 0
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *taskRepo) UpdateChecklistItem(ctx context.Context, itemID string, done *bool, title *string) error {
	sets := ""
	args := []any{}
	if done != nil {
		sets += ", done = ?"
		v := 0
		if *done {
			v = 1
		}
		args = append(args, v)
	}
	if title != nil {
		sets += ", title = ?"
		args = append(args, *title)
	}
	if sets == "" {
		return nil
	}
	args = append(args, itemID)
	q := "UPDATE checklist_items SET id = id" + sets + " WHERE id = ?"
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("task.UpdateChecklistItem: %w", err)
	}
	return nil
}

func (r *taskRepo) DeleteChecklistItem(ctx context.Context, itemID string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM checklist_items WHERE id = ?`, itemID); err != nil {
		return fmt.Errorf("task.DeleteChecklistItem: %w", err)
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
       start_at, end_at, all_day, color, recurrence, created_at, updated_at
FROM tasks
`

// scanTask reads one row (sql.Row) into a Task.
//
// Phase 16: project_id is scanned via sql.NullString because the column
// became nullable. NULL → "" (the "Inbox" representation in Go).
// Phase 23.3: recurrence is scanned the same way (NULL → "").
func scanTask(row *sql.Row) (*task.Task, error) {
	var t task.Task
	var (
		projectID, parent, columnID    sql.NullString
		desc, assigneeType, assigneeID sql.NullString
		contextMD, agentNotes          sql.NullString
		due, started, claimed, compl   sql.NullString
		calStart, calEnd, color        sql.NullString
		recurrence                     sql.NullString
		allDay                         int
		estS                           sql.NullInt64
		status, priority, awaiting     string
		created, updated               string
	)
	err := row.Scan(
		&t.ID, &projectID, &parent, &columnID, &t.Title, &desc,
		&status, &priority, &assigneeType, &assigneeID, &awaiting,
		&contextMD, &agentNotes,
		&due, &started, &claimed, &compl,
		&estS, &t.TimeSpentS, &t.Position,
		&calStart, &calEnd, &allDay, &color, &recurrence, &created, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, task.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("task.Scan: %w", err)
	}
	t.ProjectID = projectID.String
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
	t.Recurrence = recurrence.String
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
		       start_at, end_at, all_day, color, recurrence, created_at, updated_at
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
		projectID, parent, columnID    sql.NullString
		desc, assigneeType, assigneeID sql.NullString
		contextMD, agentNotes          sql.NullString
		due, started, claimed, compl   sql.NullString
		calStart, calEnd, color        sql.NullString
		recurrence                     sql.NullString
		allDay                         int
		estS                           sql.NullInt64
		status, priority, awaiting     string
		created, updated               string
	)
	err := rows.Scan(
		&t.ID, &projectID, &parent, &columnID, &t.Title, &desc,
		&status, &priority, &assigneeType, &assigneeID, &awaiting,
		&contextMD, &agentNotes,
		&due, &started, &claimed, &compl,
		&estS, &t.TimeSpentS, &t.Position,
		&calStart, &calEnd, &allDay, &color, &recurrence, &created, &updated,
	)
	if err != nil {
		return nil, fmt.Errorf("task.ScanRow: %w", err)
	}
	t.ProjectID = projectID.String
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
	t.Recurrence = recurrence.String
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
