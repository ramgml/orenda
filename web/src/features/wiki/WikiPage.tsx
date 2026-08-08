import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { api, type WikiPage, type WikiTreeNode } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'

/**
 * /wiki — sidebar tree + page view + editor.
 *
 * Phase 5 ships:
 *   - Tree view (hierarchy from /pages)
 *   - Page view + inline editor (markdown text area)
 *   - Backlinks panel below the content
 *   - Auto-save on blur (PUT /pages/:slug)
 */
export function WikiPage(): JSX.Element {
  const { slug } = useParams<{ slug: string }>()
  const [tree, setTree] = useState<WikiTreeNode[]>([])
  const [page, setPage] = useState<WikiPage | null>(null)
  const [backlinks, setBacklinks] = useState<WikiPage[]>([])
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
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

  return (
    <section className="grid md:grid-cols-[240px,1fr] gap-4 h-full">
      <aside className="rounded border border-slate-200 dark:border-slate-800 p-3 overflow-auto max-h-[80vh]">
        <h2 className="text-sm font-semibold mb-2 text-slate-500">Wiki</h2>
        <WikiTree tree={tree} current={slug} />
      </aside>

      <main className="space-y-3">
        {error && (
          <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
            {error}
          </div>
        )}

        {page === null ? (
          <p className="text-slate-500">Select a page or create one.</p>
        ) : (
          <>
            <div className="flex items-center justify-between">
              <input
                type="text"
                value={page.title}
                onChange={(e) => {
                  setPage({ ...page, title: e.target.value })
                  setDirty(true)
                }}
                className="text-2xl font-semibold bg-transparent border-b border-transparent focus:border-orenda-500 focus:outline-none"
              />
              <div className="flex items-center gap-2 text-xs">
                {dirty && <span className="text-amber-600">unsaved</span>}
                <button
                  type="button"
                  onClick={onSave}
                  disabled={saving || !dirty}
                  className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white"
                >
                  {saving ? 'Saving…' : 'Save'}
                </button>
              </div>
            </div>

            <div className="text-xs text-slate-500 font-mono">/wiki/{page.slug}</div>

            <textarea
              value={page.content_md ?? ''}
              onChange={(e) => {
                setPage({ ...page, content_md: e.target.value })
                setDirty(true)
              }}
              onBlur={onSave}
              rows={18}
              placeholder="# Markdown content. Use [[slug]] to link other pages."
              className="w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent font-mono text-sm"
            />

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
        )}
      </main>
    </section>
  )
}

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