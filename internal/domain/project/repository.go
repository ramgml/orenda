package project

import "context"

// Repository persists and retrieves Projects, Boards and Columns.
//
// The interface intentionally treats boards and columns as children of a
// project — boards are not independently addressable. Phase 2 will introduce
// explicit board CRUD when the UI needs it.
type Repository interface {
	// CreateProject inserts p. The implementation MUST also create one
	// default Board and the DefaultColumns columns (Phase 1 invariant).
	CreateProject(ctx context.Context, p *Project) (*Project, []*Board, []*Column, error)

	// GetProject returns the project with the given id or ErrNotFound.
	GetProject(ctx context.Context, id string) (*Project, error)

	// ListProjects returns all projects owned by ownerID (including
	// archived, unless we add an option in Phase 2).
	ListProjects(ctx context.Context, ownerID string) ([]*Project, error)

	// UpdateProject saves changes to an existing project.
	UpdateProject(ctx context.Context, p *Project) error

	// DeleteProject removes the project (and cascades to boards, columns,
	// tasks per the FK ON DELETE CASCADE chain in 001_init.sql).
	DeleteProject(ctx context.Context, id string) error

	// GetBoard returns the (single) board for the given project id. Phase 1
	// always returns one board per project.
	GetBoard(ctx context.Context, projectID string) (*Board, []*Column, error)

	// GetColumn fetches a single column by id, or ErrNotFound.
	GetColumn(ctx context.Context, id string) (*Column, error)

	// CreateColumn appends c to the (single) board of the given project.
	// The repository computes a position = max(position)+1024 so callers
	// don't need to know about the existing ordering. Returns
	// ErrNotFound when the project (or its board) doesn't exist.
	CreateColumn(ctx context.Context, projectID string, c *Column) (*Column, error)

	// UpdateColumn persists mutable fields (name, position, wip_limit, color).
	// Returns ErrNotFound when no row matches.
	UpdateColumn(ctx context.Context, c *Column) error
}
