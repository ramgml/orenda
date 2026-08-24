package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// helper to create a minimal test page.
func createTestPage(t *testing.T, repo wiki.Repository, slug string) *wiki.Page {
	t.Helper()
	p, err := repo.Create(context.Background(), &wiki.Page{
		Slug:          slug,
		Title:         "Test " + slug,
		ContentMD:     "# " + slug,
		ContentFormat: "markdown",
	})
	require.NoError(t, err)
	return p
}

// TestWikiBlocks_ReplaceAndGetBlocks round-trips blocks through
// ReplaceBlocks → GetBlocks. Validates ordering, nesting (ParentBlockID),
// Props/Content preservation, and the flat sort by parent_block_id, position.
func TestWikiBlocks_ReplaceAndGetBlocks(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	page := createTestPage(t, repo, "wb-roundtrip")

	// Build a flat block list: root paragraph, root heading, child list items.
	blocks := []*wiki.Block{
		{
			ID:       "b-paragraph-1",
			Type:     "paragraph",
			Props:    json.RawMessage(`{"textAlignment":"left"}`),
			Content:  json.RawMessage(`[{"type":"text","text":"Hello world","styles":{}}]`),
			PageID:   page.ID,
			Position: 0,
		},
		{
			ID:       "b-heading-1",
			Type:     "heading",
			Props:    json.RawMessage(`{"level":2}`),
			Content:  json.RawMessage(`[{"type":"text","text":"Sub heading","styles":{}}]`),
			PageID:   page.ID,
			Position: 1,
		},
		{
			ID:            "b-list-1",
			Type:          "bulletListItem",
			Props:         json.RawMessage(`{}`),
			Content:       json.RawMessage(`[{"type":"text","text":"Item 1","styles":{}}]`),
			PageID:        page.ID,
			ParentBlockID: "b-paragraph-1",
			Position:      0,
		},
		{
			ID:            "b-list-2",
			Type:          "bulletListItem",
			Props:         json.RawMessage(`{}`),
			Content:       json.RawMessage(`[{"type":"text","text":"Item 2","styles":{}}]`),
			PageID:        page.ID,
			ParentBlockID: "b-paragraph-1",
			Position:      1,
		},
	}

	err := repo.ReplaceBlocks(ctx, page.ID, blocks)
	require.NoError(t, err)

	got, err := repo.GetBlocks(ctx, page.ID)
	require.NoError(t, err)
	require.Len(t, got, 4)

	// Verify sort order: blocks grouped by parent_block_id, then position.
	// SQLite sorts NULLs first, then "" (empty string), then alphabetically.
	// So root blocks (parent="") come first, then children (parent="b-paragraph-1").
	assert.Equal(t, "b-paragraph-1", got[0].ID, "root paragraph (parent='', pos=0)")
	assert.Equal(t, "b-heading-1", got[1].ID, "root heading (parent='', pos=1)")
	assert.Equal(t, "b-list-1", got[2].ID, "child list item 1 (parent='b-paragraph-1', pos=0)")
	assert.Equal(t, "b-list-2", got[3].ID, "child list item 2 (parent='b-paragraph-1', pos=1)")

	// Verify nesting.
	assert.Equal(t, "", got[0].ParentBlockID, "root block has no parent")
	assert.Equal(t, "", got[1].ParentBlockID, "root block has no parent")
	assert.Equal(t, "b-paragraph-1", got[2].ParentBlockID)
	assert.Equal(t, "b-paragraph-1", got[3].ParentBlockID)

	// Verify Props round-trip.
	var props map[string]any
	require.NoError(t, json.Unmarshal(got[0].Props, &props))
	assert.Equal(t, "left", props["textAlignment"])

	var headingProps map[string]any
	require.NoError(t, json.Unmarshal(got[1].Props, &headingProps))
	assert.Equal(t, float64(2), headingProps["level"])

	// Verify Content round-trip.
	var content []map[string]any
	require.NoError(t, json.Unmarshal(got[0].Content, &content))
	require.Len(t, content, 1)
	assert.Equal(t, "Hello world", content[0]["text"])
}

// TestWikiBlocks_ReplaceBlocks_Idempotent verifies that calling
// ReplaceBlocks twice overwrites the previous set completely.
func TestWikiBlocks_ReplaceBlocks_Idempotent(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	page := createTestPage(t, repo, "wb-idem")

	// First write.
	err := repo.ReplaceBlocks(ctx, page.ID, []*wiki.Block{
		{ID: "old-1", Type: "paragraph", Props: json.RawMessage(`{}`), Content: json.RawMessage(`[]`), PageID: page.ID, Position: 0},
	})
	require.NoError(t, err)

	// Overwrite with new set.
	err = repo.ReplaceBlocks(ctx, page.ID, []*wiki.Block{
		{ID: "new-1", Type: "heading", Props: json.RawMessage(`{"level":1}`), Content: json.RawMessage(`[]`), PageID: page.ID, Position: 0},
		{ID: "new-2", Type: "paragraph", Props: json.RawMessage(`{}`), Content: json.RawMessage(`[]`), PageID: page.ID, Position: 1},
	})
	require.NoError(t, err)

	got, err := repo.GetBlocks(ctx, page.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "new-1", got[0].ID)
	assert.Equal(t, "new-2", got[1].ID)
}

// TestWikiBlocks_ReplaceBlocks_EmptySlice deletes all blocks.
func TestWikiBlocks_ReplaceBlocks_EmptySlice(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	page := createTestPage(t, repo, "wb-empty")

	err := repo.ReplaceBlocks(ctx, page.ID, []*wiki.Block{
		{ID: "x-1", Type: "paragraph", Props: json.RawMessage(`{}`), Content: json.RawMessage(`[]`), PageID: page.ID, Position: 0},
	})
	require.NoError(t, err)

	// Empty slice = delete all.
	err = repo.ReplaceBlocks(ctx, page.ID, []*wiki.Block{})
	require.NoError(t, err)

	got, err := repo.GetBlocks(ctx, page.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestWikiBlocks_CascadeOnPageDelete verifies that deleting a page
// cascades to its blocks via the ON DELETE CASCADE FK.
func TestWikiBlocks_CascadeOnPageDelete(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	page := createTestPage(t, repo, "wb-cascade")

	err := repo.ReplaceBlocks(ctx, page.ID, []*wiki.Block{
		{ID: "c-1", Type: "paragraph", Props: json.RawMessage(`{}`), Content: json.RawMessage(`[]`), PageID: page.ID, Position: 0},
		{ID: "c-2", Type: "heading", Props: json.RawMessage(`{"level":1}`), Content: json.RawMessage(`[]`), PageID: page.ID, Position: 1},
	})
	require.NoError(t, err)

	// Confirm blocks exist.
	got, err := repo.GetBlocks(ctx, page.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)

	// Delete the page.
	err = repo.Delete(ctx, page.ID)
	require.NoError(t, err)

	// Blocks should be gone.
	got, err = repo.GetBlocks(ctx, page.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestWikiBlocks_NilContent verifies that nil Props and Content are
// handled gracefully (the data column stores '{}').
func TestWikiBlocks_NilContent(t *testing.T) {
	db := setupUserDB(t)
	repo := NewWikiRepository(db)
	ctx := context.Background()

	page := createTestPage(t, repo, "wb-nil")

	err := repo.ReplaceBlocks(ctx, page.ID, []*wiki.Block{
		{ID: "n-1", Type: "divider", PageID: page.ID, Position: 0},
	})
	require.NoError(t, err)

	got, err := repo.GetBlocks(ctx, page.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "n-1", got[0].ID)
	assert.Nil(t, got[0].Props)
	assert.Nil(t, got[0].Content)
}
