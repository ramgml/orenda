/**
 * Wiki [[…]] autocomplete for the BlockNote editor.
 *
 * Triggers when the user types `[[` and shows matching pages.
 * Picking an item inserts a standard `link` with `href="wiki:<slug>"` —
 * this survives the markdown round-trip (see schema.ts for rationale).
 *
 * Uses a simple event-driven approach rather than SuggestionMenuController
 * because the controller doesn't reliably trigger for non-/ characters
 * inside BlockNoteView.
 *
 * `filterItems` and `flattenWikiTree` are ported from the Tiptap-based
 * `WikiLinkSuggestion.tsx` (preserving the same test contract).
 */
import { useMemo, useCallback, useEffect, useRef, useState } from 'react';
import type { BlockNoteEditor } from '@blocknote/core';
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
  /** The BlockNote editor instance. */
  editor: BlockNoteEditor<any, any, any>;
  /** All known wiki pages for the suggestion list. */
  pagesTree: WikiTreeNode[];
  /**
   * Callback when a wiki link is selected. Receives the markdown-format
   * link string to insert: `[title](wiki:slug)`.
   */
  onInsert: (markdown: string) => void;
}

/**
 * Wiki [[ autocomplete popup. Listens for `[[` input in the editor
 * and shows a filtered list of wiki pages. Selecting an item inserts
 * a standard link with wiki: protocol href.
 */
export function WikiLinkMenu({
  editor,
  pagesTree,
  onInsert,
}: WikiLinkMenuProps): JSX.Element | null {
  const allPages = useMemo(() => flattenWikiTree(pagesTree), [pagesTree]);
  const [query, setQuery] = useState('');
  const [isOpen, setIsOpen] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const popupRef = useRef<HTMLDivElement>(null);
  const triggerPosRef = useRef<number>(0);

  const matched = useMemo(() => {
    if (!query) return allPages.slice(0, 8);
    return filterItems(query, allPages);
  }, [query, allPages]);

  const insertLink = useCallback(
    (item: WikiLinkItem) => {
      // W1 fix: only delete [[query trigger text via transaction.
      // The actual link insertion is done once by onInsert (editor.insertInlineContent).
      // Using _tiptapEditor because BlockNote 0.54 has no public API for
      // deleting arbitrary text ranges in inline content.
      // W5: _tiptapEditor is private API — used intentionally for text range deletion
      // (BlockNote 0.54 has no public equivalent). Safe to use until BlockNote
      // provides a public deleteRange API.
      const state = editor._tiptapEditor.state;
      const tr = state.tr;
      let startPos = -1;
      state.doc.descendants((node, pos) => {
        if (startPos >= 0) return false;
        if (node.isText) {
          const text = node.text || '';
          const idx = text.indexOf('[[');
          if (idx >= 0) {
            startPos = pos + idx;
            return false;
          }
        }
      });
      if (startPos >= 0) {
        const endPos = editor._tiptapEditor.state.selection.from;
        tr.delete(startPos, endPos);
        editor._tiptapEditor.view.dispatch(tr);
      }
      setIsOpen(false);
      setQuery('');
      // Single insertion via onInsert — parent calls editor.insertInlineContent
      onInsert(`[${item.title}](wiki:${item.slug})`);
    },
    [editor, onInsert],
  );

  // Listen for keyboard input in the editor
  useEffect(() => {
    if (!editor) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!isOpen) return;

      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setSelectedIndex((i) => Math.min(i + 1, matched.length - 1));
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        setSelectedIndex((i) => Math.max(i - 1, 0));
      } else if (event.key === 'Enter' || event.key === 'Tab') {
        event.preventDefault();
        if (matched[selectedIndex]) {
          insertLink(matched[selectedIndex]);
        }
      } else if (event.key === 'Escape') {
        setIsOpen(false);
        setQuery('');
      }
    };

    const handleChange = (_editor: any, _ctx: any) => {
      // W5: _tiptapEditor is private API — used intentionally for text range deletion
      // (BlockNote 0.54 has no public equivalent). Safe to use until BlockNote
      // provides a public deleteRange API.
      const state = editor._tiptapEditor.state;
      const { from } = state.selection;
      // Look backward from cursor for [[
      const textBefore = state.doc.textBetween(Math.max(0, from - 50), from, '');
      const doubleBracketIdx = textBefore.lastIndexOf('[[');
      if (doubleBracketIdx >= 0) {
        const afterBracket = textBefore.slice(doubleBracketIdx + 2);
        // Only trigger if no newline between [[ and cursor
        if (!afterBracket.includes('\n')) {
          setQuery(afterBracket);
          setIsOpen(true);
          setSelectedIndex(0);
          triggerPosRef.current = from - afterBracket.length - 2;
          return;
        }
      }
      setIsOpen(false);
      setQuery('');
    };

    const unsub = editor.onChange(handleChange);
    document.addEventListener('keydown', handleKeyDown, true);
    return () => {
      unsub();
      document.removeEventListener('keydown', handleKeyDown, true);
    };
  }, [editor, isOpen, matched, selectedIndex, insertLink]);

  if (!isOpen || matched.length === 0) return null;

  // Position near cursor
  const from = editor._tiptapEditor.state.selection.from;
  // Approximate position from editor view
  const editorView = editor._tiptapEditor.view;
  const coords = editorView.coordsAtPos(from);

  return (
    <div
      ref={popupRef}
      className="fixed z-50 rounded border border-border bg-card shadow-lg py-1 text-sm min-w-[180px] max-w-[320px]"
      style={{ top: coords.bottom + 4, left: coords.left }}
      role="listbox"
    >
      {matched.map((item, idx) => (
        <button
          key={item.slug}
          type="button"
          role="option"
          aria-selected={idx === selectedIndex}
          data-testid={`wiki-link-item-${item.slug}`}
          onMouseDown={(e) => {
            e.preventDefault();
            insertLink(item);
          }}
          onMouseEnter={() => setSelectedIndex(idx)}
          className={`w-full text-left px-3 py-1.5 ${
            idx === selectedIndex
              ? 'bg-orenda-100 dark:bg-orenda-900/40 text-orenda-700 dark:text-orenda-300'
              : 'hover:bg-slate-100 dark:hover:bg-slate-800'
          }`}
        >
          <div className="font-medium truncate">{item.title}</div>
          <div className="text-[10px] text-slate-400 font-mono truncate">{item.slug}</div>
        </button>
      ))}
    </div>
  );
}
