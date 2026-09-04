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

	// GetByNumber resolves a project by its sequential number (P<N> ref).
	// Returns ErrNotFound when no project has that number.
	GetByNumber(ctx context.Context, number int) (*Project, error)

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

	// DeleteColumn removes a column. Returns ErrNotFound when the id is
	// unknown and ErrColumnNotEmpty when tasks still reference it (the
	// caller must move them away first; we never cascade-delete tasks
	// silently because that would destroy user data without warning).
	DeleteColumn(ctx context.Context, id string) error

	// UpdateColumn persists mutable fields (name, position, wip_limit, color).
	// Returns ErrNotFound when no row matches.
	UpdateColumn(ctx context.Context, c *Column) error

	// FindColumnByStatus returns the column on the (single) board of the
	// given project whose status matches the supplied machine key, or
	// ErrNotFound if no such column exists. The status lookup is what
	// Phase 27.8 uses to keep tasks and columns in sync — when the
	// owner changes a task's status through the sidebar, the service
	// looks up the column with that status and moves the card.
	FindColumnByStatus(ctx context.Context, projectID, status string) (*Column, error)

	// SetAllowedAgents replaces the project's full grant list in one
	// transaction (DELETE + INSERT): the call is idempotent — calling
	// it twice with the same list converges to the same rows. An
	// empty agentIDs list closes the project to every agent (task
	// 140: access = AgentsAllowed OR a grant row).
	SetAllowedAgents(ctx context.Context, projectID string, agentIDs []string, addedByUserID string) error

	// ListAllowedAgentIDs returns the agent ids explicitly granted to
	// the project. A closed project with no grants returns an empty
	// slice.
	ListAllowedAgentIDs(ctx context.Context, projectID string) ([]string, error)

	// AgentAccessibleProjectIDs returns the set of project ids the
	// agent may see/claim: open projects (agents_allowed = 1) plus
	// closed projects carrying a grant row for this agent.
	AgentAccessibleProjectIDs(ctx context.Context, agentID string) (map[string]bool, error)
}
