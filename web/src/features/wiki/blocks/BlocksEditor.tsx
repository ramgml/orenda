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
 * - Markdown format: `tryParseMarkdownToBlocks` on load; `blocksToMarkdown`
 *   on save (handled server-side)
 */
import { useCreateBlockNote } from '@blocknote/react';
import { BlockNoteView } from '@blocknote/mantine';
import type { BlockNoteEditor } from '@blocknote/core';
import { useEffect, useRef, useState, useCallback, forwardRef, useImperativeHandle } from 'react';
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
   * - format "markdown": content_md string
   */
  initialBlocks?: WikiBlock[];
  initialFormat?: string;
  initialContentMD?: string;
  /** All wiki pages for the [[ autocomplete. */
  pagesTree: WikiTreeNode[];
  /**
   * Called whenever the editor content changes.
   * Receives the current document as a WikiBlock array.
   */
  onChange?: (blocks: WikiBlock[]) => void;
  /**
   * Called when the user triggers save (e.g. Ctrl+S). The parent
   * should call the exposed `getDocument()` to get current blocks.
   */
  onSaveRequest?: () => void;
}

export interface BlocksEditorHandle {
  /** Get the current document as WikiBlock array. */
  getDocument: () => WikiBlock[];
}

type WikiBlockNoteEditor = BlockNoteEditor<
  (typeof schema)['blockSchema'],
  (typeof schema)['inlineContentSchema'],
  (typeof schema)['styleSchema']
>;

export const BlocksEditor = forwardRef<BlocksEditorHandle, BlocksEditorProps>(function BlocksEditor(
  {
    slug,
    pageId,
    initialBlocks,
    initialFormat,
    initialContentMD,
    pagesTree,
    onChange,
    onSaveRequest,
  }: BlocksEditorProps,
  ref,
) {
  const [theme, setTheme] = useState<'light' | 'dark'>(getIsDark);
  const [initialContent, setInitialContent] = useState<WikiBlock[] | undefined>(undefined);
  const [contentReady, setContentReady] = useState(false);

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

  // Prepare initial content based on format
  useEffect(() => {
    if (initialFormat === 'blocks' && initialBlocks) {
      setInitialContent(initialBlocks);
      setContentReady(true);
    } else if (initialFormat === 'markdown' && initialContentMD) {
      // For markdown, create editor with empty content, then load via replaceBlocks
      setInitialContent(undefined);
      setContentReady(true);
    } else {
      // Empty page
      setInitialContent(undefined);
      setContentReady(true);
    }
  }, [pageId, initialFormat, initialBlocks, initialContentMD]);

  // Upload handler for images/files
  const uploadFile = useCallback(
    async (file: File) => {
      const att = await api.uploadPageAttachment(slug, file);
      return `/api/v1/attachments/${att.id}/download`;
    },
    [slug],
  );

  const editor = useCreateBlockNote({
    schema,
    initialContent: initialContent as any,
    uploadFile,
  });

  // Expose getDocument to parent via ref
  const editorRef = useRef<WikiBlockNoteEditor>(editor);
  editorRef.current = editor;

  useImperativeHandle(
    ref,
    () => ({
      getDocument: () => editorRef.current.document as any,
    }),
    [],
  );

  // Load markdown content after editor is mounted
  useEffect(() => {
    if (!contentReady) return;
    if (initialFormat === 'markdown' && initialContentMD && editor) {
      try {
        const blocks = editor.tryParseMarkdownToBlocks(initialContentMD);
        if (blocks.length > 0) {
          editor.replaceBlocks(editor.document, blocks);
        }
      } catch {
        // If markdown parsing fails, start with empty editor
      }
    }
  }, [contentReady, initialFormat, initialContentMD, editor]);

  // Notify parent of content changes
  useEffect(() => {
    if (!editor || !onChange) return;
    const handler = () => {
      onChange(editor.document as any);
    };
    editor.onEditorContentChange(handler);
    return () => editor.onEditorContentChange(handler);
  }, [editor, onChange]);

  // Keyboard shortcut: Ctrl+S / Cmd+S triggers save
  useEffect(() => {
    if (!editor || !onSaveRequest) return;
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 's') {
        e.preventDefault();
        onSaveRequest();
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [editor, onSaveRequest]);

  if (!editor) {
    return <div className="p-4 text-slate-400 text-sm">Loading editor…</div>;
  }

  return (
    <BlockNoteView
      editor={editor}
      theme={theme}
      onChange={() => {
        onChange?.(editor.document as any);
      }}
      className="wiki-blocks-editor min-h-[300px]"
      data-yc-editor-toolbar={false}
    >
      {/* Slash menu — uses default items; whitelist enforced via schema */}
      {null}

      {/* Wiki [[ autocomplete */}
      <WikiLinkMenu
        pagesTree={pagesTree}
        onInsert={(md) => {
          // Insert as a standard link — wiki: protocol survives round-trip
          const match = md.match(/^\[(.+)\]\((.+)\)$/);
          if (match) {
            const [, text, href] = match;
            editor.insertInlineContent([
              {
                type: 'link',
                href,
                content: text,
              },
            ]);
          }
        }}
      />

      {/* Wiki link chip styling + slash menu whitelist filtering */}
      <style>{`
        /* Wiki link chips — links with wiki: protocol rendered as pills */
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
  );
});
