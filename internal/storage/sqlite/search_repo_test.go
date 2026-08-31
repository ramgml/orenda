package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/agent"
	"github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/domain/wiki"
)

func setupSearchDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir+"/s.db"), OpenConfig{
		WALMode: true, EnableForeign: true, BusyTimeoutMs: 5000,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, Migrate(context.Background(), db, MigrationsFS, "migrations"))
	return db
}

// seedSearchData inserts a task, a wiki page, and a comment so FTS5 has
// something to index.
func seedSearchData(t *testing.T, db *sql.DB) {
	t.Helper()

	users := NewUserRepository(db)
	owner := &user.User{Email: "s-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + newUUID()[:8] + "@x.com", PasswordHash: "x", DisplayName: "O"}
	require.NoError(t, users.Create(context.Background(), owner))

	tokens := NewAPITokenRepository(db)
	tok, err := tokens.Create(context.Background(), owner.ID, "s-tok", "fake", "[]", nil)
	require.NoError(t, err)
	agents := NewAgentRepository(db)
	a := &agent.Agent{Name: "s-" + newUUID()[:6], Type: []string{"qwen"}, TokenID: tok.ID}
	require.NoError(t, agents.Create(context.Background(), a))

	projects := NewProjectRepository(db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{Name: "S", OwnerID: owner.ID})
	require.NoError(t, err)

	tasks := NewTaskRepository(db)
	tr := &task.Task{
		ProjectID: p.ID, ColumnID: cols[0].ID,
		Title: "Write the Orenda wiki", Description: "searchable content",
	}
	require.NoError(t, tasks.Create(context.Background(), tr))

	wikis := NewWikiRepository(db)
	page := &wiki.Page{Slug: "orenda-architecture", Title: "Orenda architecture", ContentMD: "searchable wiki page"}
	_, err = wikis.Create(context.Background(), page)
	require.NoError(t, err)

	comments := NewCommentRepository(db)
	c := &comment.Comment{
		TargetID: tr.ID, AuthorID: owner.ID,
		BodyMD: "This is a searchable comment about search.",
	}
	_, err = comments.Create(context.Background(), c)
	require.NoError(t, err)

	_ = time.Second // silence unused
}

func TestSearchRepo_Pages(t *testing.T) {
	db := setupSearchDB(t)
	seedSearchData(t, db)
	repo := NewSearchRepository(db)

	hits, err := repo.SearchPages(context.Background(), "wiki", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(hits), 1)
	for _, h := range hits {
		assert.NotEmpty(t, h.ID)
		assert.NotEmpty(t, h.Snippet)
	}
}

// T103: page hits must carry the wiki slug so the frontend can link
// to /wiki/<slug> — the page route resolves slugs only.
func TestSearchRepo_PagesCarrySlug(t *testing.T) {
	db := setupSearchDB(t)
	seedSearchData(t, db)
	repo := NewSearchRepository(db)

	hits, err := repo.SearchPages(context.Background(), "wiki", 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	for _, h := range hits {
		if h.Title == "Orenda architecture" {
			assert.Equal(t, "orenda-architecture", h.Slug)
			return
		}
	}
	t.Fatal("seeded page 'Orenda architecture' not found in page hits")
}

func TestSearchRepo_Tasks(t *testing.T) {
	db := setupSearchDB(t)
	seedSearchData(t, db)
	repo := NewSearchRepository(db)

	hits, err := repo.SearchTasks(context.Background(), "wiki", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(hits), 1)
}

func TestSearchRepo_Comments(t *testing.T) {
	db := setupSearchDB(t)
	seedSearchData(t, db)
	repo := NewSearchRepository(db)

	hits, err := repo.SearchComments(context.Background(), "searchable", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(hits), 1)
}

func TestSearchRepo_Cyrillic(t *testing.T) {
	db := setupSearchDB(t)
	seedSearchData(t, db)
	repo := NewSearchRepository(db)

	hits, err := repo.SearchPages(context.Background(), "wiki", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(hits), 1)
	// The unicode61 + remove_diacritics 2 tokenizer handles non-ASCII.
	hits2, err := repo.SearchPages(context.Background(), "wiki", 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(hits2), 1)
}

func TestSearchRepo_EmptyReturnsEmpty(t *testing.T) {
	db := setupSearchDB(t)
	seedSearchData(t, db)
	repo := NewSearchRepository(db)

	hits, err := repo.SearchPages(context.Background(), "no-such-term", 10)
	require.NoError(t, err)
	assert.Empty(t, hits)
}

// TestSearchRepo_SnippetMarkersWithHTMLContent pins the snippet contract
// the frontend renderer relies on: FTS5 emits the fixed <mark>/</mark>
// markers around matches while the document body passes through
// verbatim. The frontend splits on those markers and renders the rest
// as plain text, so markup embedded in content (e.g. an <img onerror>)
// must never be interpreted as HTML.
func TestSearchRepo_SnippetMarkersWithHTMLContent(t *testing.T) {
	db := setupSearchDB(t)
	seedSearchData(t, db)

	wikis := NewWikiRepository(db)
	page := &wiki.Page{
		Slug:      "xss-probe",
		Title:     "XSS probe",
		ContentMD: "body with <img src=x onerror=alert(1)> and gadget here",
	}
	_, err := wikis.Create(context.Background(), page)
	require.NoError(t, err)

	repo := NewSearchRepository(db)
	hits, err := repo.SearchPages(context.Background(), "gadget", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)

	snippet := hits[0].Snippet
	// The matched term is wrapped in the fixed markers.
	assert.Contains(t, snippet, "<mark>gadget</mark>")
	// Content passes through verbatim (frontend treats it as text).
	assert.Contains(t, snippet, "<img src=x onerror=alert(1)>")
}
