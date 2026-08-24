/**
 * BlockNote-based block editor for wiki pages.
 *
 * Renders a full-featured WYSIWYG editor with:
 * - Slash `/` menu (whitelist: paragraph, heading 1-3, bullet/numbered/check
 *   list, quote, divider, codeBlock, table, image, file)
 * - `[[` wiki link autocomplete
 * - Drag-and-drop block reordering
 * - Image/file upload via page attachments API
 * - Light/dark theme matching the app
 *
 * Round-trip strategy (see schema.ts for details):
 * - Storage: standard `link` with `href="wiki:<slug>"` for wiki links
 * - Blocks format: stored/loaded as JSON via GET/PUT /pages/{slug}/blocks
 * - Markdown format: tryParseMarkdownToBlocks on mount; blocksToMarkdown
 *   on save (handled server-side)
 */
import { useCreateBlockNote } from '@blocknote/react';
import type { PartialBlock } from '@blocknote/core';

import { BlockNoteView } from '@blocknote/mantine';
import { useEffect, useState, useCallback, forwardRef, useImperativeHandle } from 'react';
import { api, type WikiBlock } from '@/shared/api/client';
import { schema } from './schema';
import { WikiLinkMenu } from './WikiLinkMenu';
import type { WikiTreeNode } from '@/shared/api/client';

/**
 * Detect current dark mode from the DOM (set by ThemeToggle).
 */
function getIsDark(): 'light' | 'dark' {
  return document.documentElement.classList.contains('dark') ? 'dark' : 'light';
}

interface BlocksEditorProps {
  /** Current page slug (for upload endpoints). */
  slug: string;
  /** Page ID (stable key to force editor re-mount on page change). */
  pageId: string;
  /**
   * Initial content from the server.
   * - format "blocks": blocks array (BlockNote-shaped)
   * - format "markdown": content_md string (parsed on mount)
   */
  initialBlocks?: WikiBlock[];
  initialFormat?: string;
  initialContentMD?: string;
  /** All wiki pages for the [[ autocomplete. */
  pagesTree: WikiTreeNode[];
  /** Called when editor content changes. */
  onChange?: () => void;
}

export interface BlocksEditorHandle {
  /** Get the current document as WikiBlock array. */
  getDocument: () => WikiBlock[];
}

export const BlocksEditor = forwardRef<BlocksEditorHandle, BlocksEditorProps>(function BlocksEditor(
  { slug, pageId, initialBlocks, initialFormat, initialContentMD, pagesTree, onChange },
  ref,
) {
  const [theme, setTheme] = useState<'light' | 'dark'>(getIsDark);
  const [markdownLoaded, setMarkdownLoaded] = useState(initialFormat !== 'markdown');

  // B3 fix: compute initial content synchronously from props.
  // For blocks format: pass directly. For markdown/empty: start empty,
  // load via effect after editor mount.
  const initialContent =
    initialFormat === 'blocks' && initialBlocks
      ? (initialBlocks as unknown as PartialBlock[])
      : undefined;

  // Track theme changes
  useEffect(() => {
    const check = () => setTheme(getIsDark());
    const observer = new MutationObserver(check);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    });
    return () => observer.disconnect();
  }, []);

  // Upload handler for images/files
  const uploadFile = useCallback(
    async (file: File) => {
      const att = await api.uploadPageAttachment(slug, file);
      return `/api/v1/attachments/${att.id}/download`;
    },
    [slug],
  );

  // B3 fix: key by pageId so editor remounts on page change,
  // getting fresh initialContent from props.
  const editor = useCreateBlockNote({ schema, initialContent, uploadFile }, [pageId]);

  // Expose getDocument to parent via ref
  useImperativeHandle(
    ref,
    () => ({
      getDocument: () => editor.document as unknown as WikiBlock[],
    }),
    [editor],
  );

  // B3 fix: load markdown content after editor is mounted
  useEffect(() => {
    if (markdownLoaded || !editor) return;
    if (initialFormat === 'markdown' && initialContentMD) {
      try {
        const blocks = editor.tryParseMarkdownToBlocks(initialContentMD);
        if (blocks.length > 0) {
          editor.replaceBlocks(editor.document, blocks);
        }
      } catch {
        // If markdown parsing fails, start with empty editor
      }
    }
    setMarkdownLoaded(true);
  }, [markdownLoaded, initialFormat, initialContentMD, editor]);

  // B4 fix: use BlockNoteView onChange (single channel, proper cleanup)
  // No onEditorContentChange — that leaks without unsubscribe.

  // Keyboard shortcut: Ctrl+S / Cmd+S triggers save
  useEffect(() => {
    if (!editor) return;
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault();
        // Trigger save via the parent's save handler
        document.dispatchEvent(new CustomEvent('wiki-blocks-save'));
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [editor]);

  if (!editor) {
    return <div className="p-4 text-slate-400 text-sm">Loading editor…</div>;
  }

  return (
    <>
      <BlockNoteView
        editor={editor}
        theme={theme}
        // B4 fix: single onChange channel (no onEditorContentChange)
        onChange={() => onChange?.()}
        // B1: default slash menu shows blocks from schema (no video/audio/embed)
        className="wiki-blocks-editor min-h-[300px]"
      >
        {/* Wiki link chip styling */}
        <style>{`
          .wiki-blocks-editor a[href^="wiki:"] {
            display: inline-flex;
            align-items: center;
            padding: 1px 6px;
            border-radius: 4px;
            background-color: rgb(229 231 235);
            color: rgb(107 114 128);
            font-size: 0.8125rem;
            font-weight: 500;
            text-decoration: none;
            white-space: nowrap;
            transition: background-color 0.15s;
          }
          .wiki-blocks-editor a[href^="wiki:"]:hover {
            background-color: rgb(209 213 219);
          }
          .dark .wiki-blocks-editor a[href^="wiki:"] {
            background-color: rgb(55 65 81);
            color: rgb(156 163 175);
          }
          .dark .wiki-blocks-editor a[href^="wiki:"]:hover {
            background-color: rgb(75 85 99);
          }
        `}</style>
      </BlockNoteView>

      {/* Wiki link autocomplete */}
      <WikiLinkMenu
        editor={editor}
        pagesTree={pagesTree}
        onInsert={(md) => {
          const match = md.match(/^\[(.+)\]\((.+)\)$/);
          if (match) {
            const [, text, href] = match;
            editor.insertInlineContent([{ type: 'link', href, content: text }]);
          }
        }}
      />
    </>
  );
});
