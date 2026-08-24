/**
 * Wiki [[…]] autocomplete for the BlockNote editor.
 *
 * Triggers when the user types [[ and shows matching pages.
 * Picking an item inserts a standard link with href="wiki:<slug>".
 *
 * Uses refs for all mutable state to avoid useEffect re-registration.
 */
import { useMemo, useCallback, useEffect, useRef, useState } from 'react';
import type { BlockNoteEditor } from '@blocknote/core';
import type { WikiTreeNode } from '@/shared/api/client';
import { filterItems, flattenWikiTree, type WikiLinkItem } from './wikiLinkUtils';

interface WikiLinkMenuProps {
  editor: BlockNoteEditor<any, any, any>;
  pagesTree: WikiTreeNode[];
  onInsert: (markdown: string) => void;
}

const NAV_KEYS = new Set([
  'Shift',
  'Control',
  'Alt',
  'Meta',
  'CapsLock',
  'ArrowLeft',
  'ArrowRight',
  'ArrowUp',
  'ArrowDown',
  'Home',
  'End',
  'PageUp',
  'PageDown',
]);

export function WikiLinkMenu({
  editor,
  pagesTree,
  onInsert,
}: WikiLinkMenuProps): JSX.Element | null {
  const allPages = useMemo(() => flattenWikiTree(pagesTree), [pagesTree]);

  const isOpenRef = useRef(false);
  const queryRef = useRef('');
  const selectedRef = useRef(0);
  const triggerPosRef = useRef(0);
  const matchedRef = useRef<WikiLinkItem[]>([]);
  const [, forceRender] = useState(0);

  // Update matchedRef whenever allPages or query changes
  useEffect(() => {
    matchedRef.current = queryRef.current
      ? filterItems(queryRef.current, allPages)
      : allPages.slice(0, 8);
  }, [allPages]); // queryRef changes don't trigger re-render; matchedRef is updated in checkForTrigger

  const insertLink = useCallback(
    (item: WikiLinkItem) => {
      // Delete ONLY the [[query range using tracked trigger position.
      // Never search the whole document — that destroys unrelated content.
      const state = editor._tiptapEditor.state;
      const start = triggerPosRef.current;
      const end = editor._tiptapEditor.state.selection.from;
      if (start >= 0 && start < end) {
        editor._tiptapEditor.view.dispatch(state.tr.delete(start, end));
      }
      isOpenRef.current = false;
      queryRef.current = '';
      forceRender((n) => n + 1);
      // Single insertion via onInsert — parent calls editor.insertInlineContent
      onInsert(`[${item.title}](wiki:${item.slug})`);
    },
    [editor, onInsert],
  );

  // Register keyup listener ONCE on mount
  useEffect(() => {
    if (!editor) return;

    const checkForTrigger = () => {
      const state = editor._tiptapEditor.state;
      const { from } = state.selection;
      const textBefore = state.doc.textBetween(Math.max(0, from - 50), from, '');
      const idx = textBefore.lastIndexOf('[[');
      if (idx >= 0) {
        const after = textBefore.slice(idx + 2);
        if (!after.includes('\n')) {
          queryRef.current = after;
          isOpenRef.current = true;
          selectedRef.current = 0;
          triggerPosRef.current = from - after.length - 2;
          // Update matchedRef
          matchedRef.current = filterItems(after, allPages);
          forceRender((n) => n + 1);
          return;
        }
      }
      if (isOpenRef.current) {
        isOpenRef.current = false;
        queryRef.current = '';
        forceRender((n) => n + 1);
      }
    };

    const handleKeyUp = (e: KeyboardEvent) => {
      if (NAV_KEYS.has(e.key)) return;
      setTimeout(checkForTrigger, 10);
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      if (!isOpenRef.current) return;
      const m = matchedRef.current;
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        selectedRef.current = Math.min(selectedRef.current + 1, m.length - 1);
        forceRender((n) => n + 1);
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        selectedRef.current = Math.max(selectedRef.current - 1, 0);
        forceRender((n) => n + 1);
      } else if ((e.key === 'Enter' || e.key === 'Tab') && m[selectedRef.current]) {
        e.preventDefault();
        insertLink(m[selectedRef.current]);
      } else if (e.key === 'Escape') {
        isOpenRef.current = false;
        queryRef.current = '';
        forceRender((n) => n + 1);
      }
    };

    document.addEventListener('keyup', handleKeyUp, true);
    document.addEventListener('keydown', handleKeyDown, true);
    return () => {
      document.removeEventListener('keyup', handleKeyUp, true);
      document.removeEventListener('keydown', handleKeyDown, true);
    };
  }, [editor, insertLink, allPages]);

  if (!isOpenRef.current || matchedRef.current.length === 0) return null;

  const { from } = editor._tiptapEditor.state.selection;
  const coords = editor._tiptapEditor.view.coordsAtPos(from);

  return (
    <div
      className="fixed z-50 rounded border border-border bg-card shadow-lg py-1 text-sm min-w-[180px] max-w-[320px]"
      style={{ top: coords.bottom + 4, left: coords.left }}
      role="listbox"
    >
      {matchedRef.current.map((item, idx) => (
        <button
          key={item.slug}
          type="button"
          role="option"
          aria-selected={idx === selectedRef.current}
          data-testid={`wiki-link-item-${item.slug}`}
          onMouseDown={(e) => {
            e.preventDefault();
            insertLink(item);
          }}
          onMouseEnter={() => {
            selectedRef.current = idx;
            forceRender((n) => n + 1);
          }}
          className={`w-full text-left px-3 py-1.5 ${
            idx === selectedRef.current
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
