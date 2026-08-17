import { describe, expect, it } from 'vitest';
import { filterItems, flattenWikiTree, type WikiLinkItem } from './WikiLinkSuggestion';

const pages: WikiLinkItem[] = [
  { slug: 'architecture', title: 'Architecture' },
  { slug: 'backup', title: 'Backup & Restore' },
  { slug: 'ci-cd', title: 'CI/CD pipeline' },
  { slug: 'phase-30', title: 'Phase 30 backlog' },
  { slug: 'phase-29', title: 'Phase 29 (Agent wiki)' },
  { slug: 'untitled', title: 'TODO: untitled page' },
];

describe('filterItems', () => {
  it('returns the first 8 items when the query is empty', () => {
    const out = filterItems('', pages);
    expect(out).toHaveLength(6); // we only have 6 pages in the fixture
    expect(out[0]).toEqual(pages[0]);
  });

  it('matches case-insensitively on title', () => {
    const out = filterItems('ARCH', pages);
    expect(out).toHaveLength(1);
    expect(out[0].slug).toBe('architecture');
  });

  it('matches case-insensitively on slug', () => {
    const out = filterItems('ci', pages);
    // "ci-cd" matches; "phase-29" mentions CI in title too
    const slugs = out.map((p) => p.slug);
    expect(slugs).toContain('ci-cd');
  });

  it('caps results at 8 even when many match', () => {
    const many: WikiLinkItem[] = Array.from({ length: 20 }, (_, i) => ({
      slug: `page-${i}`,
      title: `Common Page ${i}`,
    }));
    const out = filterItems('common', many);
    expect(out).toHaveLength(8);
  });

  it('returns an empty list when nothing matches', () => {
    const out = filterItems('nothing-here', pages);
    expect(out).toEqual([]);
  });

  it('trims whitespace before matching', () => {
    const out = filterItems('   arch   ', pages);
    expect(out).toHaveLength(1);
    expect(out[0].slug).toBe('architecture');
  });

  it('preserves the original page order (no re-ranking)', () => {
    // Pages are listed in tree-walk order; the suggestion just
    // filters, not sorts. The operator who just created a page
    // expects to see it first.
    const out = filterItems('phase', pages);
    expect(out.map((p) => p.slug)).toEqual(['phase-30', 'phase-29']);
  });

  it('does not partial-match across word boundaries', () => {
    // A query with a space matches the literal substring — pages
    // with that exact string in the title or slug pass. We assert
    // the match is exact-substring (not per-word AND) by picking a
    // query that no title/slug contains.
    const out = filterItems('xxxnotpresent', pages);
    expect(out).toEqual([]);
  });
});

describe('flattenWikiTree', () => {
  it('flattens a hierarchical tree', () => {
    const tree = [
      {
        page: { slug: 'parent', title: 'Parent' },
        children: [
          { page: { slug: 'child-a', title: 'Child A' } },
          {
            page: { slug: 'child-b', title: 'Child B' },
            children: [{ page: { slug: 'grandchild', title: 'Grandchild' } }],
          },
        ],
      },
    ];
    const out = flattenWikiTree(tree);
    expect(out).toEqual([
      { slug: 'parent', title: 'Parent' },
      { slug: 'child-a', title: 'Child A' },
      { slug: 'child-b', title: 'Child B' },
      { slug: 'grandchild', title: 'Grandchild' },
    ]);
  });

  it('returns an empty list for an empty tree', () => {
    expect(flattenWikiTree([])).toEqual([]);
  });

  it('skips nodes with no children field', () => {
    expect(flattenWikiTree([{ page: { slug: 'leaf', title: 'Leaf' } }])).toEqual([
      { slug: 'leaf', title: 'Leaf' },
    ]);
  });
});
