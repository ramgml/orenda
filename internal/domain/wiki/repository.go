package wiki

import "context"

// Repository persists and retrieves WikiPages and Links.
type Repository interface {
	// Create inserts p. Returns the row with CreatedAt/UpdatedAt.
	Create(ctx context.Context, p *Page) (*Page, error)

	// GetByID returns the page with the given id.
	GetByID(ctx context.Context, id string) (*Page, error)

	// GetBySlug returns the page with the given slug.
	GetBySlug(ctx context.Context, slug string) (*Page, error)

	// List returns all pages ordered by parent_id, position.
	List(ctx context.Context) ([]*Page, error)

	// Update persists changes.
	Update(ctx context.Context, p *Page) error

	// Delete removes the page by id (cascades to wiki_links via FK).
	Delete(ctx context.Context, id string) error

	// UpdateParent re-parents a page under newParentID (or the root
	// when newParentID is empty). Returns ErrNotFound when id is
	// missing, ErrInvalidInput on bad input.
	UpdateParent(ctx context.Context, id, newParentID string) error

	// DescendantIDs returns every id in the subtree under id (not
	// including id itself), depth-first. Used to reject moves that
	// would create cycles.
	DescendantIDs(ctx context.Context, id string) ([]string, error)

	// SetLinks replaces the outgoing links for a page. Used by Save to
	// keep wiki_links in sync with the parsed [[slug]] tokens.
	SetLinks(ctx context.Context, fromPageID string, toPageIDs []string) error

	// Backlinks returns every page that links to the given id.
	Backlinks(ctx context.Context, pageID string) ([]*Page, error)
}
