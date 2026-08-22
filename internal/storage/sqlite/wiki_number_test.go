package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// TestWikiRepo_NumberAssignedSequentially pins the Phase-36 contract:
// every Create draws COALESCE(MAX(number),0)+1, so numbers are 1-based
// and monotonically increasing in creation order.
func TestWikiRepo_NumberAssignedSequentially(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	prev := 0
	for i := range 5 {
		p, err := repo.Create(ctx, &wiki.Page{
			Slug:  "page-" + string(rune('a'+i)),
			Title: "Page " + string(rune('A'+i)),
		})
		require.NoError(t, err)
		assert.Equal(t, prev+1, p.Number, "page %d should get number %d", i, prev+1)
		prev = p.Number
	}
	assert.Equal(t, 5, prev)
}

// TestWikiRepo_NumberNeverReused: deleting a page must NOT free its
// number — a "W42" reference in a commit message or branch name has
// to keep pointing at the same (now deleted) page forever.
func TestWikiRepo_NumberNeverReused(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	mk := func(slug string) *wiki.Page {
		p, err := repo.Create(ctx, &wiki.Page{Slug: slug, Title: slug})
		require.NoError(t, err)
		return p
	}

	a := mk("w-num-a")
	b := mk("w-num-b")
	c := mk("w-num-c")

	t.Run("delete head", func(t *testing.T) {
		err := repo.Delete(ctx, c.ID)
		require.NoError(t, err)
		_, err = repo.GetByNumber(ctx, c.Number)
		assert.ErrorIs(t, err, wiki.ErrNotFound,
			"after deleting the newest page its number must stay burned")
	})

	t.Run("delete middle", func(t *testing.T) {
		err := repo.Delete(ctx, b.ID)
		require.NoError(t, err)
		_, err = repo.GetByNumber(ctx, b.Number)
		assert.ErrorIs(t, err, wiki.ErrNotFound,
			"after deleting a middle page its number must stay burned")
	})

	// The two surviving pages keep their original numbers.
	gotA, err := repo.GetByNumber(ctx, a.Number)
	require.NoError(t, err)
	assert.Equal(t, a.ID, gotA.ID)

	// Next page gets MAX+1 (c.Number was 3, next is 4).
	d := mk("w-num-d")
	assert.Equal(t, c.Number+1, d.Number,
		"next page after deletes must get MAX(number)+1, not reuse burned numbers")
}

// TestWikiRepo_GetByNumber: the "W<N>" lookup — hit and miss.
func TestWikiRepo_GetByNumber(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	p, err := repo.Create(ctx, &wiki.Page{Slug: "w-num-lookup", Title: "Lookup"})
	require.NoError(t, err)
	assert.Greater(t, p.Number, 0)

	got, err := repo.GetByNumber(ctx, p.Number)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)
	assert.Equal(t, p.Number, got.Number)

	_, err = repo.GetByNumber(ctx, p.Number+1000)
	assert.ErrorIs(t, err, wiki.ErrNotFound)
}

// TestWikiRepo_NumberVsSlugNoCollision: a numeric ref and a slug are
// disjoint namespaces — GetBySlug never matches a number string and the
// UUID (which always contains '-' and hex letters) is never mistaken
// for a number by ParseRefNumber.
func TestWikiRepo_NumberVsSlugNoCollision(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	p, err := repo.Create(ctx, &wiki.Page{Slug: "w-num-collision", Title: "Collision"})
	require.NoError(t, err)

	// The number resolves via GetByNumber.
	got, err := repo.GetByNumber(ctx, p.Number)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got.ID)

	// The slug resolves via GetBySlug.
	got2, err := repo.GetBySlug(ctx, p.Slug)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got2.ID)

	// The string form of the number resolves by number.
	got3, err := repo.GetByNumber(ctx, p.Number)
	require.NoError(t, err)
	assert.Equal(t, p.ID, got3.ID)
}
