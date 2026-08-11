// Package sqlite — Project/Board/Column repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ramgml/orenda/internal/domain/project"
)

// projectRepo persists projects, boards and columns.
type projectRepo struct {
	db *sql.DB
}

// NewProjectRepository returns a project.Repository backed by db.
func NewProjectRepository(db *sql.DB) project.Repository {
	return &projectRepo{db: db}
}

func (r *projectRepo) CreateProject(ctx context.Context, p *project.Project) (*project.Project, []*project.Board, []*project.Column, error) {
	if err := p.Validate(); err != nil {
		return nil, nil, nil, err
	}
	if p.ID == "" {
		p.ID = newUUID()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("project.CreateProject: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const insProject = `
		INSERT INTO projects (id, name, color, description, owner_id, archived,
		                     created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, datetime('now'), datetime('now'))
	`
	if _, err := tx.ExecContext(ctx, insProject,
		p.ID, p.Name, p.Color, p.Description, p.OwnerID,
	); err != nil {
		return nil, nil, nil, fmt.Errorf("project.CreateProject: insert project: %w", err)
	}

	// Default board.
	boardID := newUUID()
	const insBoard = `
		INSERT INTO boards (id, project_id, name, position, created_at)
		VALUES (?, ?, 'Main', 0, datetime('now'))
	`
	if _, err := tx.ExecContext(ctx, insBoard, boardID, p.ID); err != nil {
		return nil, nil, nil, fmt.Errorf("project.CreateProject: insert board: %w", err)
	}

	// Default columns with evenly spaced positions so future inserts can
	// average between them without renumbering.
	columns := make([]*project.Column, 0, len(project.DefaultColumns))
	const insColumn = `
		INSERT INTO columns (id, board_id, name, position)
		VALUES (?, ?, ?, ?)
	`
	for i, name := range project.DefaultColumns {
		colID := newUUID()
		position := float64(i) * 1024
		if _, err := tx.ExecContext(ctx, insColumn, colID, boardID, name, position); err != nil {
			return nil, nil, nil, fmt.Errorf("project.CreateProject: insert column %q: %w", name, err)
		}
		columns = append(columns, &project.Column{
			ID:       colID,
			BoardID:  boardID,
			Name:     name,
			Position: position,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, nil, fmt.Errorf("project.CreateProject: commit: %w", err)
	}

	// Reload to populate timestamps.
	created, err := r.GetProject(ctx, p.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	boards := []*project.Board{{
		ID: boardID, ProjectID: p.ID, Name: "Main", Position: 0,
	}}
	return created, boards, columns, nil
}

func (r *projectRepo) GetProject(ctx context.Context, id string) (*project.Project, error) {
	const q = `
		SELECT id, name, color, description, owner_id, archived, created_at, updated_at
		FROM projects WHERE id = ?
	`
	row := r.db.QueryRowContext(ctx, q, id)
	var (
		p    project.Project
		desc sql.NullString
		arch int
		cAt  string
		uAt  string
	)
	err := row.Scan(&p.ID, &p.Name, &p.Color, &desc, &p.OwnerID, &arch, &cAt, &uAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, project.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project.GetProject: %w", err)
	}
	p.Description = desc.String
	p.Archived = arch != 0
	p.CreatedAt = parseTime(cAt)
	p.UpdatedAt = parseTime(uAt)
	return &p, nil
}

func (r *projectRepo) ListProjects(ctx context.Context, ownerID string) ([]*project.Project, error) {
	// Phase 11 is single-owner, so every project (including the
	// system-owned Inbox) is visible to the caller. The ownerID arg
	// is kept for API compatibility with handlers that still pass it,
	// but the query no longer filters by it — without this, the
	// Inbox project (owned by the system placeholder user) was
	// invisible to the frontend's project list.
	_ = ownerID
	const q = `
		SELECT id, name, color, description, owner_id, archived, created_at, updated_at
		FROM projects
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("project.ListProjects: %w", err)
	}
	defer rows.Close()

	out := make([]*project.Project, 0)
	for rows.Next() {
		var (
			p    project.Project
			desc sql.NullString
			arch int
			cAt  string
			uAt  string
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.Color, &desc, &p.OwnerID, &arch, &cAt, &uAt); err != nil {
			return nil, fmt.Errorf("project.ListProjects: scan: %w", err)
		}
		p.Description = desc.String
		p.Archived = arch != 0
		p.CreatedAt = parseTime(cAt)
		p.UpdatedAt = parseTime(uAt)
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *projectRepo) UpdateProject(ctx context.Context, p *project.Project) error {
	if err := p.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE projects
		SET name = ?, color = ?, description = ?, archived = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q, p.Name, p.Color, p.Description, boolToInt(p.Archived), p.ID)
	if err != nil {
		return fmt.Errorf("project.UpdateProject: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("project.UpdateProject: rows: %w", err)
	}
	if n == 0 {
		return project.ErrNotFound
	}
	got, err := r.GetProject(ctx, p.ID)
	if err != nil {
		return err
	}
	*p = *got
	return nil
}

func (r *projectRepo) DeleteProject(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("project.DeleteProject: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return project.ErrNotFound
	}
	return nil
}

func (r *projectRepo) GetBoard(ctx context.Context, projectID string) (*project.Board, []*project.Column, error) {
	// Phase 1: single board per project; pick the lowest-position board.
	const qBoard = `
		SELECT id, project_id, name, position, created_at
		FROM boards WHERE project_id = ?
		ORDER BY position ASC LIMIT 1
	`
	var (
		b   project.Board
		cAt string
	)
	err := r.db.QueryRowContext(ctx, qBoard, projectID).
		Scan(&b.ID, &b.ProjectID, &b.Name, &b.Position, &cAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, project.ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("project.GetBoard: %w", err)
	}
	b.CreatedAt = parseTime(cAt)

	const qCols = `
		SELECT id, board_id, name, position, wip_limit, color
		FROM columns WHERE board_id = ?
		ORDER BY position ASC
	`
	rows, err := r.db.QueryContext(ctx, qCols, b.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("project.GetBoard: list columns: %w", err)
	}
	defer rows.Close()

	cols := []*project.Column{}
	for rows.Next() {
		var (
			col   project.Column
			wip   sql.NullInt64
			color sql.NullString
		)
		if err := rows.Scan(&col.ID, &col.BoardID, &col.Name, &col.Position, &wip, &color); err != nil {
			return nil, nil, fmt.Errorf("project.GetBoard: scan column: %w", err)
		}
		if wip.Valid {
			v := int(wip.Int64)
			col.WIPLimit = &v
		}
		col.Color = color.String
		cols = append(cols, &col)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return &b, cols, nil
}

// boolToInt converts a bool to 0/1 for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpdateColumn persists mutable fields (name, position, wip_limit, color)
// of a kanban column. WIPLimit = nil → store NULL (no limit).
//
// Returns project.ErrNotFound when no row matches the id.
func (r *projectRepo) UpdateColumn(ctx context.Context, c *project.Column) error {
	if c.ID == "" {
		return project.ErrInvalidInput
	}
	var wipLimit sql.NullInt64
	if c.WIPLimit != nil {
		wipLimit = sql.NullInt64{Int64: int64(*c.WIPLimit), Valid: true}
	}
	var color sql.NullString
	if c.Color != "" {
		color = sql.NullString{String: c.Color, Valid: true}
	}
	const q = `
		UPDATE columns
		SET name = ?, position = ?, wip_limit = ?, color = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q, c.Name, c.Position, wipLimit, color, c.ID)
	if err != nil {
		return fmt.Errorf("project.UpdateColumn: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("project.UpdateColumn: rows: %w", err)
	}
	if n == 0 {
		return project.ErrNotFound
	}
	return nil
}

// DeleteColumn removes a column by id. Returns ErrNotFound when no
// row matches and ErrColumnNotEmpty when at least one task still
// references the column via tasks.column_id.
//
// We check the task count BEFORE issuing the DELETE because the FK
// tasks.column_id is declared as ON DELETE SET NULL — silently
// unlinking tasks from their column would lose board context without
// the user noticing. Rejecting with 422 forces the caller (UI or API
// client) to move tasks out first.
func (r *projectRepo) DeleteColumn(ctx context.Context, id string) error {
	if id == "" {
		return project.ErrInvalidInput
	}

	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE column_id = ?`, id).Scan(&count)
	if err != nil {
		return fmt.Errorf("project.DeleteColumn: count tasks: %w", err)
	}
	if count > 0 {
		return project.ErrColumnNotEmpty
	}

	res, err := r.db.ExecContext(ctx, `DELETE FROM columns WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("project.DeleteColumn: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("project.DeleteColumn: rows: %w", err)
	}
	if n == 0 {
		return project.ErrNotFound
	}
	return nil
}

// GetColumn fetches a single column by id, returning ErrNotFound when missing.
func (r *projectRepo) GetColumn(ctx context.Context, id string) (*project.Column, error) {
	const q = `
		SELECT id, board_id, name, position, wip_limit, color
		FROM columns WHERE id = ?
	`
	var (
		col   project.Column
		wip   sql.NullInt64
		color sql.NullString
	)
	err := r.db.QueryRowContext(ctx, q, id).
		Scan(&col.ID, &col.BoardID, &col.Name, &col.Position, &wip, &color)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, project.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project.GetColumn: %w", err)
	}
	if wip.Valid {
		v := int(wip.Int64)
		col.WIPLimit = &v
	}
	col.Color = color.String
	return &col, nil
}

// CreateColumn appends a new column to the project's (single) board. The
// position is chosen as max(existing positions) + 1024 so the new column
// always lands at the end of the board and reordering a single column
// stays a midpoint operation. Name and (optional) WIP/Color come from c;
// c.ID, BoardID, and Position are filled in by the repo.
//
// Returns project.ErrNotFound when the project has no board (project
// missing or freshly seeded). Returns ErrInvalidInput when c.Name is empty.
func (r *projectRepo) CreateColumn(ctx context.Context, projectID string, c *project.Column) (*project.Column, error) {
	if c == nil || c.Name == "" {
		return nil, project.ErrInvalidInput
	}
	if c.ID == "" {
		c.ID = newUUID()
	}

	board, _, err := r.GetBoard(ctx, projectID)
	if err != nil {
		return nil, err
	}

	// Pick the next position. Default 0 means "the board is empty", which
	// can't happen for boards created by CreateProject (which seeds 5
	// default columns) but is a safe fallback for a hypothetical manual
	// board.
	const maxPosQ = `SELECT COALESCE(MAX(position), 0) FROM columns WHERE board_id = ?`
	var maxPos float64
	if err := r.db.QueryRowContext(ctx, maxPosQ, board.ID).Scan(&maxPos); err != nil {
		return nil, fmt.Errorf("project.CreateColumn: max position: %w", err)
	}
	c.BoardID = board.ID
	c.Position = maxPos + 1024

	var wipLimit sql.NullInt64
	if c.WIPLimit != nil {
		wipLimit = sql.NullInt64{Int64: int64(*c.WIPLimit), Valid: true}
	}
	var color sql.NullString
	if c.Color != "" {
		color = sql.NullString{String: c.Color, Valid: true}
	}

	const ins = `
		INSERT INTO columns (id, board_id, name, position, wip_limit, color)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	if _, err := r.db.ExecContext(ctx, ins,
		c.ID, c.BoardID, c.Name, c.Position, wipLimit, color,
	); err != nil {
		return nil, fmt.Errorf("project.CreateColumn: insert: %w", err)
	}
	return c, nil
}
