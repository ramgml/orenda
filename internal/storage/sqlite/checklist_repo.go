package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/checklist"
)

// checklistRepo implements the Checklist + ChecklistItem persistence.
// Both tables exist in 001_init.sql and cascade-delete with their
// parent (task or checklist respectively), so cleanup is automatic.
type checklistRepo struct {
	db *sql.DB
}

// NewChecklistRepository returns a ChecklistRepository backed by db.
func NewChecklistRepository(db *sql.DB) *checklistRepo {
	return &checklistRepo{db: db}
}

func (r *checklistRepo) AddList(ctx context.Context, taskID, title string) (*checklist.Checklist, error) {
	if taskID == "" || title == "" {
		return nil, errors.New("checklist.AddList: empty taskID or title")
	}
	var pos int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM checklists WHERE task_id = ?`,
		taskID).Scan(&pos); err != nil {
		return nil, fmt.Errorf("checklist.AddList: peek position: %w", err)
	}
	id := newUUID()
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO checklists (id, task_id, title, position) VALUES (?, ?, ?, ?)`,
		id, taskID, title, pos); err != nil {
		return nil, fmt.Errorf("checklist.AddList: %w", err)
	}
	return &checklist.Checklist{ID: id, TaskID: taskID, Title: title, Position: pos}, nil
}

func (r *checklistRepo) ListLists(ctx context.Context, taskID string) ([]*checklist.Checklist, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, task_id, title, position FROM checklists
		 WHERE task_id = ? ORDER BY position ASC`, taskID)
	if err != nil {
		return nil, fmt.Errorf("checklist.ListLists: %w", err)
	}
	defer rows.Close()
	out := make([]*checklist.Checklist, 0)
	for rows.Next() {
		var c checklist.Checklist
		if err := rows.Scan(&c.ID, &c.TaskID, &c.Title, &c.Position); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *checklistRepo) DeleteList(ctx context.Context, listID string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM checklists WHERE id = ?`, listID); err != nil {
		return fmt.Errorf("checklist.DeleteList: %w", err)
	}
	return nil
}

func (r *checklistRepo) AddItem(ctx context.Context, listID, title string) (*checklist.Item, error) {
	if listID == "" || title == "" {
		return nil, errors.New("checklist.AddItem: empty listID or title")
	}
	var pos int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM checklist_items WHERE checklist_id = ?`,
		listID).Scan(&pos); err != nil {
		return nil, fmt.Errorf("checklist.AddItem: peek position: %w", err)
	}
	id := newUUID()
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO checklist_items (id, checklist_id, title, done, position)
		 VALUES (?, ?, ?, 0, ?)`,
		id, listID, title, pos); err != nil {
		return nil, fmt.Errorf("checklist.AddItem: %w", err)
	}
	return &checklist.Item{ID: id, ChecklistID: listID, Title: title, Done: false, Position: pos}, nil
}

func (r *checklistRepo) ListItems(ctx context.Context, listID string) ([]*checklist.Item, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, checklist_id, title, done, position
		 FROM checklist_items WHERE checklist_id = ?
		 ORDER BY position ASC`, listID)
	if err != nil {
		return nil, fmt.Errorf("checklist.ListItems: %w", err)
	}
	defer rows.Close()
	out := make([]*checklist.Item, 0)
	for rows.Next() {
		var (
			it      checklist.Item
			doneInt int
		)
		if err := rows.Scan(&it.ID, &it.ChecklistID, &it.Title, &doneInt, &it.Position); err != nil {
			return nil, err
		}
		it.Done = doneInt != 0
		out = append(out, &it)
	}
	return out, rows.Err()
}

func (r *checklistRepo) UpdateItem(ctx context.Context, itemID string, done *bool, title *string) error {
	// We support partial updates: callers pass nil for the fields
	// they don't want to change. Build a tiny dynamic query so we
	// don't have to repeat SQL for every combination.
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
		return nil // no-op
	}
	args = append(args, itemID)
	q := "UPDATE checklist_items SET id = id" + sets + " WHERE id = ?"
	if _, err := r.db.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("checklist.UpdateItem: %w", err)
	}
	return nil
}

func (r *checklistRepo) DeleteItem(ctx context.Context, itemID string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM checklist_items WHERE id = ?`, itemID); err != nil {
		return fmt.Errorf("checklist.DeleteItem: %w", err)
	}
	return nil
}
