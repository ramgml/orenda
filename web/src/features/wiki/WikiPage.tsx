import { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

import {
  api,
  type SearchHit,
  type WikiPage,
  type WikiTreeNode,
  type WikiBlock,
} from '@/shared/api/client';
import { useWebSocketTopic } from '@/shared/ws';
import { slugify } from '@/shared/util/slug';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Textarea } from '@/shared/ui/textarea';

import { SnippetText } from '@/features/search/SearchPage';
import { BlocksEditor, type BlocksEditorHandle } from './blocks/BlocksEditor';
import { WikiNumberChip } from './WikiNumberChip';

/**
 * /wiki — sidebar tree + markdown editor + preview.
 *
 * Layout:
 *   ┌─ sidebar (260px) ─┬─ editor ────────────────────────────┐
 *   │ New page form      │ title input + Save/Delete toolbar   │
 *   │ (slug + Create)    │ Write / Preview tabs                │
 *   │ ─────────          │ Write: textarea + markdown toolbar  │
 *   │ WikiTree           │ Preview: react-markdown render      │
 *   └────────────────────┴──────────────────────────────────────┘
 *
 * Phase 5 wired tree + save + backlinks; Phase 6+ adds the new-page
 * form (slug input) and a proper markdown editor with live preview.
 */
export function WikiPage(): JSX.Element {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  const [tree, setTree] = useState<WikiTreeNode[]>([]);
  const [page, setPage] = useState<WikiPage | null>(null);
  const [backlinks, setBacklinks] = useState<WikiPage[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [blockView, setBlockView] = useState<{
    format: string;
    blocks?: WikiBlock[];
    content_md?: string;
  } | null>(null);
  const blocksEditorRef = useRef<BlocksEditorHandle | null>(null);

  async function loadTree(): Promise<void> {
    try {
      const t = await api.listPages();
      setTree(t.tree ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function loadPage(slugVal: string): Promise<void> {
    try {
      const p = await api.getPageBySlug(slugVal);
      const bl = await api.getPageBacklinks(slugVal);
      setPage(p);
      setBacklinks(bl.backlinks ?? []);
      // B2 fix: always fetch blocks — legacy markdown pages get
      // format="markdown" + content_md; blocks pages get format="blocks" + blocks
      try {
        const bv = await api.getPageBlocks(slugVal);
        setBlockView(bv);
      } catch {
        setBlockView(null);
      }
      setError(null);
    } catch (e) {
      if ((e as { response?: { status?: number } }).response?.status === 404) {
        // Slug not yet present — show an empty editor to create it.
        setPage({
          id: '',
          slug: slugVal,
          title: slugVal,
          content_md: '',
          position: 0,
          number: 0,
          created_at: '',
          updated_at: '',
        });
        setBacklinks([]);
        setError(null);
      } else {
        setError(e instanceof Error ? e.message : String(e));
      }
    }
  }

  useEffect(() => {
    loadTree();
    if (slug) loadPage(slug);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug]);

  useWebSocketTopic('wiki', () => {
    loadTree();
    if (slug) loadPage(slug);
  });

  async function onSave(): Promise<void> {
    if (!page || !page.slug) return;
    setSaving(true);
    try {
      // If blocks editor is active, save as blocks
      if (page.id && blocksEditorRef.current) {
        const blocks = blocksEditorRef.current.getDocument();
        const saved = await api.updatePageBlocks(page.slug, blocks);
        setPage(saved);
        setBlockView({ format: 'blocks', blocks });
      } else if (page.id) {
        await api.updatePage(page.slug, {
          title: page.title,
          content_md: page.content_md,
        });
      } else {
        await api.savePage({
          slug: page.slug,
          title: page.title,
          content_md: page.content_md,
        });
      }
      setDirty(false);
      await loadPage(page.slug);
      await loadTree();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  async function onDelete(): Promise<void> {
    if (!page || !page.id) return;
    if (!window.confirm(`Delete "${page.title}"? This cannot be undone.`)) return;
    setDeleting(true);
    setError(null);
    try {
      await api.deletePage(page.slug);
      setPage(null);
      await loadTree();
      navigate('/wiki');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setDeleting(false);
    }
  }

  return (
    <section className="grid md:grid-cols-[260px,1fr] gap-4 h-full">
      <WikiSidebar
        tree={tree}
        current={slug}
        onCreated={(newSlug) => navigate(`/wiki/${newSlug}`)}
        onRefresh={loadTree}
      />

      <main className="space-y-3 min-w-0">
        {error && (
          <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
            {error}
          </div>
        )}

        {page === null ? (
          <EmptyState />
        ) : (
          <PageEditor
            page={page}
            backlinks={backlinks}
            dirty={dirty}
            saving={saving}
            deleting={deleting}
            pagesTree={tree}
            blockView={blockView}
            blocksEditorRef={blocksEditorRef}
            onChange={(p) => {
              setPage(p);
              setDirty(true);
            }}
            onDirty={() => setDirty(true)}
            onSave={onSave}
            onDelete={onDelete}
          />
        )}
      </main>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Sidebar — new-page form + tree
// ---------------------------------------------------------------------------

function WikiSidebar({
  tree,
  current,
  onCreated,
  onRefresh,
}: {
  tree: WikiTreeNode[];
  current?: string;
  onCreated: (newSlug: string) => void;
  onRefresh: () => void;
}): JSX.Element {
  const navigate = useNavigate();
  // The form accepts a free-text title (any language). We auto-derive
  // the slug via slugify() and let the user override it if they want a
  // custom URL. Once the user touches the slug field we stop
  // regenerating it from the title.
  const [title, setTitle] = useState('');
  const [slugOverride, setSlugOverride] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const autoSlug = slugify(title);
  const slug = (slugOverride ?? autoSlug).trim().toLowerCase();

  // --- Sidebar search (tree filter + FTS content matches) -------------
  // One input, two modes: an instant client-side title filter over the
  // tree, and a debounced FTS query that appends a "Content matches"
  // section under the tree. The URL is deliberately left alone — the
  // search is a sidebar-local affordance, not a route.
  const [query, setQuery] = useState('');
  const trimmedQuery = query.trim();
  const [ftsHits, setFtsHits] = useState<SearchHit[] | null>(null);
  const [searchErr, setSearchErr] = useState<string | null>(null);
  // Monotonic id of the in-flight request; only the latest response is
  // applied, so a slow earlier reply can never overwrite a newer one.
  const requestIdRef = useRef(0);

  const filteredTree = useMemo(() => {
    if (!trimmedQuery) return tree;
    const q = trimmedQuery.toLowerCase();
    const keep = (nodes: WikiTreeNode[]): WikiTreeNode[] =>
      nodes
        .map((node): WikiTreeNode | null => {
          const children = keep(node.children ?? []);
          const self = node.page.title.toLowerCase().includes(q);
          if (!self && children.length === 0) return null;
          return { ...node, children };
        })
        .filter((n): n is WikiTreeNode => n !== null);
    return keep(tree);
  }, [tree, trimmedQuery]);

  useEffect(() => {
    if (!trimmedQuery) {
      // Empty query: hide the FTS section and drop any stale request.
      requestIdRef.current += 1;
      setFtsHits(null);
      setSearchErr(null);
      return;
    }
    const id = ++requestIdRef.current;
    const timer = setTimeout(() => {
      api
        .search({ q: trimmedQuery, type: 'page', limit: 20 })
        .then((res) => {
          if (requestIdRef.current === id) {
            setFtsHits(res.hits);
            setSearchErr(null);
          }
        })
        .catch((e: unknown) => {
          if (requestIdRef.current === id) {
            setSearchErr(e instanceof Error ? e.message : String(e));
          }
        });
    }, 300);
    return () => clearTimeout(timer);
  }, [trimmedQuery]);

  async function submitNewPage(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    const t = title.trim();
    if (!t) return;
    if (!/^[a-z0-9_-]+$/.test(slug)) {
      setErr('Slug must contain only [a-z0-9_-].');
      return;
    }
    setCreating(true);
    setErr(null);
    try {
      // Pre-create so the page is visible in the tree immediately.
      // The slug is what we sent; the backend echoes it back.
      await api.savePage({
        slug,
        title: t,
        content_md: '',
      });
      setTitle('');
      setSlugOverride(null);
      onCreated(slug);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setErr(/slug_taken/.test(msg) ? 'A page with this slug already exists.' : msg);
    } finally {
      setCreating(false);
    }
  }

  return (
    <aside className="rounded border border-border p-3 overflow-auto max-h-[80vh] space-y-3">
      <form onSubmit={submitNewPage} className="space-y-1.5">
        <h2 className="text-sm font-semibold text-slate-500">New page</h2>
        <Input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Page title (any language)"
          className="text-sm"
          autoFocus
        />
        <div className="flex items-center gap-1">
          <span className="text-xs text-slate-500 font-mono shrink-0">/wiki/</span>
          <Input
            type="text"
            value={slug}
            onChange={(e) => {
              const v = e.target.value;
              // If the user edits the slug we treat it as an explicit
              // override and stop regenerating from the title. If they
              // clear it back to the auto value we follow along again.
              const isAuto = v === autoSlug;
              setSlugOverride(isAuto ? null : v);
            }}
            placeholder="auto-generated"
            className="flex-1 min-w-0 text-xs font-mono"
          />
        </div>
        <Button
          type="submit"
          disabled={creating || !title.trim()}
          className="w-full text-xs"
          size="sm"
        >
          {creating ? 'Creating…' : 'Create'}
        </Button>
        {err && <p className="text-xs text-red-600">{err}</p>}
      </form>
      <div>
        <h2 className="text-sm font-semibold text-slate-500 mb-1">Pages</h2>
        <Input
          type="text"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Filter pages…"
          aria-label="Search wiki"
          className="text-sm mb-1"
        />
        <WikiTree tree={filteredTree} current={current} onRefresh={onRefresh} />
      </div>

      {searchErr && (
        <p className="text-xs text-red-600" role="alert">
          {searchErr}
        </p>
      )}

      {trimmedQuery && ftsHits !== null && (
        <div>
          <h2 className="text-sm font-semibold text-slate-500 mb-1">Content matches</h2>
          {ftsHits.length === 0 ? (
            <p className="text-xs text-slate-500">No matches.</p>
          ) : (
            <ul className="space-y-1.5">
              {ftsHits.map((hit) => (
                <li key={hit.id}>
                  <button
                    type="button"
                    className="text-sm text-left w-full hover:underline"
                    onClick={() => {
                      navigate(`/wiki/${hit.slug ?? hit.id}`);
                    }}
                  >
                    {hit.title || hit.slug || hit.id}
                  </button>
                  <p className="text-xs text-slate-500">
                    <SnippetText text={hit.snippet} />
                  </p>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </aside>
  );
}

function EmptyState(): JSX.Element {
  return (
    <div className="rounded border border-dashed border-border p-8 text-center text-slate-500">
      <p className="text-lg mb-1">No page selected</p>
      <p className="text-sm">
        Pick a page from the tree, or type a slug in <em>New page</em> on the left to create one.
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Page editor — Notion-style Tiptap WYSIWYG with a Source tab for raw
// markdown. The editor is the live preview, so we don't need a
// separate Preview tab; the Source tab is for power users / pasting.
// ---------------------------------------------------------------------------

function PageEditor({
  page,
  backlinks,
  dirty,
  saving,
  deleting,
  pagesTree,
  blockView,
  blocksEditorRef,
  onChange,
  onSave,
  onDelete,
  onDirty,
}: {
  page: WikiPage;
  backlinks: WikiPage[];
  dirty: boolean;
  saving: boolean;
  deleting: boolean;
  pagesTree: WikiTreeNode[];
  blockView: { format: string; blocks?: WikiBlock[]; content_md?: string } | null;
  blocksEditorRef: React.RefObject<BlocksEditorHandle | null>;
  onChange: (p: WikiPage) => void;
  onDirty: () => void;
  onSave: () => void;
  onDelete: () => void;
}): JSX.Element {
  const [tab, setTab] = useState<'edit' | 'preview' | 'source'>('preview');

  return (
    <>
      <div className="flex items-center justify-between gap-3">
        <Input
          type="text"
          value={page.title}
          onChange={(e) => onChange({ ...page, title: e.target.value })}
          className="text-2xl font-semibold bg-transparent border-0 focus-visible:ring-0 px-0 flex-1 min-w-0"
          placeholder="Untitled"
        />
        {page.number > 0 && <WikiNumberChip number={page.number} />}
        <div className="flex items-center gap-2 text-xs shrink-0">
          {dirty && <span className="text-amber-600">unsaved</span>}
          <Button type="button" onClick={onSave} disabled={saving || !dirty} size="sm">
            {saving ? 'Saving…' : 'Save'}
          </Button>
          {page.id && (
            <Button
              type="button"
              onClick={onDelete}
              disabled={deleting}
              title="Delete this page"
              variant="outline"
              size="sm"
              className="text-red-700 border-red-300 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/20"
            >
              {deleting ? 'Deleting…' : 'Delete'}
            </Button>
          )}
        </div>
      </div>

      <div className="text-xs text-slate-500 font-mono">/wiki/{page.slug}</div>

      <div className="border-b border-border flex">
        <TabBtn active={tab === 'preview'} onClick={() => setTab('preview')}>
          Preview
        </TabBtn>
        <TabBtn active={tab === 'edit'} onClick={() => setTab('edit')}>
          Edit
        </TabBtn>
        <TabBtn active={tab === 'source'} onClick={() => setTab('source')}>
          Source
        </TabBtn>
      </div>

      {tab === 'edit' && blockView ? (
        <BlocksEditor
          ref={blocksEditorRef as React.RefObject<BlocksEditorHandle>}
          slug={page.slug}
          pageId={page.id}
          initialBlocks={blockView.blocks}
          initialFormat={blockView.format}
          initialContentMD={blockView.content_md}
          pagesTree={pagesTree}
          onChange={() => onDirty()}
        />
      ) : tab === 'preview' ? (
        <div className="rounded border border-border bg-background px-6 py-5 min-h-[300px]">
          {page.content_md ? (
            <article className="prose dark:prose-invert max-w-none text-sm">
              <ReactMarkdown remarkPlugins={[remarkGfm]}>{page.content_md}</ReactMarkdown>
            </article>
          ) : (
            <p className="text-slate-400 italic text-sm">Nothing to preview yet.</p>
          )}
        </div>
      ) : (
        <Textarea
          value={page.content_md ?? ''}
          onChange={(e) => onChange({ ...page, content_md: e.target.value })}
          rows={22}
          spellCheck={false}
          className="w-full font-mono text-sm leading-relaxed outline-none resize-y"
        />
      )}

      {backlinks.length > 0 && (
        <section className="rounded border border-border p-3">
          <h3 className="text-sm font-semibold text-slate-500 mb-2">Backlinks</h3>
          <ul className="space-y-1 text-sm">
            {backlinks.map((b) => (
              <li key={b.id}>
                <Link to={`/wiki/${b.slug}`} className="text-orenda-600 hover:underline">
                  {b.title}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

function TabBtn({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}): JSX.Element {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`px-3 py-1.5 text-xs font-medium ${
        active
          ? 'border-b-2 border-orenda-500 text-orenda-700 dark:text-orenda-300'
          : 'text-slate-500 hover:text-slate-700'
      }`}
    >
      {children}
    </button>
  );
}

// ---------------------------------------------------------------------------
// WikiTree — recursive, collapsible, with inline child creation and
// per-page move / delete actions. Notion-style but kept simple.
//
// Each row has:
//   - chevron (only when it has children) — click to collapse
//   - icon (folder or doc) + title — click to navigate
//   - hover-revealed "+" button — add a child page under this one
//   - hover-revealed "⋯" menu — Move / Delete

function WikiTree({
  tree,
  current,
  onRefresh,
}: {
  tree: WikiTreeNode[];
  current?: string;
  onRefresh: () => void;
}): JSX.Element {
  if (tree.length === 0) {
    return <p className="text-xs text-slate-400">No pages yet.</p>;
  }
  return (
    <ul className="space-y-0.5 text-sm">
      {tree.map((n) => (
        <TreeNode key={n.page.id} node={n} current={current} onRefresh={onRefresh} depth={0} />
      ))}
    </ul>
  );
}

function TreeNode({
  node,
  current,
  onRefresh,
  depth,
}: {
  node: WikiTreeNode;
  current?: string;
  onRefresh: () => void;
  depth: number;
}): JSX.Element {
  const hasChildren = (node.children?.length ?? 0) > 0;
  const [open, setOpen] = useState(true);
  const [addingChild, setAddingChild] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [moveTarget, setMoveTarget] = useState<string | null>(null); // null = closed, '' = root picker
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onCreatedChild = (newSlug: string): void => {
    setAddingChild(false);
    onRefresh();
    // Navigate to the newly created child.
    window.location.assign(`/wiki/${newSlug}`);
  };

  async function onDelete(): Promise<void> {
    if (!window.confirm(`Delete "${node.page.title}" and all its sub-pages?`)) return;
    setBusy(true);
    try {
      await api.deletePage(node.page.slug);
      setMenuOpen(false);
      onRefresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onMoveTo(parentId: string): Promise<void> {
    setBusy(true);
    setErr(null);
    try {
      await api.movePage(node.page.slug, parentId);
      setMoveTarget(null);
      setMenuOpen(false);
      onRefresh();
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setErr(/cycle|invalid/.test(msg) ? "Can't move there (would create a cycle)." : msg);
    } finally {
      setBusy(false);
    }
  }

  return (
    <li>
      <div
        className={`group flex items-center gap-1 rounded ${
          current === node.page.slug
            ? 'bg-orenda-100 dark:bg-orenda-900/30 text-orenda-700'
            : 'hover:bg-slate-100 dark:hover:bg-slate-800'
        }`}
        style={{ paddingLeft: depth * 12 }}
      >
        {hasChildren ? (
          <Button
            type="button"
            onClick={() => setOpen((v) => !v)}
            variant="ghost"
            size="icon"
            className="h-6 w-6 px-1 text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 text-xs shrink-0"
            title={open ? 'Collapse' : 'Expand'}
          >
            {open ? '▾' : '▸'}
          </Button>
        ) : (
          <span className="w-3 shrink-0" />
        )}
        <span aria-hidden className="text-slate-400 text-xs shrink-0">
          {hasChildren ? '📁' : '📄'}
        </span>
        <Link
          to={`/wiki/${node.page.slug}`}
          className="flex-1 min-w-0 truncate px-1 py-1"
          title={node.page.title}
        >
          {node.page.title}
        </Link>
        {/* Hover-revealed action buttons */}
        <div className="flex items-center gap-0.5 pr-1 opacity-0 group-hover:opacity-100 transition-opacity">
          <Button
            type="button"
            onClick={() => setAddingChild((v) => !v)}
            variant="ghost"
            size="sm"
            className="h-6 px-1.5 text-xs"
            title="Add sub-page"
          >
            +
          </Button>
          <Button
            type="button"
            onClick={() => setMenuOpen((v) => !v)}
            variant="ghost"
            size="sm"
            className="h-6 px-1.5 text-xs"
            title="More"
          >
            ⋯
          </Button>
        </div>
      </div>

      {addingChild && (
        <div style={{ paddingLeft: (depth + 1) * 12 }} className="my-1">
          <NewPageForm
            placeholder={`Sub-page under "${node.page.title}"`}
            parentId={node.page.id}
            onCreated={onCreatedChild}
            onCancel={() => setAddingChild(false)}
          />
        </div>
      )}

      {menuOpen && (
        <div className="ml-6 my-1 rounded border border-border bg-card shadow-sm p-2 text-xs space-y-1">
          <Button
            type="button"
            onClick={() => {
              setMoveTarget('pick');
              setMenuOpen(false);
            }}
            variant="ghost"
            size="sm"
            className="w-full justify-start text-xs h-auto"
            disabled={busy}
          >
            Move to…
          </Button>
          <Button
            type="button"
            onClick={onDelete}
            variant="ghost"
            size="sm"
            className="w-full justify-start text-xs h-auto text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20"
            disabled={busy}
          >
            Delete
          </Button>
        </div>
      )}

      {moveTarget === 'pick' && (
        <MoveTargetPicker
          tree={[] /* loaded below */}
          currentNodeId={node.page.id}
          onPick={onMoveTo}
          onCancel={() => setMoveTarget(null)}
          err={err}
          busy={busy}
        />
      )}

      {hasChildren && open && (
        <ul className="space-y-0.5">
          {node.children!.map((c) => (
            <TreeNode
              key={c.page.id}
              node={c}
              current={current}
              onRefresh={onRefresh}
              depth={depth + 1}
            />
          ))}
        </ul>
      )}
    </li>
  );
}

// NewPageForm — inline title input + auto-slug, used both at the
// sidebar root and inside a TreeNode (as a sub-page creator).
function NewPageForm({
  placeholder,
  parentId,
  onCreated,
  onCancel,
}: {
  placeholder: string;
  parentId?: string;
  onCreated: (slug: string) => void;
  onCancel: () => void;
}): JSX.Element {
  const [title, setTitle] = useState('');
  const [creating, setCreating] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const autoSlug = slugify(title);

  async function submit(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    const t = title.trim();
    if (!t) return;
    if (!/^[a-z0-9_-]+$/.test(autoSlug)) {
      setErr('Title must contain at least one a-z/0-9/-/_ character.');
      return;
    }
    setCreating(true);
    setErr(null);
    try {
      const saved = await api.savePage({
        slug: autoSlug,
        title: t,
        content_md: '',
        ...(parentId ? { parent_id: parentId } : {}),
      });
      onCreated(saved.slug);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setErr(/slug_taken/.test(msg) ? 'A page with this slug already exists.' : msg);
    } finally {
      setCreating(false);
    }
  }

  return (
    <form
      onSubmit={submit}
      className="rounded border border-orenda-300 bg-orenda-50/40 dark:bg-orenda-900/20 p-2 space-y-1"
      onKeyDown={(e) => {
        if (e.key === 'Escape') onCancel();
      }}
    >
      <Input
        autoFocus
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        placeholder={placeholder}
        className="text-sm"
      />
      {title && <div className="text-[10px] text-slate-500 font-mono">→ /wiki/{autoSlug}</div>}
      {err && <div className="text-xs text-red-600">{err}</div>}
      <div className="flex items-center gap-1 text-xs">
        <Button type="submit" disabled={creating || !title.trim()} size="sm" className="text-xs">
          {creating ? '…' : 'Create'}
        </Button>
        <Button
          type="button"
          onClick={onCancel}
          variant="ghost"
          size="sm"
          className="text-xs text-slate-500 hover:text-slate-700"
        >
          Cancel
        </Button>
      </div>
    </form>
  );
}

// MoveTargetPicker — list every other page (flat) the user can move
// this page under, plus a "Top level" option. Avoids cycles by
// excluding this page and its descendants (computed server-side; here
// we just rely on the backend's 400 response).
function MoveTargetPicker({
  currentNodeId,
  onPick,
  onCancel,
  err,
  busy,
}: {
  tree: WikiTreeNode[];
  currentNodeId: string;
  onPick: (parentId: string) => void;
  onCancel: () => void;
  err: string | null;
  busy: boolean;
}): JSX.Element {
  const [pages, setPages] = useState<WikiPage[] | null>(null);
  useEffect(() => {
    // Fetch the flat list once when the picker opens — the existing
    // /pages endpoint returns the tree, so we call getPage for each
    // leaf OR fall back to /pages and walk. Simpler: list every page
    // via /pages/{slug} would be N requests; instead reuse the tree
    // by flattening it.
    api.listPages().then((r) => {
      const out: WikiPage[] = [];
      const walk = (n: WikiTreeNode): void => {
        if (n.page.id !== currentNodeId) out.push(n.page);
        (n.children ?? []).forEach(walk);
      };
      (r.tree ?? []).forEach(walk);
      setPages(out);
    });
  }, [currentNodeId]);

  return (
    <div className="ml-6 my-1 rounded border border-border bg-card shadow-sm p-2 text-xs">
      <div className="font-medium mb-1">Move to…</div>
      {err && <div className="text-red-600 mb-1">{err}</div>}
      <div className="max-h-48 overflow-auto space-y-0.5">
        <Button
          type="button"
          onClick={() => onPick('')}
          disabled={busy}
          variant="ghost"
          size="sm"
          className="w-full justify-start text-xs h-auto"
        >
          ↩ Top level
        </Button>
        {pages === null && <div className="text-slate-400 px-2">Loading…</div>}
        {pages?.map((p) => (
          <Button
            key={p.id}
            type="button"
            onClick={() => onPick(p.id)}
            disabled={busy}
            variant="ghost"
            size="sm"
            className="w-full justify-start text-xs h-auto truncate"
            title={p.title}
          >
            📄 {p.title}
          </Button>
        ))}
      </div>
      <div className="mt-1">
        <Button
          type="button"
          onClick={onCancel}
          variant="ghost"
          size="sm"
          className="text-xs text-slate-500 hover:text-slate-700"
        >
          Cancel
        </Button>
      </div>
    </div>
  );
}
