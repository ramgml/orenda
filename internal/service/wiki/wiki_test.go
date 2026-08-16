package wiki_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/wiki"
	wikisvc "github.com/ramgml/orenda/internal/service/wiki"
	"github.com/ramgml/orenda/internal/storage/sqlite"
)

type memHub struct{ n int }

func (h *memHub) Publish(_ context.Context, _ ws.Event) { h.n++ }

// Close implements ws.Hub (Phase 22.3).
func (h *memHub) Close() {}

func (h *memHub) Subscribe(string, string) (<-chan ws.Event, ws.Unsubscribe) {
	ch := make(chan ws.Event, 1)
	return ch, func() { close(ch) }
}

func setupWikiSvc(t *testing.T) (*wikisvc.Service, *memHub) {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir+"/w.db"), sqlite.OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, sqlite.Migrate(context.Background(), db, sqlite.MigrationsFS, "migrations"))

	hub := &memHub{}
	return wikisvc.New(sqlite.NewWikiRepository(db), hub), hub
}

func TestWikiService_SaveCreatesPage(t *testing.T) {
	svc, hub := setupWikiSvc(t)

	p := &wiki.Page{Slug: "home", Title: "Home", ContentMD: "Hello"}
	got, err := svc.Save(context.Background(), p)
	require.NoError(t, err)
	assert.NotEmpty(t, got.ID)
	assert.Equal(t, 1, hub.n, "expected 1 WS event")
}

func TestWikiService_SaveUpdatesLinks(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	p := &wiki.Page{
		Slug:      "architecture",
		Title:     "Architecture",
		ContentMD: "See [[db-schema]] and [[api-reference]].",
	}
	got, err := svc.Save(context.Background(), p)
	require.NoError(t, err)
	_ = got // architecture's own id isn't needed beyond Save side-effects

	// The referenced slugs should have been auto-created.
	gotRef, err := svc.GetBySlug(context.Background(), "db-schema")
	require.NoError(t, err)
	assert.Equal(t, "db-schema", gotRef.Slug)

	gotRef2, err := svc.GetBySlug(context.Background(), "api-reference")
	require.NoError(t, err)
	assert.Equal(t, "api-reference", gotRef2.Slug)

	// Backlinks from the referenced pages point back to architecture.
	bl, err := svc.Backlinks(context.Background(), gotRef.ID)
	require.NoError(t, err)
	assert.Len(t, bl, 1)
	assert.Equal(t, "architecture", bl[0].Slug)
}

func TestWikiService_BacklinksEmpty(t *testing.T) {
	svc, _ := setupWikiSvc(t)
	p := &wiki.Page{Slug: "empty", Title: "Empty"}
	got, err := svc.Save(context.Background(), p)
	require.NoError(t, err)

	bl, err := svc.Backlinks(context.Background(), got.ID)
	require.NoError(t, err)
	assert.Empty(t, bl)
}

func TestWikiService_Tree(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	root, err := svc.Save(context.Background(), &wiki.Page{Slug: "root", Title: "Root", Position: 0})
	require.NoError(t, err)
	child1, err := svc.Save(context.Background(), &wiki.Page{Slug: "c1", Title: "C1", ParentID: root.ID, Position: 0})
	require.NoError(t, err)
	child2, err := svc.Save(context.Background(), &wiki.Page{Slug: "c2", Title: "C2", ParentID: root.ID, Position: 1})
	require.NoError(t, err)
	_, err = svc.Save(context.Background(), &wiki.Page{Slug: "gc", Title: "GC", ParentID: child1.ID, Position: 0})
	require.NoError(t, err)

	tree, err := svc.Tree(context.Background())
	require.NoError(t, err)
	require.Len(t, tree, 1)
	assert.Equal(t, "root", tree[0].Page.Slug)
	require.Len(t, tree[0].Children, 2)
	assert.Equal(t, child1.ID, tree[0].Children[0].Page.ID)
	assert.Equal(t, child2.ID, tree[0].Children[1].Page.ID)
	assert.Len(t, tree[0].Children[0].Children, 1)
}

func TestWikiService_SaveSlugTaken(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	p1 := &wiki.Page{Slug: "x", Title: "A"}
	_, err := svc.Save(context.Background(), p1)
	require.NoError(t, err)

	p2 := &wiki.Page{Slug: "x", Title: "B"}
	_, err = svc.Save(context.Background(), p2)
	require.Error(t, err)
	assert.ErrorIs(t, err, wikisvc.ErrSlugTaken)
}

func TestWikiService_SaveLinkUpdate(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	p := &wiki.Page{Slug: "x", Title: "A", ContentMD: "see [[y]]"}
	got, err := svc.Save(context.Background(), p)
	require.NoError(t, err)

	// Update to point at a different slug.
	got.ContentMD = "see [[z]]"
	got2, err := svc.Save(context.Background(), got)
	require.NoError(t, err)

	// Old target should no longer be referenced by this page.
	y, err := svc.GetBySlug(context.Background(), "y")
	require.NoError(t, err)
	blY, err := svc.Backlinks(context.Background(), y.ID)
	require.NoError(t, err)
	assert.Empty(t, blY)

	z, err := svc.GetBySlug(context.Background(), "z")
	require.NoError(t, err)
	blZ, err := svc.Backlinks(context.Background(), z.ID)
	require.NoError(t, err)
	assert.Len(t, blZ, 1)
	assert.Equal(t, got2.ID, blZ[0].ID)
	_ = strings.ReplaceAll
}
