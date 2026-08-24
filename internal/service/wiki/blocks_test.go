package wiki_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ramgml/orenda/internal/domain/wiki"
)

// helper to build a paragraph block.
func paraBlock(id, text string) *wiki.Block {
	c, _ := json.Marshal([]map[string]any{{"type": "text", "text": text}})
	return &wiki.Block{ID: id, Type: "paragraph", Content: c}
}

// helper to build a heading block.
func headingBlock(id, text string, level int) *wiki.Block {
	p, _ := json.Marshal(map[string]int{"level": level})
	c, _ := json.Marshal([]map[string]any{{"type": "text", "text": text}})
	return &wiki.Block{ID: id, Type: "heading", Props: p, Content: c}
}

// helper to build a wikiLink paragraph.
func wikiLinkParaBlock(id, slug string) *wiki.Block {
	c, _ := json.Marshal([]map[string]any{{"type": "wikiLink", "props": map[string]string{"slug": slug}}})
	return &wiki.Block{ID: id, Type: "paragraph", Content: c}
}

func TestBlockService_ReplaceBlockTree(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	// Create a page first.
	page, err := svc.Save(context.Background(), &wiki.Page{
		Slug:  "test-blocks",
		Title: "Test Blocks",
	})
	require.NoError(t, err)
	assert.Equal(t, "markdown", page.ContentFormat)

	// Replace with a block tree.
	tree := []*wiki.Block{
		paraBlock("b1", "Hello world"),
		headingBlock("b2", "My Heading", 2),
		wikiLinkParaBlock("b3", "other-page"),
	}
	got, err := svc.ReplaceBlockTree(context.Background(), "test-blocks", tree)
	require.NoError(t, err)
	assert.Equal(t, "blocks", got.ContentFormat)
	assert.Contains(t, got.ContentMD, "Hello world")
	assert.Contains(t, got.ContentMD, "## My Heading")
	assert.Contains(t, got.ContentMD, "[[other-page]]")

	// wiki_links should be set for other-page.
	other, err := svc.GetBySlug(context.Background(), "other-page")
	require.NoError(t, err)
	bl, err := svc.Backlinks(context.Background(), other.ID)
	require.NoError(t, err)
	assert.Len(t, bl, 1)
	assert.Equal(t, "test-blocks", bl[0].Slug)
}

func TestBlockService_GetBlockView_Markdown(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	page, err := svc.Save(context.Background(), &wiki.Page{
		Slug:      "legacy",
		Title:     "Legacy Page",
		ContentMD: "# Hello",
	})
	require.NoError(t, err)

	bv, err := svc.GetBlockView(context.Background(), "legacy")
	require.NoError(t, err)
	assert.Equal(t, "markdown", bv.Format)
	assert.Equal(t, page.ContentMD, bv.ContentMD)
	assert.Nil(t, bv.Blocks)
}

func TestBlockService_GetBlockView_Blocks(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	_, err := svc.Save(context.Background(), &wiki.Page{
		Slug:  "blockview",
		Title: "BlockView Test",
	})
	require.NoError(t, err)

	tree := []*wiki.Block{
		paraBlock("p1", "First paragraph"),
		headingBlock("h1", "Section", 1),
	}
	_, err = svc.ReplaceBlockTree(context.Background(), "blockview", tree)
	require.NoError(t, err)

	bv, err := svc.GetBlockView(context.Background(), "blockview")
	require.NoError(t, err)
	assert.Equal(t, "blocks", bv.Format)
	assert.Empty(t, bv.ContentMD)
	require.Len(t, bv.Blocks, 2)
	assert.Equal(t, "paragraph", bv.Blocks[0].Type)
	assert.Equal(t, "heading", bv.Blocks[1].Type)
}

func TestBlockService_SaveMarkdownDeletesBlocks(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	_, err := svc.Save(context.Background(), &wiki.Page{
		Slug:  "roundtrip",
		Title: "Roundtrip",
	})
	require.NoError(t, err)

	// Put blocks.
	tree := []*wiki.Block{paraBlock("b1", "block content")}
	_, err = svc.ReplaceBlockTree(context.Background(), "roundtrip", tree)
	require.NoError(t, err)

	bv, err := svc.GetBlockView(context.Background(), "roundtrip")
	require.NoError(t, err)
	assert.Equal(t, "blocks", bv.Format)

	// Now save markdown — should switch back.
	p, err := svc.Resolve(context.Background(), "roundtrip")
	require.NoError(t, err)
	page, err := svc.Save(context.Background(), &wiki.Page{
		Slug:      "roundtrip",
		Title:     "Roundtrip",
		ID:        p.ID,
		ContentMD: "# Back to markdown",
	})
	require.NoError(t, err)
	assert.Equal(t, "markdown", page.ContentFormat)

	// Blocks should be gone.
	bv, err = svc.GetBlockView(context.Background(), "roundtrip")
	require.NoError(t, err)
	assert.Equal(t, "markdown", bv.Format)
	assert.Nil(t, bv.Blocks)
}

func TestBlockService_Validation_DuplicateID(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	_, err := svc.Save(context.Background(), &wiki.Page{
		Slug:  "dup-id",
		Title: "Dup ID",
	})
	require.NoError(t, err)

	tree := []*wiki.Block{
		paraBlock("same", "a"),
		paraBlock("same", "b"),
	}
	_, err = svc.ReplaceBlockTree(context.Background(), "dup-id", tree)
	require.Error(t, err)
	assert.ErrorIs(t, err, wiki.ErrInvalidInput)
}

func TestBlockService_Validation_UnknownType(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	_, err := svc.Save(context.Background(), &wiki.Page{
		Slug:  "bad-type",
		Title: "Bad Type",
	})
	require.NoError(t, err)

	tree := []*wiki.Block{
		{ID: "x", Type: "videoEmbed"},
	}
	_, err = svc.ReplaceBlockTree(context.Background(), "bad-type", tree)
	require.Error(t, err)
	assert.ErrorIs(t, err, wiki.ErrInvalidInput)
}

func TestBlockService_Validation_Depth(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	_, err := svc.Save(context.Background(), &wiki.Page{
		Slug:  "deep",
		Title: "Deep",
	})
	require.NoError(t, err)

	// Build a chain of depth 9 (exceeds maxBlockDepth=8).
	var deepest *wiki.Block
	for i := 0; i < 9; i++ {
		child := &wiki.Block{ID: fmt.Sprintf("d%d", i), Type: "paragraph"}
		if deepest != nil {
			child.Children = []*wiki.Block{deepest}
		}
		deepest = child
	}
	tree := []*wiki.Block{deepest}

	_, err = svc.ReplaceBlockTree(context.Background(), "deep", tree)
	require.Error(t, err)
	assert.ErrorIs(t, err, wiki.ErrInvalidInput)
}

func TestBlockService_Validation_TooManyBlocks(t *testing.T) {
	svc, _ := setupWikiSvc(t)

	_, err := svc.Save(context.Background(), &wiki.Page{
		Slug:  "many",
		Title: "Many",
	})
	require.NoError(t, err)

	blocks := make([]*wiki.Block, 2001)
	for i := range blocks {
		blocks[i] = &wiki.Block{ID: fmt.Sprintf("b%d", i), Type: "paragraph"}
	}
	_, err = svc.ReplaceBlockTree(context.Background(), "many", blocks)
	require.Error(t, err)
	assert.ErrorIs(t, err, wiki.ErrInvalidInput)
}
