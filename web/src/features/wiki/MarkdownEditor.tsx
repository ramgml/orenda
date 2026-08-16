import { EditorContent, useEditor } from '@tiptap/react';
import { BubbleMenu } from '@tiptap/react/menus';
import StarterKit from '@tiptap/starter-kit';
import Link from '@tiptap/extension-link';
import { Markdown } from 'tiptap-markdown';
import { useEffect, useMemo } from 'react';
import {
  flattenWikiTree,
  WikiLinkSuggestion,
  type WikiLinkItem,
  type WikiTreeNode,
} from './WikiLinkSuggestion';

/**
 * Notion-style markdown editor built on Tiptap.
 *
 * Storage stays as plain markdown via the `tiptap-markdown` extension —
 * `editor.storage.markdown.getMarkdown()` round-trips back to the same
 * text we loaded. Markdown shortcuts work out of the box from the
 * StarterKit (type `**` then space for bold, `# ` for h1, `- ` for a
 * list, `> ` for a quote, etc.), giving the Notion feel without
 * forcing the user to think in markdown.
 *
 * BubbleMenu shows a small floating toolbar when the user selects
 * text: B / I / link / H1 / H2 / list — that's the Notion "floating
 * menu on selection" UX.
 *
 * Phase 30.6: when `pages` is provided, typing `[[` opens a popup
 * with matching page titles; picking one inserts `[[slug]]` as plain
 * text — the markdown mirror parses those on save and records
 * `wiki_links` rows so backlinks work.
 */
export function MarkdownEditor({
  value,
  onChange,
  placeholder,
  pages,
}: {
  value: string;
  onChange: (md: string) => void;
  placeholder?: string;
  /**
   * Hierarchical list of wiki pages for the [[…]] suggestion popup.
   * When omitted, the suggestion extension is not mounted (so other
   * callers — tests, simple forms — don't pay the cost).
   */
  pages?: WikiTreeNode[];
}): JSX.Element {
  const wikiItems: WikiLinkItem[] = useMemo(
    () => (pages ? flattenWikiTree(pages) : []),
    [pages],
  );
  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        heading: { levels: [1, 2, 3] },
      }),
      Link.configure({
        openOnClick: false,
        autolink: true,
        HTMLAttributes: { rel: 'noopener noreferrer' },
      }),
      Markdown.configure({
        // Keep the document as markdown in storage; render as HTML
        // in the editor.
        html: false,
        breaks: true,
        transformPastedText: true,
        transformCopiedText: true,
      }),
      ...(pages
        ? [
            WikiLinkSuggestion({
              getItems: () => wikiItems,
            }),
          ]
        : []),
    ],
    content: value,
    editorProps: {
      attributes: {
        class: 'prose dark:prose-invert max-w-none focus:outline-none min-h-[300px] px-4 py-3',
        'data-placeholder': placeholder ?? 'Press / for commands…',
      },
    },
    onUpdate: ({ editor: ed }) => {
      const md = (
        ed.storage as unknown as { markdown: { getMarkdown: () => string } }
      ).markdown.getMarkdown();
      onChange(md);
    },
  });

  // When the loaded value changes (page switch in the sidebar) push
  // the new markdown into the editor. We avoid an infinite loop by
  // comparing against the editor's current markdown view.
  useEffect(() => {
    if (!editor) return;
    const md = (
      editor.storage as unknown as { markdown: { getMarkdown: () => string } }
    ).markdown.getMarkdown();
    if (value !== md) {
      editor.commands.setContent(value || '', { emitUpdate: false });
    }
  }, [value, editor]);

  if (!editor) {
    return (
      <div className="rounded border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900 p-6 text-sm text-slate-500">
        Loading editor…
      </div>
    );
  }

  return (
    <div className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 overflow-hidden">
      <BubbleMenu
        editor={editor}
        className="flex items-center gap-0.5 rounded-md border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 shadow-lg px-1 py-0.5 text-xs"
      >
        <Btn
          active={editor.isActive('bold')}
          onClick={() => editor.chain().focus().toggleBold().run()}
          title="Bold"
        >
          <strong>B</strong>
        </Btn>
        <Btn
          active={editor.isActive('italic')}
          onClick={() => editor.chain().focus().toggleItalic().run()}
          title="Italic"
        >
          <em>I</em>
        </Btn>
        <Btn
          active={editor.isActive('strike')}
          onClick={() => editor.chain().focus().toggleStrike().run()}
          title="Strikethrough"
        >
          <s>S</s>
        </Btn>
        <Sep />
        <Btn
          active={editor.isActive('heading', { level: 1 })}
          onClick={() => editor.chain().focus().toggleHeading({ level: 1 }).run()}
          title="Heading 1"
        >
          H1
        </Btn>
        <Btn
          active={editor.isActive('heading', { level: 2 })}
          onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}
          title="Heading 2"
        >
          H2
        </Btn>
        <Btn
          active={editor.isActive('heading', { level: 3 })}
          onClick={() => editor.chain().focus().toggleHeading({ level: 3 }).run()}
          title="Heading 3"
        >
          H3
        </Btn>
        <Sep />
        <Btn
          active={editor.isActive('bulletList')}
          onClick={() => editor.chain().focus().toggleBulletList().run()}
          title="Bullet list"
        >
          •
        </Btn>
        <Btn
          active={editor.isActive('orderedList')}
          onClick={() => editor.chain().focus().toggleOrderedList().run()}
          title="Numbered list"
        >
          1.
        </Btn>
        <Btn
          active={editor.isActive('blockquote')}
          onClick={() => editor.chain().focus().toggleBlockquote().run()}
          title="Quote"
        >
          “
        </Btn>
        <Btn
          active={editor.isActive('codeBlock')}
          onClick={() => editor.chain().focus().toggleCodeBlock().run()}
          title="Code block"
        >
          {}
        </Btn>
        <Sep />
        <LinkBtn editor={editor} />
      </BubbleMenu>

      <EditorContent editor={editor} />
    </div>
  );
}

function Btn({
  active,
  onClick,
  title,
  children,
}: {
  active?: boolean;
  onClick: () => void;
  title: string;
  children?: React.ReactNode;
}): JSX.Element {
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      className={`px-2 py-1 rounded hover:bg-slate-200 dark:hover:bg-slate-700 ${
        active ? 'bg-orenda-100 dark:bg-orenda-900/40 text-orenda-700 dark:text-orenda-300' : ''
      }`}
    >
      {children}
    </button>
  );
}

function Sep(): JSX.Element {
  return <span className="w-px h-5 bg-slate-200 dark:bg-slate-700 mx-0.5" />;
}

function LinkBtn({ editor }: { editor: ReturnType<typeof useEditor> }): JSX.Element {
  if (!editor) return <></>;
  return (
    <button
      type="button"
      title="Link"
      onClick={() => {
        const prev = editor.getAttributes('link').href as string | undefined;
        const url = window.prompt('URL', prev ?? 'https://');
        if (url === null) return;
        if (url === '') {
          editor.chain().focus().unsetLink().run();
          return;
        }
        editor.chain().focus().extendMarkRange('link').setLink({ href: url }).run();
      }}
      className={`px-2 py-1 rounded hover:bg-slate-200 dark:hover:bg-slate-700 ${
        editor.isActive('link') ? 'bg-orenda-100 dark:bg-orenda-900/40 text-orenda-700' : ''
      }`}
    >
      🔗
    </button>
  );
}
