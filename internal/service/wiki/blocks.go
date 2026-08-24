package wiki

import (
	"context"
	"fmt"
	"sort"

	"github.com/ramgml/orenda/internal/api/ws"
	"github.com/ramgml/orenda/internal/domain/wiki"
)

// BlockView is the JSON shape returned by GET /pages/{slug}/blocks.
type BlockView struct {
	Format    string        `json:"format"`               // "markdown" | "blocks"
	ContentMD string        `json:"content_md,omitempty"` // populated when format="markdown"
	Blocks    []*wiki.Block `json:"blocks,omitempty"`     // populated when format="blocks"
}

// GetBlockView returns the block representation of a page.
//
// For markdown-format pages this is a simple pass-through of the raw
// content_md.  For block-format pages the flat list from the DB is
// assembled into a tree (parent→children, sorted by Position).
func (s *Service) GetBlockView(ctx context.Context, ref string) (*BlockView, error) {
	p, err := s.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	if p.ContentFormat == "markdown" || p.ContentFormat == "" {
		return &BlockView{Format: "markdown", ContentMD: p.ContentMD}, nil
	}
	blocks, err := s.Repo.GetBlocks(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	tree := assembleTree(blocks)
	return &BlockView{Format: "blocks", Blocks: tree}, nil
}

// assembleTree builds a tree from a flat block list.
// Every block's Children is populated from blocks sharing the same
// ParentBlockID. Blocks at each level are sorted by Position.
func assembleTree(flat []*wiki.Block) []*wiki.Block {
	byParent := make(map[string][]*wiki.Block)
	for _, b := range flat {
		pid := b.ParentBlockID
		byParent[pid] = append(byParent[pid], b)
	}
	for _, bs := range byParent {
		sort.Slice(bs, func(i, j int) bool {
			return bs[i].Position < bs[j].Position
		})
	}
	return buildBlockTree(byParent, "")
}

func buildBlockTree(byParent map[string][]*wiki.Block, parentID string) []*wiki.Block {
	children := byParent[parentID]
	out := make([]*wiki.Block, 0, len(children))
	for _, b := range children {
		bcopy := *b
		bcopy.Children = buildBlockTree(byParent, b.ID)
		out = append(out, &bcopy)
	}
	return out
}

// ReplaceBlockTree validates and persists a block tree, then projects
// it into content_md + wiki_links, and fires the wiki.saved event +
// mirror write — the full pipeline mirroring Save for blocks.
//
// Validation:
//   - Every ID is non-empty and unique.
//   - Every type is in the ValidBlockType whitelist.
//   - Tree depth ≤ 8.
//   - Total block count (all levels) ≤ 2000.
//
// Returns ErrInvalidInput on any violation.
func (s *Service) ReplaceBlockTree(ctx context.Context, ref string, tree []*wiki.Block) (*wiki.Page, error) {
	p, err := s.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := validateBlockTree(tree); err != nil {
		return nil, err
	}
	flat := flattenDFS(tree, p.ID)
	if err := s.Repo.ReplaceBlocks(ctx, p.ID, flat); err != nil {
		return nil, err
	}
	md := BlocksToMarkdown(tree)
	p.ContentMD = md
	p.ContentFormat = "blocks"
	if err := s.Repo.Update(ctx, p); err != nil {
		return nil, err
	}
	// Re-read after update to pick up UpdatedAt.
	got, err := s.Repo.GetByID(ctx, p.ID)
	if err != nil {
		return nil, err
	}

	// --- shared pipeline (same as Save) ---
	slugs := extractSlugs(md)
	toIDs, err := s.resolveSlugs(ctx, slugs)
	if err != nil {
		return nil, err
	}
	if err := s.Repo.SetLinks(ctx, got.ID, toIDs); err != nil {
		return nil, err
	}
	if s.Hub != nil {
		s.Hub.Publish(ctx, ws.Event{
			Topic: "wiki",
			Body: map[string]any{
				"type": "wiki.saved",
				"page": got,
			},
		})
	}
	if s.Mirror != nil {
		_, _ = s.Mirror.WritePage(got)
	}
	return got, nil
}

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

const (
	maxBlockDepth = 8
	maxBlockCount = 2000
)

// validateBlockTree validates the entire tree (all levels). It counts
// ALL blocks recursively, not just the root slice, to enforce the
// total block limit.
func validateBlockTree(tree []*wiki.Block) error {
	if len(tree) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	total, err := countAndValidate(tree, 0, seen)
	if err != nil {
		return err
	}
	if total > maxBlockCount {
		return fmt.Errorf("%w: too many blocks (%d > %d)", wiki.ErrInvalidInput, total, maxBlockCount)
	}
	return nil
}

// countAndValidate walks the tree validating depth, ID uniqueness,
// and block types. Returns the total count of blocks visited.
func countAndValidate(blocks []*wiki.Block, depth int, seen map[string]struct{}) (int, error) {
	if depth > maxBlockDepth {
		return 0, fmt.Errorf("%w: block tree depth exceeds %d", wiki.ErrInvalidInput, maxBlockDepth)
	}
	count := 0
	for _, b := range blocks {
		count++
		if b.ID == "" {
			return 0, fmt.Errorf("%w: block ID must not be empty", wiki.ErrInvalidInput)
		}
		if _, ok := seen[b.ID]; ok {
			return 0, fmt.Errorf("%w: duplicate block ID %q", wiki.ErrInvalidInput, b.ID)
		}
		seen[b.ID] = struct{}{}
		if !wiki.ValidBlockType(b.Type) {
			return 0, fmt.Errorf("%w: unknown block type %q", wiki.ErrInvalidInput, b.Type)
		}
		childCount, err := countAndValidate(b.Children, depth+1, seen)
		count += childCount
		if err != nil {
			return 0, err
		}
	}
	return count, nil
}

// flattenDFS converts a tree into a flat list suitable for ReplaceBlocks.
// Each block gets ParentBlockID and Position set according to the tree
// structure.
func flattenDFS(tree []*wiki.Block, pageID string) []*wiki.Block {
	var out []*wiki.Block
	flattenChildren(tree, pageID, "", &out)
	return out
}

func flattenChildren(blocks []*wiki.Block, pageID, parentBlockID string, out *[]*wiki.Block) {
	for i, b := range blocks {
		bcopy := *b
		bcopy.PageID = pageID
		bcopy.ParentBlockID = parentBlockID
		bcopy.Position = i
		// Strip children — they're stored as separate rows.
		bcopy.Children = nil
		*out = append(*out, &bcopy)
		flattenChildren(b.Children, pageID, b.ID, out)
	}
}
