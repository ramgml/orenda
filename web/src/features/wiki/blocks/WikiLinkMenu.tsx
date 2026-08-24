/**
 * Wiki [[…]] autocomplete for the BlockNote editor.
 *
 * Triggers on `[` and shows matching pages when the second `[` is typed.
 * Picking an item inserts a standard `link` with `href="wiki:<slug>"` —
 * this survives the markdown round-trip (see schema.ts for rationale).
 *
 * `filterItems` and `flattenWikiTree` are ported from the Tiptap-based
 * `WikiLinkSuggestion.tsx` (preserving the same test contract).
 */
import { SuggestionMenuController } from '@blocknote/react';
import type { DefaultReactSuggestionItem } from '@blocknote/react';
import { useMemo, useCallback } from 'react';
import type { WikiTreeNode } from '@/shared/api/client';

/** Minimal item shape used by filterItems / flattenWikiTree. */
export interface WikiLinkItem {
  slug: string;
  title: string;
}

/**
 * Filter wiki pages by query. Matches case-insensitively on title and
 * slug. Returns at most 8 results, preserving original order.
 */
export function filterItems(query: string, all: WikiLinkItem[]): WikiLinkItem[] {
  const q = query.toLowerCase().trim();
  if (!q) return all.slice(0, 8);
  return all
    .filter((p) => p.title.toLowerCase().includes(q) || p.slug.toLowerCase().includes(q))
    .slice(0, 8);
}

/**
 * Flatten a hierarchical wiki page tree into a flat {slug, title} array.
 * Accepts the API client's WikiTreeNode shape ({page, children?}).
 */
export function flattenWikiTree(tree: WikiTreeNode[]): WikiLinkItem[] {
  const out: WikiLinkItem[] = [];
  const visit = (nodes: WikiTreeNode[]) => {
    for (const n of nodes) {
      out.push({ slug: n.page.slug, title: n.page.title });
      if (n.children && n.children.length > 0) visit(n.children);
    }
  };
  visit(tree);
  return out;
}

interface WikiLinkMenuProps {
  /** All known wiki pages for the suggestion list. */
  pagesTree: WikiTreeNode[];
  /**
   * Callback when a wiki link is selected. Receives the markdown-format
   * link string to insert: `[title](wiki:slug)`.
   */
  onInsert: (markdown: string) => void;
}

/**
 * SuggestionMenuController that triggers on `[` and shows wiki page
 * suggestions when the user types a second `[`.
 */
export function WikiLinkMenu({ pagesTree, onInsert }: WikiLinkMenuProps): JSX.Element | null {
  const allPages = useMemo(() => flattenWikiTree(pagesTree), [pagesTree]);

  const getItems = useCallback(
    async (query: string): Promise<DefaultReactSuggestionItem[]> => {
      // Only show suggestions when query starts with a second `[`
      if (!query.startsWith('[')) return [];
      const q = query.slice(1); // Strip leading `[`
      const matched = filterItems(q, allPages);
      return matched.map((p) => ({
        title: p.title,
        subtext: p.slug,
        onItemClick: () => {
          onInsert(`[${p.title}](wiki:${p.slug})`);
        },
      }));
    },
    [allPages, onInsert],
  );

  if (allPages.length === 0) return null;

  return <SuggestionMenuController triggerCharacter="[" minQueryLength={1} getItems={getItems} />;
}
