// Package sqlite — Project/Board/Column repository implementation.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

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

	// number comes from the project_number_seq high-watermark, not from
	// MAX(projects.number): a MAX+1 would re-issue the newest project's
	// number after that project is deleted, and a "P7" reference in a
	// commit message, branch name or PR title must keep pointing at
	// the same project forever. The watermark UPDATE...RETURNING and the
	// INSERT share one transaction, so the draw is atomic and the
	// sequence can never run backwards.
	var number int
	if err := tx.QueryRowContext(ctx,
		`UPDATE project_number_seq SET next = next + 1 WHERE id = 1 RETURNING next - 1`,
	).Scan(&number); err != nil {
		return nil, nil, nil, fmt.Errorf("project.CreateProject: draw number: %w", err)
	}

	// wiki_slug is nullable; normalize "" to NULL on insert so the FK
	// semantics line up with the rest of the codebase.
	const insProject = `
		INSERT INTO projects (id, number, name, color, description, wiki_slug, owner_id, archived,
		                     created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, datetime('now'), datetime('now'))
	`
	if _, err := tx.ExecContext(ctx, insProject,
		p.ID, number, p.Name, p.Color, p.Description, nullString(p.WikiSlug), p.OwnerID,
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
	//
	// Phase 27.8: we also seed the new `status` machine key in the
	// same INSERT so the column is usable for the Phase 27.8
	// invariant without a separate round-trip. The five canonical
	// column names are already valid status keys (lowercase form).
	columns := make([]*project.Column, 0, len(project.DefaultColumns))
	const insColumn = `
		INSERT INTO columns (id, board_id, name, position, status)
		VALUES (?, ?, ?, ?, ?)
	`
	for i, name := range project.DefaultColumns {
		colID := newUUID()
		position := float64(i) * 1024
		if _, err := tx.ExecContext(ctx, insColumn, colID, boardID, name, position, name); err != nil {
			return nil, nil, nil, fmt.Errorf("project.CreateProject: insert column %q: %w", name, err)
		}
		columns = append(columns, &project.Column{
			ID:       colID,
			BoardID:  boardID,
			Name:     name,
			Position: position,
			Status:   name, // Phase 27.8: default columns share name+status.
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, nil, fmt.Errorf("project.CreateProject: commit: %w", err)
	}

	// Reload to populate timestamps and number.
	created, err := r.GetProject(ctx, p.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	*p = *created
	boards := []*project.Board{{
		ID: boardID, ProjectID: p.ID, Name: "Main", Position: 0,
	}}
	return created, boards, columns, nil
}

func (r *projectRepo) GetProject(ctx context.Context, id string) (*project.Project, error) {
	const q = `
		SELECT id, number, name, color, description, wiki_slug, owner_id, archived, created_at, updated_at
		FROM projects WHERE id = ?
	`
	row := r.db.QueryRowContext(ctx, q, id)
	var (
		p    project.Project
		desc sql.NullString
		wiki sql.NullString
		arch int
		cAt  string
		uAt  string
	)
	err := row.Scan(&p.ID, &p.Number, &p.Name, &p.Color, &desc, &wiki, &p.OwnerID, &arch, &cAt, &uAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, project.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project.GetProject: %w", err)
	}
	p.Description = desc.String
	p.WikiSlug = wiki.String
	p.Archived = arch != 0
	p.CreatedAt = parseTime(cAt)
	p.UpdatedAt = parseTime(uAt)
	return &p, nil
}

// GetByNumber resolves the human-readable "P<N>" reference to a project.
// The UNIQUE index idx_projects_number (migration 036) makes this an
// index point lookup.
func (r *projectRepo) GetByNumber(ctx context.Context, number int) (*project.Project, error) {
	const q = `
		SELECT id, number, name, color, description, wiki_slug, owner_id, archived, created_at, updated_at
		FROM projects WHERE number = ?
	`
	row := r.db.QueryRowContext(ctx, q, number)
	var (
		p    project.Project
		desc sql.NullString
		wiki sql.NullString
		arch int
		cAt  string
		uAt  string
	)
	err := row.Scan(&p.ID, &p.Number, &p.Name, &p.Color, &desc, &wiki, &p.OwnerID, &arch, &cAt, &uAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, project.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project.GetByNumber: %w", err)
	}
	p.Description = desc.String
	p.WikiSlug = wiki.String
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
		SELECT id, number, name, color, description, wiki_slug, owner_id, archived, created_at, updated_at
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
			wiki sql.NullString
			arch int
			cAt  string
			uAt  string
		)
		if err := rows.Scan(&p.ID, &p.Number, &p.Name, &p.Color, &desc, &wiki, &p.OwnerID, &arch, &cAt, &uAt); err != nil {
			return nil, fmt.Errorf("project.ListProjects: scan: %w", err)
		}
		p.Description = desc.String
		p.WikiSlug = wiki.String
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
		SET name = ?, color = ?, description = ?, wiki_slug = ?, archived = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q, p.Name, p.Color, p.Description, nullString(p.WikiSlug), boolToInt(p.Archived), p.ID)
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
		SELECT id, board_id, name, position, wip_limit, color,
		       COALESCE(status, '')
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
		if err := rows.Scan(&col.ID, &col.BoardID, &col.Name, &col.Position, &wip, &color, &col.Status); err != nil {
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
	// Phase 30.14: nullable status (machine key). UNIQUE(board_id, status)
	// index will surface a duplicate as a constraint error — handlers
	// translate that into a 409.
	var status sql.NullString
	if c.Status != "" {
		status = sql.NullString{String: c.Status, Valid: true}
	}
	const q = `
		UPDATE columns
		SET name = ?, position = ?, wip_limit = ?, color = ?, status = ?
		WHERE id = ?
	`
	res, err := r.db.ExecContext(ctx, q, c.Name, c.Position, wipLimit, color, status, c.ID)
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
//
// Phase 16 + 23: the column is joined with its board so the caller
// gets the owning project_id in one round-trip. The Service.Move
// needs this to (a) file an Inbox card under the project of the
// column it's dropped onto and (b) surface a human-friendly project
// id in WIP-limit error messages if we ever decide to.
func (r *projectRepo) GetColumn(ctx context.Context, id string) (*project.Column, error) {
	const q = `
		SELECT c.id, c.board_id, c.name, c.position, c.wip_limit, c.color,
		       c.status, b.project_id
		FROM columns c
		JOIN boards b ON b.id = c.board_id
		WHERE c.id = ?
	`
	var (
		col    project.Column
		wip    sql.NullInt64
		color  sql.NullString
		status sql.NullString
	)
	err := r.db.QueryRowContext(ctx, q, id).
		Scan(&col.ID, &col.BoardID, &col.Name, &col.Position, &wip, &color, &status, &col.ProjectID)
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
	col.Status = status.String
	return &col, nil
}

// slugifyColumnStatus mirrors the slugify rule used by the
// migration-020 backfill so freshly inserted columns land on the same
// canonical machine keys as old boards. The five default column
// names (`backlog`, `todo`, `in_progress`, `review`, `done`) keep
// their lowercase form verbatim — agent flow hard-codes those.
// Custom names are lowercased, non-[a-z0-9] is replaced with '_', and
// runs of '_' collapse.
func slugifyColumnStatus(name string) string {
	switch name {
	case "backlog", "todo", "in_progress", "review", "done":
		return name
	}
	var b strings.Builder
	b.Grow(len(name))
	prevUnderscore := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			prevUnderscore = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	out := b.String()
	out = strings.Trim(out, "_")
	if out == "" {
		return "custom"
	}
	return out
}

// uniqueSlugifiedColumnStatus returns a slugified machine key for
// `name` that doesn't collide with any existing column.status on the
// given board. The UNIQUE(board_id, status) index requires it.
func uniqueSlugifiedColumnStatus(ctx context.Context, db *sql.DB, boardID, name string) string {
	base := slugifyColumnStatus(name)
	const q = `SELECT 1 FROM columns WHERE board_id = ? AND status = ? LIMIT 1`
	var dummy int
	err := db.QueryRowContext(ctx, q, boardID, base).Scan(&dummy)
	if err == sql.ErrNoRows {
		return base
	}
	// Collision: append _2, _3, … until we find a free slot.
	for n := 2; n < 1000; n++ {
		candidate := fmt.Sprintf("%s_%d", base, n)
		err = db.QueryRowContext(ctx, q, boardID, candidate).Scan(&dummy)
		if err == sql.ErrNoRows {
			return candidate
		}
	}
	// Pathological: 1000+ columns on the same board with the same slug.
	// Fall through to a deterministic suffix.
	return fmt.Sprintf("%s_%s", base, uuid.NewString()[:6])
}

// CreateColumn appends a new column to the project's (single) board. The
// position is chosen as max(existing positions) + 1024 so the new column
// always lands at the end of the board and reordering a single column
// stays a midpoint operation. Name and (optional) WIP/Color come from c;
// c.ID, BoardID, and Position are filled in by the repo.
//
// Phase 30.14: status (machine key). When c.Status is empty, a stable
// machine key is slugified from Name (matching the migration-020
// backfill so existing tools keep working). When the slug collides
// with an already-existing status on this board, "_2", "_3", … is
// appended (matching the migration's dedup rule).
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

	// Resolve the machine key. Hand-crafted columns arrive with the
	// status field already filled in (after the handler validates the
	// regex). Otherwise slugify from Name with the same rules as
	// migration 020.
	if c.Status == "" {
		c.Status = uniqueSlugifiedColumnStatus(ctx, r.db, board.ID, c.Name)
	}

	const ins = `
		INSERT INTO columns (id, board_id, name, position, wip_limit, color, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	if _, err := r.db.ExecContext(ctx, ins,
		c.ID, c.BoardID, c.Name, c.Position, wipLimit, color, sql.NullString{String: c.Status, Valid: c.Status != ""},
	); err != nil {
		return nil, fmt.Errorf("project.CreateColumn: insert: %w", err)
	}
	return c, nil
}

// FindColumnByStatus returns the column on the (single) board of the
// given project whose status matches the supplied machine key. The
// status lookup joins through `boards` to scope by project — Phase 1
// uses a single board per project, but the schema allows more.
func (r *projectRepo) FindColumnByStatus(ctx context.Context, projectID, status string) (*project.Column, error) {
	if status == "" {
		return nil, project.ErrNotFound
	}
	const q = `
		SELECT c.id, c.board_id, c.name, c.position, c.wip_limit, c.color,
		       COALESCE(c.status, '')
		FROM columns c
		JOIN boards b ON b.id = c.board_id
		WHERE b.project_id = ? AND c.status = ?
		LIMIT 1
	`
	var (
		col   project.Column
		wip   sql.NullInt64
		color sql.NullString
	)
	err := r.db.QueryRowContext(ctx, q, projectID, status).Scan(
		&col.ID, &col.BoardID, &col.Name, &col.Position, &wip, &color, &col.Status,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, project.ErrNotFound
		}
		return nil, fmt.Errorf("project.FindColumnByStatus: %w", err)
	}
	col.ProjectID = projectID
	if wip.Valid {
		v := int(wip.Int64)
		col.WIPLimit = &v
	}
	if color.Valid {
		col.Color = color.String
	}
	return &col, nil
}
