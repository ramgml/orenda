// Wiki [[…]] suggestion extension (Phase 30.6).
//
// Notion-style autocomplete: the operator types `[[` and a popup lists
// matching wiki pages. Picking one inserts `[[slug]]` as plain text —
// the markdown mirror walks the page content on save, parses
// `[[…]]`, and records `wiki_links` rows so backlinks work.
//
// Implemented with `@tiptap/suggestion`, the canonical Tiptap primitive
// for popup overlays (the same one their official `mention` extension
// builds on). The popup itself is a React component portaled into
// `document.body`; we don't pull in `tippy.js` to keep the dep
// surface small.
//
// Why a custom extension rather than `@tiptap/extension-mention`?
// `mention` assumes `…@` triggers and renders the mention as a
// ProseMirror Node — we want plain markdown text (`[[slug]]`) and
// `[[` as the trigger. Building on `Suggestion` directly keeps both
// contracts explicit.
import { Extension, type Editor, type Range } from '@tiptap/core';
import Suggestion from '@tiptap/suggestion';
import { forwardRef, useEffect, useImperativeHandle, useState } from 'react';
import { createPortal } from 'react-dom';
import { createRoot, type Root } from 'react-dom/client';

/**
 * WikiLinkItem is the surface the suggestion needs to render and pick
 * an item. Just the title and slug — the caller doesn't have to
 * thread the full WikiPage struct down.
 */
export interface WikiLinkItem {
  slug: string;
  title: string;
}

/**
 * Configuration for the WikiLink extension.
 */
export interface WikiLinkConfig {
  /** All pages known to the editor; the popup filters this list. */
  getItems: () => WikiLinkItem[];
}

interface SuggestionRenderProps {
  items: WikiLinkItem[];
  command: (item: { slug: string; label: string }) => void;
}

/**
 * WikiLinkSuggestion extends the editor with a `[[…]]` autocomplete.
 *
 * Two surfaces here, with separate tests:
 *   - filterItems(query, all): pure function; vitest covers it
 *     directly without spinning an editor.
 *   - WikiLinkSuggestion(config): the Tiptap extension itself.
 *     Mounting it requires a real editor and a browser environment
 *     (Tiptap's suggestion relies on `document.body` portals and a
 *     working Selection), so we don't unit-test it here — Playwright
 *     covers it end-to-end in the existing `wiki` flow.
 */
export function filterItems(query: string, all: WikiLinkItem[]): WikiLinkItem[] {
  const q = query.toLowerCase().trim();
  if (!q) return all.slice(0, 8);
  return all
    .filter((p) => p.title.toLowerCase().includes(q) || p.slug.toLowerCase().includes(q))
    .slice(0, 8);
}

/**
 * WikiLinkSuggestion — the Tiptap extension.
 */
export function WikiLinkSuggestion(config: WikiLinkConfig) {
  return Extension.create({
    name: 'wikiLinkSuggestion',

    addOptions() {
      return {
        suggestion: {
          char: '[[',
          startOfLine: false,
          items: ({ query }: { query: string }) => filterItems(query, config.getItems()),
          command: ({
            editor,
            range,
            props,
          }: {
            editor: Editor;
            range: Range;
            props: { slug: string; label: string };
          }) => {
            editor.chain().focus().deleteRange(range).insertContent(`[[${props.slug}]]`).run();
          },
          render: () => {
            let renderer: ReactRendererLite | null = null;
            return {
              onStart: (props: SuggestionRenderProps) => {
                renderer = mountSuggestion(props);
              },
              onUpdate: (props: SuggestionRenderProps) => {
                renderer?.update(props);
              },
              onKeyDown: (props: { event: KeyboardEvent }) =>
                renderer?.onKeyDown(props.event) ?? false,
              onExit: () => {
                renderer?.destroy();
                renderer = null;
              },
            };
          },
        },
      };
    },

    addProseMirrorPlugins() {
      return [
        Suggestion({
          editor: this.editor,
          ...this.options.suggestion,
        }),
      ];
    },
  });
}

// ---------------------------------------------------------------------------
// React popup. We use a minimal mount/update/destroy wrapper instead
// of pulling in @tiptap/react's full ReactRenderer — saves a few
// hundred bytes and keeps the dep surface flat.
// ---------------------------------------------------------------------------

interface SuggestionListHandle {
  onKeyDown: (event: KeyboardEvent) => boolean;
}

interface MountedRenderer {
  update: (props: SuggestionRenderProps) => void;
  onKeyDown: (event: KeyboardEvent) => boolean;
  destroy: () => void;
}

function mountSuggestion(initialProps: SuggestionRenderProps): MountedRenderer {
  const host = document.createElement('div');
  document.body.appendChild(host);

  let currentProps = initialProps;

  // SuggestionList lives as a stable React component we own; we
  // re-render it on every prop change. The forwardRef gives us
  // a keyboard handler that Tiptap's onKeyDown can call.
  const listRef: { current: SuggestionListHandle | null } = { current: null };
  let reactRoot: Root | null = null;

  const renderList = () => {
    const element = (
      <SuggestionList
        ref={(h) => {
          listRef.current = h;
        }}
        items={currentProps.items}
        command={(item) => currentProps.command(item)}
      />
    );
    reactRoot?.render(element);
  };

  reactRoot = createRoot(host);
  renderList();

  // Position the host element. Tiptap's render() doesn't hand us
  // a clientRect, so we anchor to fixed top-left and rely on the
  // popup being keyboard-navigable. A future phase can wire the
  // editor's view.coordsAtPos if a tighter UX is wanted.
  host.style.position = 'fixed';
  host.style.top = '0';
  host.style.left = '0';
  host.style.zIndex = '50';

  return {
    update: (props) => {
      currentProps = props;
      renderList();
    },
    onKeyDown: (event) => listRef.current?.onKeyDown(event) ?? false,
    destroy: () => {
      reactRoot?.unmount();
      host.parentElement?.removeChild(host);
    },
  };
}

// Lightweight alias so the closure types above stay readable.
type ReactRendererLite = MountedRenderer;

const SuggestionList = forwardRef<SuggestionListHandle, SuggestionRenderProps>(
  function SuggestionList({ items, command }, ref) {
    const [selected, setSelected] = useState(0);

    useEffect(() => {
      setSelected(0);
    }, [items]);

    const pick = (idx: number) => {
      const item = items[idx];
      if (!item) return;
      command({ slug: item.slug, label: item.title });
    };

    useImperativeHandle(ref, () => ({
      onKeyDown: (event: KeyboardEvent) => {
        if (!items.length) return false;
        if (event.key === 'ArrowUp') {
          setSelected((s) => (s - 1 + items.length) % items.length);
          return true;
        }
        if (event.key === 'ArrowDown') {
          setSelected((s) => (s + 1) % items.length);
          return true;
        }
        if (event.key === 'Enter' || event.key === 'Tab') {
          pick(selected);
          return true;
        }
        return false;
      },
    }));

    if (!items.length) {
      return createPortal(
        <div
          data-testid="wiki-link-suggestion"
          className="rounded border border dark:border-border bg-card shadow-lg px-3 py-2 text-xs text-slate-500"
        >
          No matching pages.
        </div>,
        document.body,
      );
    }

    return createPortal(
      <div
        data-testid="wiki-link-suggestion"
        className="rounded border border dark:border-border bg-card shadow-lg py-1 text-sm min-w-[180px] max-w-[320px]"
        role="listbox"
      >
        {items.map((item, idx) => (
          <button
            key={item.slug}
            type="button"
            role="option"
            aria-selected={idx === selected}
            data-testid={`wiki-link-item-${item.slug}`}
            onMouseDown={(e) => {
              e.preventDefault();
              pick(idx);
            }}
            onMouseEnter={() => setSelected(idx)}
            className={`w-full text-left px-3 py-1.5 ${
              idx === selected
                ? 'bg-orenda-100 dark:bg-orenda-900/40 text-orenda-700 dark:text-orenda-300'
                : 'hover:bg-slate-100 dark:hover:bg-slate-800'
            }`}
          >
            <div className="font-medium truncate">{item.title}</div>
            <div className="text-[10px] text-slate-400 font-mono truncate">{item.slug}</div>
          </button>
        ))}
      </div>,
      document.body,
    );
  },
);

// ---------------------------------------------------------------------------
// flattenWikiTree — convenience used by callers to feed the
// suggestion list from the hierarchical /pages response. Accepts
// the api client's WikiTreeNode shape ({page, children?}) — we
// reach into .page for slug + title.
// ---------------------------------------------------------------------------

/**
 * TreeNode is the api client's wiki tree node shape (one page + an
 * optional list of children). Declared here as a structural type so
 * WikiLinkSuggestion doesn't have to import from the api client —
 * avoids a circular dependency if the api ever needs to surface
 * suggestion types.
 */
export interface WikiTreeNode {
  page: { slug: string; title: string };
  children?: WikiTreeNode[];
}

/**
 * flattenWikiTree walks a nested pages tree and returns a flat array
 * of {slug, title} entries. The wiki pages endpoint returns a tree;
 * the suggestion only needs slug + title.
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
