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

	// number comes from the task_number_seq high-watermark, not from
	// MAX(tasks.number): a MAX+1 would re-issue the newest task's
	// number after that task is deleted, and a "#42" reference in a
	// commit message, branch name or PR title must keep pointing at
	// the same task forever. The watermark UPDATE...RETURNING and the
	// INSERT share one transaction, so the draw is atomic and the
	// sequence can never run backwards.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("task.Create: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var number int
	if err := tx.QueryRowContext(ctx,
		`UPDATE task_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&number); err != nil {
		return fmt.Errorf("task.Create: draw number: %w", err)
	}

	const q = `
		INSERT INTO tasks (
			id, project_id, parent_task_id, column_id, title, description,
			status, priority, assignee_type, assignee_id, awaiting,
			context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
			time_estimate_s, time_spent_s, position,
			start_at, end_at, all_day, color, recurrence,
			study_course_id,
			number,
			created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?, ?,
			?,
			?,
			datetime('now'), datetime('now')
		)
	`
	_, err = tx.ExecContext(ctx, q,
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
		nullString(t.StudyCourseID),
		number,
	)
	if err != nil {
		return fmt.Errorf("task.Create: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("task.Create: commit: %w", err)
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

// GetByNumber resolves the human-readable "#N" reference to a task.
// The UNIQUE index idx_tasks_number (migration 033) makes this an
// index point lookup.
func (r *taskRepo) GetByNumber(ctx context.Context, number int) (*task.Task, error) {
	const q = selectTaskColumns + " WHERE number = ?"
	return scanTask(r.db.QueryRowContext(ctx, q, number))
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
		if f.AssigneeTypeIncludeNull {
			// Phase 33.1: unassigned rows are claimable too.
			clauses = append(clauses, "(assignee_type = ? OR assignee_type IS NULL)")
		} else {
			clauses = append(clauses, "assignee_type = ?")
		}
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
	// Phase 28.22: explicit id restriction (e.g. /today enriches only
	// the visible tasks instead of scanning the whole table).
	if len(f.IDs) > 0 {
		placeholders := strings.Repeat("?, ", len(f.IDs)-1) + "?"
		clauses = append(clauses, "id IN ("+placeholders+")")
		for _, id := range f.IDs {
			args = append(args, id)
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

// ListByProjectWithStats returns tasks from ListByProject with the
// per-task Counters (comments/attachments/children/checklist_items),
// BlockedByCount, and Tags populated in a single round-trip per metric.
//
// The implementation runs four aggregate queries keyed by the result
// set's task IDs rather than N+1 per-task queries. For a kanban
// board with 100 cards the per-render cost is bounded to ~5 SQL
// queries instead of ~100. Phase 27.3 adds a fifth: a batch
// TagsForTasks join that fills the Tags slice the TaskCard renders
// as coloured chips.
//
// Used by the /projects/{id}/board and /inbox/tasks endpoints
// (Phase 17; Phase 27.3 made the kanban chipper end-to-end).
func (r *taskRepo) ListByProjectWithStats(ctx context.Context, f task.Filter) ([]*task.Task, error) {
	tasks, err := r.ListByProject(ctx, f)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return tasks, nil
	}
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	counters, err := r.aggregateCounters(ctx, ids)
	if err != nil {
		return nil, err
	}
	blockers, err := r.aggregateBlockers(ctx, ids)
	if err != nil {
		return nil, err
	}
	tags, err := r.TagsForTasks(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if c, ok := counters[t.ID]; ok {
			t.Counters = &c
		}
		t.BlockedByCount = blockers[t.ID]
		// TagsForTasks pre-populates an empty slice for every input
		// id, so `tagged == nil` only happens if some other code path
		// stripped the entry — nil-check guards that case without
		// hiding real bugs.
		if ts, ok := tags[t.ID]; ok {
			t.Tags = ts
		}
	}
	return tasks, nil
}

// aggregateCounters returns counts[comments/attachments/children/
// checklist_items] for each task id. Tasks with no activity get
// zero-valued counters (not missing keys).
func (r *taskRepo) aggregateCounters(ctx context.Context, ids []string) (map[string]task.TaskCounters, error) {
	out := make(map[string]task.TaskCounters, len(ids))
	for _, id := range ids {
		out[id] = task.TaskCounters{}
	}
	placeholders := strings.Repeat("?, ", len(ids)-1) + "?"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	// comments: target_type='task' AND target_id IN (...)
	q := `SELECT target_id, COUNT(*) FROM comments
	      WHERE target_type = 'task' AND target_id IN (` + placeholders + `)
	      GROUP BY target_id`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregateCounters.comments: %w", err)
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		c := out[id]
		c.Comments = n
		out[id] = c
	}
	rows.Close()

	// attachments
	q = `SELECT target_id, COUNT(*) FROM attachments
	      WHERE target_type = 'task' AND target_id IN (` + placeholders + `)
	      GROUP BY target_id`
	rows, err = r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregateCounters.attachments: %w", err)
	}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		c := out[id]
		c.Attachments = n
		out[id] = c
	}
	rows.Close()

	// children (parent_task_id)
	q = `SELECT parent_task_id,
	             COUNT(*),
	             SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END)
	      FROM tasks WHERE parent_task_id IN (` + placeholders + `)
	      GROUP BY parent_task_id`
	rows, err = r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregateCounters.children: %w", err)
	}
	for rows.Next() {
		var id string
		var total, done int
		if err := rows.Scan(&id, &total, &done); err != nil {
			rows.Close()
			return nil, err
		}
		c := out[id]
		c.ChildrenTotal = total
		c.ChildrenDone = done
		out[id] = c
	}
	rows.Close()

	// checklist items: join through checklists to find items for each task.
	q = `SELECT c.task_id, COUNT(ci.id), SUM(CASE WHEN ci.done = 1 THEN 1 ELSE 0 END)
	      FROM checklists c
	      LEFT JOIN checklist_items ci ON ci.checklist_id = c.id
	      WHERE c.task_id IN (` + placeholders + `)
	      GROUP BY c.task_id`
	rows, err = r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregateCounters.checklist: %w", err)
	}
	for rows.Next() {
		var id string
		var total, done int
		if err := rows.Scan(&id, &total, &done); err != nil {
			rows.Close()
			return nil, err
		}
		c := out[id]
		c.ChecklistTotal = total
		c.ChecklistDone = done
		out[id] = c
	}
	rows.Close()

	return out, nil
}

// aggregateBlockers returns the open-blocker count per task id.
// "Open" means the blocker is not yet done.
func (r *taskRepo) aggregateBlockers(ctx context.Context, ids []string) (map[string]int, error) {
	out := make(map[string]int, len(ids))
	for _, id := range ids {
		out[id] = 0
	}
	placeholders := strings.Repeat("?, ", len(ids)-1) + "?"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	q := `SELECT d.task_id, COUNT(*)
	      FROM task_dependencies d
	      JOIN tasks dep ON dep.id = d.depends_on_task_id
	      WHERE d.task_id IN (` + placeholders + `)
	        AND dep.status != 'done' AND dep.completed_at IS NULL
	      GROUP BY d.task_id`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregateBlockers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, nil
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
			start_at = ?, end_at = ?, all_day = ?, color = ?, recurrence = ?,
			study_course_id = ?
		WHERE id = ?
	`
	// Phase 31: study_course_id added to the SET so PATCH /tasks/{id}
	// can attach a freshly-accepted study reminder. The empty-string
	// case stays valid — it clears the link (useful when the user
	// files a reminder under a project and no longer wants it to
	// appear under due_today). Per Validate(), a Task with column_id
	// set MUST also have a project; that invariant is unchanged
	// here.
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
		nullString(t.StudyCourseID),
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
// Review queue (Phase 19)
// ---------------------------------------------------------------------------
//
// One query: tasks where awaiting='human' OR status='review',
// LEFT JOIN'd with projects (NULL-safe after Phase 16 dropped the
// system Inbox). Ordered by updated_at DESC so the most recently
// submitted work shows at the top of the queue.
//
// The handler may call this on every WS event for the badge counter
// (cheap query, idx_tasks_status covers the WHERE clause).

func (r *taskRepo) ListAwaitingReview(ctx context.Context) ([]task.ReviewQueueItem, error) {
	const q = `
		SELECT t.id, t.number, t.project_id, t.parent_task_id, t.column_id, t.title, t.description,
		       t.status, t.priority, t.assignee_type, t.assignee_id, t.awaiting,
		       t.context_md, t.agent_notes, t.due_at, t.started_at, t.claimed_at, t.completed_at,
		       t.time_estimate_s, t.time_spent_s, t.position,
		       t.start_at, t.end_at, t.all_day, t.color, t.recurrence,
		       t.study_course_id,
		       t.created_at, t.updated_at,
		       COALESCE(p.name, '')  AS project_name,
		       COALESCE(p.color, '') AS project_color
		FROM tasks t
		LEFT JOIN projects p ON p.id = t.project_id
		WHERE t.awaiting = 'human' OR t.status = 'review'
		ORDER BY t.updated_at DESC, t.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("task.ListAwaitingReview: %w", err)
	}
	defer rows.Close()

	out := make([]task.ReviewQueueItem, 0)
	for rows.Next() {
		var (
			item        task.ReviewQueueItem
			projectName string
			projectCol  string
		)
		// item.Task is *Task; the address-of fields below would NPE
		// without an explicit backing struct.
		item.Task = &task.Task{}
		// scanTaskRow reads the task columns; the extra two strings are
		// tail-position so we read them after the task fields. We can't
		// share scanTaskRow directly because of the column ordering;
		// inline-scan keeps the JOIN narrow and obvious.
		var (
			projectID, parent, columnID    sql.NullString
			desc, assigneeType, assigneeID sql.NullString
			contextMD, agentNotes          sql.NullString
			due, started, claimed, compl   sql.NullString
			calStart, calEnd, color        sql.NullString
			recurrence, studyCourse        sql.NullString
			allDay                         int
			estS                           sql.NullInt64
			status, priority, awaiting     string
			created, updated               string
		)
		if err := rows.Scan(
			&item.Task.ID, &item.Task.Number, &projectID, &parent, &columnID, &item.Task.Title, &desc,
			&status, &priority, &assigneeType, &assigneeID, &awaiting,
			&contextMD, &agentNotes,
			&due, &started, &claimed, &compl,
			&estS, &item.Task.TimeSpentS, &item.Task.Position,
			&calStart, &calEnd, &allDay, &color, &recurrence,
			&studyCourse,
			&created, &updated,
			&projectName, &projectCol,
		); err != nil {
			return nil, fmt.Errorf("task.ListAwaitingReview: scan: %w", err)
		}
		t := item.Task
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
		t.StudyCourseID = studyCourse.String
		if estS.Valid {
			v := int(estS.Int64)
			t.TimeEstimateS = &v
		}
		t.CreatedAt = parseTime(created)
		t.UpdatedAt = parseTime(updated)
		item.Task = t
		item.ProjectName = projectName
		item.ProjectColor = projectCol
		out = append(out, item)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Tags (Phase 13)
// ---------------------------------------------------------------------------
//
// The `tags` table is a global label vocabulary (name is unique
// across the database, not scoped to a project). Tasks link to tags
// through the many-to-many `task_tags` join with ON DELETE CASCADE so
// removing a task or a tag cleans up the links automatically.
//
// SetTaskTags is the only write path used by the API — it's a single
// transaction (DELETE then bulk INSERT) so the user never observes a
// half-updated set. Callers (the handler) should compare the
// desired set to the current one and skip the call when nothing
// changed to avoid noise in the activity feed.

func (r *taskRepo) ListTags(ctx context.Context) ([]task.Tag, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(color, '') FROM tags ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("task.ListTags: %w", err)
	}
	defer rows.Close()
	out := make([]task.Tag, 0)
	for rows.Next() {
		var t task.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			return nil, fmt.Errorf("task.ListTags: scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *taskRepo) GetTagByID(ctx context.Context, id string) (*task.Tag, error) {
	var t task.Tag
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, COALESCE(color, '') FROM tags WHERE id = ?`, id,
	).Scan(&t.ID, &t.Name, &t.Color)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, task.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("task.GetTagByID: %w", err)
	}
	return &t, nil
}

func (r *taskRepo) CreateTag(ctx context.Context, t *task.Tag) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.ID == "" {
		t.ID = newUUID()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tags (id, name, color) VALUES (?, ?, ?)`,
		t.ID, t.Name, nullString(t.Color))
	if err != nil {
		// UNIQUE(name) violation surfaces here; callers translate to 409.
		return fmt.Errorf("task.CreateTag: %w", err)
	}
	return nil
}

func (r *taskRepo) UpdateTag(ctx context.Context, t *task.Tag) error {
	if err := t.Validate(); err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE tags SET name = ?, color = ? WHERE id = ?`,
		t.Name, nullString(t.Color), t.ID)
	if err != nil {
		return fmt.Errorf("task.UpdateTag: %w", err)
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

func (r *taskRepo) DeleteTag(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("task.DeleteTag: %w", err)
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

// ListByDueBetween returns tasks whose due_at falls within
// [from, to]. Phase 30.8: the calendar needs to render tasks
// alongside timed events. Tasks without a due_at are not returned;
// status is not filtered — the calendar renders done tasks
// differently (shaded) but it still cares about them.
func (r *taskRepo) ListByDueBetween(ctx context.Context, from, to time.Time) ([]*task.Task, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, number, project_id, parent_task_id, column_id, title, description,
		       status, priority, assignee_type, assignee_id, awaiting,
		       context_md, agent_notes, due_at, started_at, claimed_at,
		       completed_at, time_estimate_s, time_spent_s, position,
		       start_at, end_at, all_day, color, recurrence,
		       study_course_id,
		       created_at, updated_at
		FROM tasks
		WHERE due_at IS NOT NULL
		  AND due_at >= ?
		  AND due_at <= ?
		ORDER BY due_at ASC`,
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("task.ListByDueBetween: %w", err)
	}
	defer rows.Close()
	out := make([]*task.Task, 0)
	for rows.Next() {
		tr, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("task.ListByDueBetween: scan: %w", err)
		}
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task.ListByDueBetween: rows: %w", err)
	}
	return out, nil
}

// ListTagsForTask returns every tag attached to the given task,
// ordered by name. Used by the task detail endpoint; the kanban
// list endpoint uses TagsForTasks (batch) instead.
func (r *taskRepo) ListTagsForTask(ctx context.Context, taskID string) ([]task.Tag, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.name, COALESCE(t.color, '')
		FROM tags t
		JOIN task_tags tt ON tt.tag_id = t.id
		WHERE tt.task_id = ?
		ORDER BY t.name ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("task.ListTagsForTask: %w", err)
	}
	defer rows.Close()
	out := make([]task.Tag, 0)
	for rows.Next() {
		var t task.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color); err != nil {
			return nil, fmt.Errorf("task.ListTagsForTask: scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetTaskTags replaces the tag set for a task atomically.
//
// Implementation notes:
//   - The DELETE+INSERT pair runs inside a single transaction. If the
//     INSERT fails (e.g. an id that no longer exists) the DELETE is
//     rolled back, so the task keeps its previous tag set.
//   - We don't validate the tag IDs here; a bad id simply results in
//     a FK violation (ON DELETE CASCADE keeps this table clean).
//     Callers should pre-validate when surfacing a clean error to the
//     UI (the handler does this).
//   - De-dup is implicit: the join table's PRIMARY KEY (task_id, tag_id)
//     makes a second insert of the same pair a no-op via INSERT OR
//     IGNORE (modernc/sqlite honours it for UNIQUE/PK violations).
func (r *taskRepo) SetTaskTags(ctx context.Context, taskID string, tagIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("task.SetTaskTags: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM task_tags WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("task.SetTaskTags: delete: %w", err)
	}
	for _, id := range tagIDs {
		if id == "" {
			continue
		}
		if _, err = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO task_tags (task_id, tag_id) VALUES (?, ?)`,
			taskID, id); err != nil {
			return fmt.Errorf("task.SetTaskTags: insert: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("task.SetTaskTags: commit: %w", err)
	}
	return nil
}

// TagsForTasks returns the tag set for every task in taskIDs in a
// single round-trip. Result is keyed by task id; every input id
// appears in the map (tasks with no tags get an empty slice, not a
// missing key). This makes the kanban enrichment loop safe — code
// can iterate `range got` without nil checks per card.
func (r *taskRepo) TagsForTasks(ctx context.Context, taskIDs []string) (map[string][]task.Tag, error) {
	out := make(map[string][]task.Tag, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	// Pre-populate so tasks without tags still surface as empty
	// slices (not missing keys). Saves the caller from having to
	// know which task ids are in the input.
	for _, id := range taskIDs {
		out[id] = []task.Tag{}
	}
	// Build a "?, ?, ?" placeholder string for the IN clause.
	placeholders := strings.Repeat("?, ", len(taskIDs)-1) + "?"
	args := make([]any, 0, len(taskIDs))
	for _, id := range taskIDs {
		args = append(args, id)
	}
	q := `
		SELECT tt.task_id, t.id, t.name, COALESCE(t.color, '')
		FROM task_tags tt
		JOIN tags t ON t.id = tt.tag_id
		WHERE tt.task_id IN (` + placeholders + `)
		ORDER BY tt.task_id ASC, t.name ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("task.TagsForTasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			taskID string
			t      task.Tag
		)
		if err := rows.Scan(&taskID, &t.ID, &t.Name, &t.Color); err != nil {
			return nil, fmt.Errorf("task.TagsForTasks: scan: %w", err)
		}
		out[taskID] = append(out[taskID], t)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Task dependencies (Phase 15)
// ---------------------------------------------------------------------------
//
// The dependency graph lives in `task_dependencies(task_id, depends_on)`.
// We do the cycle check at the service layer (DFS over the existing graph);
// the repo just owns the SQL.

func (r *taskRepo) AddDependency(ctx context.Context, taskID, dependsOnID string) error {
	if taskID == "" || dependsOnID == "" {
		return task.ErrInvalidInput
	}
	if taskID == dependsOnID {
		return task.ErrSelfDependency
	}
	// Make sure both rows exist; we want a clean ErrNotFound, not a
	// silent FK violation.
	for _, id := range []string{taskID, dependsOnID} {
		var exists int
		if err := r.db.QueryRowContext(ctx,
			`SELECT 1 FROM tasks WHERE id = ?`, id,
		).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return task.ErrNotFound
			}
			return fmt.Errorf("task.AddDependency: lookup %s: %w", id, err)
		}
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO task_dependencies(task_id, depends_on_task_id) VALUES (?, ?)`,
		taskID, dependsOnID,
	); err != nil {
		// Idempotent: a duplicate INSERT fails with UNIQUE constraint.
		// Detect that case and report ErrDependencyExists so the
		// service can stay quiet about it.
		if isUniqueViolation(err) {
			return task.ErrDependencyExists
		}
		return fmt.Errorf("task.AddDependency: %w", err)
	}
	return nil
}

// RemoveDependency deletes one edge. Missing edges are a no-op
// (DELETE returns 0 rows; we treat that as success so the service
// can be idempotent).
func (r *taskRepo) RemoveDependency(ctx context.Context, taskID, dependsOnID string) error {
	if taskID == "" || dependsOnID == "" {
		return task.ErrInvalidInput
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM task_dependencies WHERE task_id = ? AND depends_on_task_id = ?`,
		taskID, dependsOnID,
	)
	if err != nil {
		return fmt.Errorf("task.RemoveDependency: %w", err)
	}
	_, _ = res.RowsAffected() // 0 is fine
	return nil
}

// SetTaskDependencies replaces the task's full blocker set
// transactionally (DELETE then INSERT). Empty input clears every edge.
//
// IMPORTANT: we don't run the cycle check here — that belongs to the
// service. The service builds the proposed target set, runs DFS, and
// only then calls this method. Using SetTaskDependencies in a
// different order (e.g. directly from a handler) would open the door
// to cycles.
func (r *taskRepo) SetTaskDependencies(ctx context.Context, taskID string, dependsOnIDs []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("task.SetTaskDependencies: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_dependencies WHERE task_id = ?`, taskID,
	); err != nil {
		return fmt.Errorf("task.SetTaskDependencies: delete: %w", err)
	}
	for _, dep := range dependsOnIDs {
		if dep == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_dependencies(task_id, depends_on_task_id) VALUES (?, ?)`,
			taskID, dep,
		); err != nil {
			return fmt.Errorf("task.SetTaskDependencies: insert %s: %w", dep, err)
		}
	}
	return tx.Commit()
}

// Blockers returns the edges pointing INTO taskID (i.e. tasks taskID
// depends on). All edges, including edges to done tasks — the caller
// filters when it cares about "still open".
func (r *taskRepo) Blockers(ctx context.Context, taskID string) ([]task.BlockerRow, error) {
	const q = `
		SELECT dep.id, dep.title, dep.status, dep.completed_at
		FROM task_dependencies d
		JOIN tasks dep ON dep.id = d.depends_on_task_id
		WHERE d.task_id = ?
		ORDER BY dep.title ASC`
	rows, err := r.db.QueryContext(ctx, q, taskID)
	if err != nil {
		return nil, fmt.Errorf("task.Blockers: %w", err)
	}
	defer rows.Close()
	out := make([]task.BlockerRow, 0)
	for rows.Next() {
		var (
			b         task.BlockerRow
			completed sql.NullString
		)
		if err := rows.Scan(&b.BlockerID, &b.Title, &b.Status, &completed); err != nil {
			return nil, fmt.Errorf("task.Blockers: scan: %w", err)
		}
		// status='done' OR a non-null completed_at ⇒ satisfied.
		if b.Status == task.StatusDone || completed.Valid {
			b.Done = true
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BlockersForTasks is the batch form of Blockers (Phase 28.22): one
// query for many task ids, keyed by task id. Pre-populates an empty
// slice per input id so untagged/unblocked tasks read as "no blockers"
// rather than "missing key".
func (r *taskRepo) BlockersForTasks(ctx context.Context, taskIDs []string) (map[string][]task.BlockerRow, error) {
	out := make(map[string][]task.BlockerRow, len(taskIDs))
	for _, id := range taskIDs {
		out[id] = make([]task.BlockerRow, 0)
	}
	if len(taskIDs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?, ", len(taskIDs)-1) + "?"
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		args[i] = id
	}
	q := `SELECT d.task_id, dep.id, dep.title, dep.status, dep.completed_at
	      FROM task_dependencies d
	      JOIN tasks dep ON dep.id = d.depends_on_task_id
	      WHERE d.task_id IN (` + placeholders + `)
	      ORDER BY dep.title ASC`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("task.BlockersForTasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			taskID    string
			b         task.BlockerRow
			completed sql.NullString
		)
		if err := rows.Scan(&taskID, &b.BlockerID, &b.Title, &b.Status, &completed); err != nil {
			return nil, fmt.Errorf("task.BlockersForTasks: scan: %w", err)
		}
		if b.Status == task.StatusDone || completed.Valid {
			b.Done = true
		}
		out[taskID] = append(out[taskID], b)
	}
	return out, rows.Err()
}

// Dependents returns the ids of tasks that depend on taskID
// (reverse lookup). Returns []string of just the IDs — the caller
// can hydrate the rows via GetByID if it needs titles.
func (r *taskRepo) Dependents(ctx context.Context, taskID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT task_id FROM task_dependencies WHERE depends_on_task_id = ? ORDER BY task_id ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("task.Dependents: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("task.Dependents: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// (isUniqueViolation lives in helpers.go — modernc doesn't expose a
// typed error for UNIQUE failures, so the helper just does a substring
// match. We reuse it from the AddDependency dedupe path.)

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
SELECT id, number, project_id, parent_task_id, column_id, title, description,
       status, priority, assignee_type, assignee_id, awaiting,
       context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
       time_estimate_s, time_spent_s, position,
       start_at, end_at, all_day, color, recurrence,
       study_course_id,
       created_at, updated_at
FROM tasks
`

// scanTask reads one row (sql.Row) into a Task.
//
// Phase 16: project_id is scanned via sql.NullString because the column
// became nullable. NULL → "" (the "Inbox" representation in Go).
// Phase 23.3: recurrence is scanned the same way (NULL → "").
// Phase 31: study_course_id follows the same convention — empty
// string is the unmarked case, NULL is reserved for "course was
// deleted" (the task remains as a plain task in either case).
func scanTask(row *sql.Row) (*task.Task, error) {
	var t task.Task
	var (
		projectID, parent, columnID    sql.NullString
		desc, assigneeType, assigneeID sql.NullString
		contextMD, agentNotes          sql.NullString
		due, started, claimed, compl   sql.NullString
		calStart, calEnd, color        sql.NullString
		recurrence, studyCourse        sql.NullString
		allDay                         int
		estS                           sql.NullInt64
		status, priority, awaiting     string
		created, updated               string
	)
	err := row.Scan(
		&t.ID, &t.Number, &projectID, &parent, &columnID, &t.Title, &desc,
		&status, &priority, &assigneeType, &assigneeID, &awaiting,
		&contextMD, &agentNotes,
		&due, &started, &claimed, &compl,
		&estS, &t.TimeSpentS, &t.Position,
		&calStart, &calEnd, &allDay, &color, &recurrence,
		&studyCourse,
		&created, &updated,
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
	t.StudyCourseID = studyCourse.String
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
		SELECT id, number, project_id, parent_task_id, column_id, title, description,
		       status, priority, assignee_type, assignee_id, awaiting,
		       context_md, agent_notes, due_at, started_at, claimed_at, completed_at,
		       time_estimate_s, time_spent_s, position,
		       start_at, end_at, all_day, color, recurrence,
		       study_course_id,
		       created_at, updated_at
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
		recurrence, studyCourse        sql.NullString
		allDay                         int
		estS                           sql.NullInt64
		status, priority, awaiting     string
		created, updated               string
	)
	err := rows.Scan(
		&t.ID, &t.Number, &projectID, &parent, &columnID, &t.Title, &desc,
		&status, &priority, &assigneeType, &assigneeID, &awaiting,
		&contextMD, &agentNotes,
		&due, &started, &claimed, &compl,
		&estS, &t.TimeSpentS, &t.Position,
		&calStart, &calEnd, &allDay, &color, &recurrence,
		&studyCourse,
		&created, &updated,
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
	t.StudyCourseID = studyCourse.String
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

// TitlesByIDs returns id→title for every requested task in a single
// round-trip (Phase 27.9). Missing ids are absent from the map; the
// service treats "no key" as "task deleted between writes", falling
// back to a slice of the id. Empty input → empty map.
//
// We don't pre-populate here — the contract differs from TagsForTasks:
// an absent key in this case is meaningful (gone) rather than empty
// data. The caller iterates its own input slice, not the result map.
func (r *taskRepo) TitlesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?, ", len(ids)-1) + "?"
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	q := `SELECT id, title FROM tasks WHERE id IN (` + placeholders + `)`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("task.TitlesByIDs: query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, fmt.Errorf("task.TitlesByIDs: scan: %w", err)
		}
		out[id] = title
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("task.TitlesByIDs: rows: %w", err)
	}
	return out, nil
}
