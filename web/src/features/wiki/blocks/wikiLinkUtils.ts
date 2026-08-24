/**
 * Wiki link utility functions.
 * Separated from WikiLinkMenu.tsx to avoid Vite Fast Refresh incompatibility
 * (non-component named exports alongside React components break HMR).
 */
import type { WikiTreeNode } from '@/shared/api/client';

export interface WikiLinkItem {
  slug: string;
  title: string;
}

export function filterItems(query: string, all: WikiLinkItem[]): WikiLinkItem[] {
  const q = query.toLowerCase().trim();
  if (!q) return all.slice(0, 8);
  return all
    .filter((p) => p.title.toLowerCase().includes(q) || p.slug.toLowerCase().includes(q))
    .slice(0, 8);
}

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
