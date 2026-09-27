package sqlite

// T365 parity corpus: the same fixtures are seeded on both matrix legs
// (sqlite FTS5 / postgres tsvector) and every case pins the identical
// observable result — same hit sets, same snippet marker contract, same
// relevance direction. Ranking NUMBERS differ by design (bm25 vs
// ts_rank); only their order is compared.
//
// Each leg runs in its own process (pgtest.DriverMatrix), so "parity"
// means both legs satisfy the same table of expectations below; a
// divergence shows up as a failure on exactly one driver.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/comment"
	"github.com/ramgml/orenda/internal/domain/project"
	"github.com/ramgml/orenda/internal/domain/task"
	"github.com/ramgml/orenda/internal/domain/user"
	"github.com/ramgml/orenda/internal/domain/wiki"
	"github.com/ramgml/orenda/internal/service/search"
)

// parityFixture is the shared corpus. Identifiers are unique per test
// (suffix) so matrix runs never collide with leftover rows from other
// tests in the same template-derived database.
type parityFixture struct {
	pageEN     *wiki.Page // en prose, one long document
	pageRU     *wiki.Page // cyrillic prose
	pageDiacr  *wiki.Page // diacritics + hyphenated compounds
	taskPhrase *task.Task // ru phrase target
	taskLong   *task.Task // 40+ word description (long query, tf ranking)
	taskTF     *task.Task // same term twice (relevance sanity)
	comments   []*comment.Comment
}

// seedParityCorpus inserts the shared corpus through the domain
// repositories (identical code path on both drivers).
func seedParityCorpus(t *testing.T, db *sql.DB) *parityFixture {
	t.Helper()
	sfx := newUUID()[:8]

	users := NewUserRepository(db)
	owner := &user.User{Email: "parity-" + sfx + "@x.com", PasswordHash: "x", DisplayName: "P"}
	require.NoError(t, users.Create(context.Background(), owner))

	projects := NewProjectRepository(db)
	p, _, cols, err := projects.CreateProject(context.Background(), &project.Project{Name: "parity-" + sfx, OwnerID: owner.ID})
	require.NoError(t, err)

	fx := &parityFixture{}

	wikis := NewWikiRepository(db)
	fx.pageEN = &wiki.Page{
		Slug:      "parity-en-" + sfx,
		Title:     "Orenda architecture guide",
		ContentMD: "The storage layer keeps pages and tasks. The architecture chapter explains the storage engine internals in prose long enough to need snippet truncation around the storage term.",
	}
	fx.pageRU = &wiki.Page{
		Slug:      "parity-ru-" + sfx,
		Title:     "Поиск в Оренде",
		ContentMD: "Полнотекстовый поиск работает без стемминга: поиск находит точные словоформы.",
	}
	fx.pageDiacr = &wiki.Page{
		Slug:      "parity-diacr-" + sfx,
		Title:     "Café notes",
		ContentMD: "Café drinks: café au lait and strong кофе by the sea. Wiki-page compound token.",
	}
	for _, pg := range []*wiki.Page{fx.pageEN, fx.pageRU, fx.pageDiacr} {
		_, err := wikis.Create(context.Background(), pg)
		require.NoError(t, err)
	}

	tasks := NewTaskRepository(db)
	fx.taskPhrase = &task.Task{
		ProjectID: p.ID, ColumnID: cols[0].ID,
		Title: "Настроить поиск", Description: "Полнотекстовый поиск по задачам",
	}
	fx.taskLong = &task.Task{
		ProjectID: p.ID, ColumnID: cols[0].ID,
		Title: "Long document",
		Description: "Filler prose " + strings.Repeat("padding word ", 24) +
			"with one arcane mention deep inside the body text",
	}
	fx.taskTF = &task.Task{
		ProjectID: p.ID, ColumnID: cols[0].ID,
		Title:       "Arcane ritual",
		Description: "An arcane rite, arcane twice over",
	}
	for _, tk := range []*task.Task{fx.taskPhrase, fx.taskLong, fx.taskTF} {
		require.NoError(t, tasks.Create(context.Background(), tk))
	}

	comments := NewCommentRepository(db)
	for _, body := range []string{
		"Комментарий про поиск: full-text search works",
		"заметка один",
		"заметка два",
		"заметка три",
	} {
		c := &comment.Comment{TargetID: fx.taskPhrase.ID, AuthorID: owner.ID, BodyMD: body}
		_, err := comments.Create(context.Background(), c)
		require.NoError(t, err)
		fx.comments = append(fx.comments, c)
	}
	return fx
}

// slugsOf collects the page slugs of page hits.
func slugsOf(hits []search.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Slug)
	}
	return out
}

// idsOf collects the hit ids of task/comment hits.
func idsOf(hits []search.Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

func TestSearchParity(t *testing.T) {
	db := setupSearchDB(t)
	fx := seedParityCorpus(t, db)
	repo := newSearchRepo(db)
	ctx := context.Background()

	t.Run("en_word_matches_across_fields", func(t *testing.T) {
		hits, err := repo.SearchPages(ctx, "storage", 20)
		require.NoError(t, err)
		assert.Contains(t, slugsOf(hits), fx.pageEN.Slug)
		for _, h := range hits {
			assert.Positive(t, h.Score)
			if h.Slug == fx.pageEN.Slug {
				assert.Contains(t, h.Snippet, "<mark>storage</mark>")
			}
		}
	})

	t.Run("ru_word_no_stemming", func(t *testing.T) {
		hits, err := repo.SearchPages(ctx, "поиск", 20)
		require.NoError(t, err)
		assert.Contains(t, slugsOf(hits), fx.pageRU.Slug)
		// Different word form must NOT match (no stemming, no prefix).
		hits, err = repo.SearchPages(ctx, "поиске", 20)
		require.NoError(t, err)
		assert.NotContains(t, slugsOf(hits), fx.pageRU.Slug)
	})

	t.Run("diacritics_preserved_query", func(t *testing.T) {
		// unicode61 folds the query, 'simple' keeps it — either way the
		// accented query hits the accented document on both drivers.
		hits, err := repo.SearchPages(ctx, "café", 20)
		require.NoError(t, err)
		assert.Contains(t, slugsOf(hits), fx.pageDiacr.Slug)
	})

	t.Run("phrase_en", func(t *testing.T) {
		hits, err := repo.SearchPages(ctx, "storage engine", 20)
		require.NoError(t, err)
		assert.Contains(t, slugsOf(hits), fx.pageEN.Slug)
		// Same tokens, wrong order — phrase must not match.
		hits, err = repo.SearchPages(ctx, "engine storage", 20)
		require.NoError(t, err)
		assert.NotContains(t, slugsOf(hits), fx.pageEN.Slug)
	})

	t.Run("phrase_ru", func(t *testing.T) {
		hits, err := repo.SearchTasks(ctx, "полнотекстовый поиск", 20)
		require.NoError(t, err)
		require.Len(t, idsOf(hits), 1)
		assert.Equal(t, fx.taskPhrase.ID, idsOf(hits)[0])
		// FTS5 marks the whole phrase in one span, ts_headline marks
		// each token separately — the shared contract is: both query
		// words appear inside marked spans.
		assert.Contains(t, hits[0].Snippet, "<mark>")
		assert.Contains(t, hits[0].Snippet, "Полнотекстовый")
		assert.Contains(t, hits[0].Snippet, "поиск")
	})

	t.Run("phrase_gap_negative", func(t *testing.T) {
		// "architecture … storage" are not adjacent in the corpus.
		hits, err := repo.SearchPages(ctx, "architecture storage", 20)
		require.NoError(t, err)
		assert.Empty(t, hits)
	})

	t.Run("short_query", func(t *testing.T) {
		hits, err := repo.SearchPages(ctx, "кофе", 20)
		require.NoError(t, err)
		assert.Contains(t, slugsOf(hits), fx.pageDiacr.Slug)
	})

	t.Run("long_query", func(t *testing.T) {
		long := strings.TrimSpace("filler prose " + strings.Repeat("padding word ", 24) +
			"with one arcane mention")
		hits, err := repo.SearchTasks(ctx, long, 20)
		require.NoError(t, err)
		require.NotEmpty(t, hits)
		assert.Equal(t, fx.taskLong.ID, idsOf(hits)[0])
	})

	t.Run("relevance_tf_ordering", func(t *testing.T) {
		hits, err := repo.SearchTasks(ctx, "arcane", 20)
		require.NoError(t, err)
		require.Len(t, hits, 2)
		// The double-occurrence short document outranks the single
		// occurrence buried in filler — under bm25 AND ts_rank.
		assert.Equal(t, fx.taskTF.ID, idsOf(hits)[0])
		assert.GreaterOrEqual(t, hits[0].Score, hits[1].Score)
	})

	t.Run("punctuation_only_query", func(t *testing.T) {
		hits, err := repo.SearchPages(ctx, "!!!", 20)
		require.NoError(t, err)
		assert.Empty(t, hits)
		hits, err = repo.SearchTasks(ctx, "!!!", 20)
		require.NoError(t, err)
		assert.Empty(t, hits)
		hits, err = repo.SearchComments(ctx, "!!!", 20)
		require.NoError(t, err)
		assert.Empty(t, hits)
	})

	t.Run("limit_respected", func(t *testing.T) {
		hits, err := repo.SearchComments(ctx, "заметка", 2)
		require.NoError(t, err)
		require.Len(t, hits, 2)
		for _, h := range hits {
			assert.Contains(t, h.Snippet, "<mark>заметка</mark>")
		}
		// Raising the limit sees all three seeded comments.
		all, err := repo.SearchComments(ctx, "заметка", 20)
		require.NoError(t, err)
		assert.Len(t, all, 3)
	})
}
