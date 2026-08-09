import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'

import { api, type WikiPage, type WikiTreeNode } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'
import { slugify } from '@/shared/util/slug'

import { MarkdownEditor } from './MarkdownEditor'

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
  const { slug } = useParams<{ slug: string }>()
  const navigate = useNavigate()
  const [tree, setTree] = useState<WikiTreeNode[]>([])
  const [page, setPage] = useState<WikiPage | null>(null)
  const [backlinks, setBacklinks] = useState<WikiPage[]>([])
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [dirty, setDirty] = useState(false)

  async function loadTree(): Promise<void> {
    try {
      const t = await api.listPages()
      setTree(t.tree ?? [])
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  async function loadPage(slugVal: string): Promise<void> {
    try {
      const p = await api.getPageBySlug(slugVal)
      const bl = await api.getPageBacklinks(slugVal)
      setPage(p)
      setBacklinks(bl.backlinks ?? [])
      setError(null)
    } catch (e) {
      if ((e as { response?: { status?: number } }).response?.status === 404) {
        // Slug not yet present — show an empty editor to create it.
        setPage({
          id: '',
          slug: slugVal,
          title: slugVal,
          content_md: '',
          position: 0,
          created_at: '',
          updated_at: '',
        })
        setBacklinks([])
        setError(null)
      } else {
        setError(e instanceof Error ? e.message : String(e))
      }
    }
  }

  useEffect(() => {
    loadTree()
    if (slug) loadPage(slug)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug])

  useWebSocketTopic('wiki', () => {
    loadTree()
    if (slug) loadPage(slug)
  })

  async function onSave(): Promise<void> {
    if (!page || !page.slug) return
    setSaving(true)
    try {
      if (page.id) {
        await api.updatePage(page.slug, {
          title: page.title,
          content_md: page.content_md,
        })
      } else {
        await api.savePage({
          slug: page.slug,
          title: page.title,
          content_md: page.content_md,
        })
      }
      setDirty(false)
      await loadPage(page.slug)
      await loadTree()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function onDelete(): Promise<void> {
    if (!page || !page.id) return
    if (!window.confirm(`Delete "${page.title}"? This cannot be undone.`)) return
    setDeleting(true)
    setError(null)
    try {
      await api.deletePage(page.slug)
      setPage(null)
      await loadTree()
      navigate('/wiki')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <section className="grid md:grid-cols-[260px,1fr] gap-4 h-full">
      <WikiSidebar
        tree={tree}
        current={slug}
        onCreated={(newSlug) => navigate(`/wiki/${newSlug}`)}
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
            onChange={(p) => {
              setPage(p)
              setDirty(true)
            }}
            onSave={onSave}
            onDelete={onDelete}
          />
        )}
      </main>
    </section>
  )
}

// ---------------------------------------------------------------------------
// Sidebar — new-page form + tree
// ---------------------------------------------------------------------------

function WikiSidebar({
  tree,
  current,
  onCreated,
}: {
  tree: WikiTreeNode[]
  current?: string
  onCreated: (newSlug: string) => void
}): JSX.Element {
  // The form accepts a free-text title (any language). We auto-derive
  // the slug via slugify() and let the user override it if they want a
  // custom URL. Once the user touches the slug field we stop
  // regenerating it from the title.
  const [title, setTitle] = useState('')
  const [slugOverride, setSlugOverride] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const autoSlug = slugify(title)
  const slug = (slugOverride ?? autoSlug).trim().toLowerCase()

  async function submitNewPage(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    const t = title.trim()
    if (!t) return
    if (!/^[a-z0-9_-]+$/.test(slug)) {
      setErr('Slug must contain only [a-z0-9_-].')
      return
    }
    setCreating(true)
    setErr(null)
    try {
      // Pre-create so the page is visible in the tree immediately.
      // The slug is what we sent; the backend echoes it back.
      await api.savePage({
        slug,
        title: t,
        content_md: '',
      })
      setTitle('')
      setSlugOverride(null)
      onCreated(slug)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setErr(/slug_taken/.test(msg) ? 'A page with this slug already exists.' : msg)
    } finally {
      setCreating(false)
    }
  }

  return (
    <aside className="rounded border border-slate-200 dark:border-slate-800 p-3 overflow-auto max-h-[80vh] space-y-3">
      <form onSubmit={submitNewPage} className="space-y-1.5">
        <h2 className="text-sm font-semibold text-slate-500">New page</h2>
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Page title (any language)"
          className="w-full px-2 py-1 text-sm rounded border border-slate-300 dark:border-slate-700 bg-transparent"
          autoFocus
        />
        <div className="flex items-center gap-1">
          <span className="text-xs text-slate-500 font-mono shrink-0">/wiki/</span>
          <input
            type="text"
            value={slug}
            onChange={(e) => {
              const v = e.target.value
              // If the user edits the slug we treat it as an explicit
              // override and stop regenerating from the title. If they
              // clear it back to the auto value we follow along again.
              const isAuto = v === autoSlug
              setSlugOverride(isAuto ? null : v)
            }}
            placeholder="auto-generated"
            className="flex-1 min-w-0 px-2 py-1 text-xs font-mono rounded border border-slate-200 dark:border-slate-700 bg-transparent"
          />
        </div>
        <button
          type="submit"
          disabled={creating || !title.trim()}
          className="w-full px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
        >
          {creating ? 'Creating…' : 'Create'}
        </button>
        {err && <p className="text-xs text-red-600">{err}</p>}
      </form>

      <div>
        <h2 className="text-sm font-semibold text-slate-500 mb-1">Pages</h2>
        <WikiTree tree={tree} current={current} />
      </div>
    </aside>
  )
}

function EmptyState(): JSX.Element {
  return (
    <div className="rounded border border-dashed border-slate-300 dark:border-slate-700 p-8 text-center text-slate-500">
      <p className="text-lg mb-1">No page selected</p>
      <p className="text-sm">
        Pick a page from the tree, or type a slug in <em>New page</em> on the left
        to create one.
      </p>
    </div>
  )
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
  onChange,
  onSave,
  onDelete,
}: {
  page: WikiPage
  backlinks: WikiPage[]
  dirty: boolean
  saving: boolean
  deleting: boolean
  onChange: (p: WikiPage) => void
  onSave: () => void
  onDelete: () => void
}): JSX.Element {
  const [tab, setTab] = useState<'edit' | 'source'>('edit')

  return (
    <>
      <div className="flex items-center justify-between gap-3">
        <input
          type="text"
          value={page.title}
          onChange={(e) => onChange({ ...page, title: e.target.value })}
          className="text-2xl font-semibold bg-transparent border-b border-transparent focus:border-orenda-500 focus:outline-none flex-1 min-w-0"
          placeholder="Untitled"
        />
        <div className="flex items-center gap-2 text-xs shrink-0">
          {dirty && <span className="text-amber-600">unsaved</span>}
          <button
            type="button"
            onClick={onSave}
            disabled={saving || !dirty}
            className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white"
          >
            {saving ? 'Saving…' : 'Save'}
          </button>
          {page.id && (
            <button
              type="button"
              onClick={onDelete}
              disabled={deleting}
              title="Delete this page"
              className="px-3 py-1.5 rounded border border-red-300 text-red-700 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/20 disabled:opacity-50"
            >
              {deleting ? 'Deleting…' : 'Delete'}
            </button>
          )}
        </div>
      </div>

      <div className="text-xs text-slate-500 font-mono">/wiki/{page.slug}</div>

      <div className="border-b border-slate-200 dark:border-slate-800 flex">
        <TabBtn active={tab === 'edit'} onClick={() => setTab('edit')}>
          Edit
        </TabBtn>
        <TabBtn active={tab === 'source'} onClick={() => setTab('source')}>
          Markdown
        </TabBtn>
      </div>

      {tab === 'edit' ? (
        <MarkdownEditor
          value={page.content_md ?? ''}
          onChange={(md) => onChange({ ...page, content_md: md })}
          placeholder="Type / for commands…"
        />
      ) : (
        <textarea
          value={page.content_md ?? ''}
          onChange={(e) => onChange({ ...page, content_md: e.target.value })}
          rows={22}
          spellCheck={false}
          className="w-full px-4 py-3 rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 font-mono text-sm leading-relaxed outline-none focus:border-orenda-500 resize-y"
        />
      )}

      {backlinks.length > 0 && (
        <section className="rounded border border-slate-200 dark:border-slate-800 p-3">
          <h3 className="text-sm font-semibold text-slate-500 mb-2">Backlinks</h3>
          <ul className="space-y-1 text-sm">
            {backlinks.map((b) => (
              <li key={b.id}>
                <Link
                  to={`/wiki/${b.slug}`}
                  className="text-orenda-600 hover:underline"
                >
                  {b.title}
                </Link>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  )
}

function TabBtn({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
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
  )
}

// ---------------------------------------------------------------------------
// WikiTree — recursive page list
// ---------------------------------------------------------------------------

function WikiTree({ tree, current }: { tree: WikiTreeNode[]; current?: string }): JSX.Element {
  if (tree.length === 0) {
    return <p className="text-xs text-slate-400">No pages yet.</p>
  }
  return (
    <ul className="space-y-1 text-sm">
      {tree.map((n) => (
        <li key={n.page.id}>
          <Link
            to={`/wiki/${n.page.slug}`}
            className={`block px-2 py-1 rounded ${
              current === n.page.slug
                ? 'bg-orenda-100 dark:bg-orenda-900/30 text-orenda-700'
                : 'hover:bg-slate-100 dark:hover:bg-slate-800'
            }`}
          >
            {n.page.title}
          </Link>
          {n.children && n.children.length > 0 && (
            <ul className="ml-3 mt-1 space-y-1 border-l border-slate-200 dark:border-slate-700 pl-2">
              {n.children.map((c) => (
                <li key={c.page.id}>
                  <Link
                    to={`/wiki/${c.page.slug}`}
                    className={`block px-2 py-1 rounded ${
                      current === c.page.slug
                        ? 'bg-orenda-100 dark:bg-orenda-900/30 text-orenda-700'
                        : 'hover:bg-slate-100 dark:hover:bg-slate-800'
                    }`}
                  >
                    {c.page.title}
                  </Link>
                  {c.children && c.children.length > 0 && (
                    <div className="ml-3">
                      <WikiTree tree={c.children} current={current} />
                    </div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  )
}