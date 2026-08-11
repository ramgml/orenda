import { useEffect, useState } from 'react'

import { api, type Tag } from '@/shared/api/client'
import { TaskTagChip } from './TaskTagChip'

/**
 * Tag editor for the task sidebar.
 *
 * Behaviour:
 *   - Loads the global tag catalogue once on mount.
 *   - Renders each catalogue tag as a toggle (chip-style); tags the
 *     task already has are pre-checked.
 *   - Inline "+ new tag" form at the bottom creates a tag and
 *     immediately attaches it to the task. Colour defaults to the
 *     tag-editor's last-used colour (or a fresh slate hue if the
 *     user hasn't picked one).
 *   - All mutations go through the dedicated endpoints (PATCH /tags
 *     for label changes, DELETE /tags for removal). We do NOT batch
 *     these into PATCH /tasks/{id} — the dedicated endpoints give
 *     proper 409 feedback on duplicate names.
 *
 * State design:
 *   - `selected` is the set of tag IDs currently on the task
 *     (canonical source of truth for the UI between saves).
 *   - `catalogue` is the full list of available tags.
 *   - `saving` is a coarse-grained lock during setTaskTags so the
 *     user doesn't double-save when clicking fast.
 */
export function TagsList({ taskId, initial }: { taskId: string; initial: Tag[] }): JSX.Element {
  const [catalogue, setCatalogue] = useState<Tag[]>([])
  const [selected, setSelected] = useState<Set<string>>(() => new Set(initial.map((t) => t.id)))
  const [saving, setSaving] = useState(false)
  const [newName, setNewName] = useState('')
  const [newColor, setNewColor] = useState('#3b82f6')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const r = await api.listTags()
        if (cancelled) return
        setCatalogue(r.tags ?? [])
      } catch (e) {
        if (!cancelled) {
          setError(e instanceof Error ? e.message : String(e))
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  // Keep `selected` in sync when the parent task reloads (e.g. WS
  // event). Use the tag id set rather than full equality so we
  // tolerate reorderings and the chip render staying stable.
  useEffect(() => {
    setSelected(new Set(initial.map((t) => t.id)))
  }, [initial])

  async function persist(next: Set<string>): Promise<void> {
    setSaving(true)
    setError(null)
    try {
      const ids = Array.from(next)
      const r = await api.setTaskTags(taskId, ids)
      setSelected(new Set((r.tags ?? []).map((t) => t.id)))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  async function toggle(tag: Tag): Promise<void> {
    const next = new Set(selected)
    if (next.has(tag.id)) {
      next.delete(tag.id)
    } else {
      next.add(tag.id)
    }
    setSelected(next)
    await persist(next)
  }

  async function createAndAttach(): Promise<void> {
    const name = newName.trim()
    if (!name) return
    setCreating(true)
    setError(null)
    try {
      const created = await api.createTag({ name, color: newColor })
      // Add to local catalogue so the toggle appears immediately.
      setCatalogue((cur) => {
        if (cur.some((t) => t.id === created.id)) return cur
        const next = [...cur, created]
        next.sort((a, b) => a.name.localeCompare(b.name))
        return next
      })
      const next = new Set(selected)
      next.add(created.id)
      setSelected(next)
      await persist(next)
      setNewName('')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setCreating(false)
    }
  }

  const selectedList = catalogue.filter((t) => selected.has(t.id))

  return (
    <div className="rounded border border-slate-200 dark:border-slate-800 p-3 space-y-2">
      <div className="flex items-center justify-between">
        <p className="text-xs text-slate-500">Tags</p>
        {saving && <span className="text-[10px] text-slate-400">saving…</span>}
      </div>

      {selectedList.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {selectedList.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => void toggle(t)}
              disabled={saving}
              className="inline-flex items-center gap-1 hover:opacity-70 disabled:opacity-50"
              title={`Click to remove "${t.name}"`}
            >
              <TaskTagChip tag={t} />
            </button>
          ))}
        </div>
      ) : (
        <p className="text-xs text-slate-400 italic">No tags yet.</p>
      )}

      {catalogue.some((t) => !selected.has(t.id)) && (
        <details className="text-xs">
          <summary className="cursor-pointer text-slate-500 hover:text-slate-700">
            Add existing tag ({catalogue.length - selectedList.length})
          </summary>
          <div className="mt-2 flex flex-wrap gap-1">
            {catalogue
              .filter((t) => !selected.has(t.id))
              .map((t) => (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => void toggle(t)}
                  disabled={saving}
                  className="inline-flex items-center hover:opacity-70 disabled:opacity-50"
                  title={`Attach "${t.name}"`}
                >
                  <TaskTagChip tag={t} />
                </button>
              ))}
          </div>
        </details>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault()
          void createAndAttach()
        }}
        className="flex gap-1 items-center pt-1 border-t border-slate-100 dark:border-slate-800"
      >
        <input
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          placeholder="new tag"
          disabled={creating || saving}
          className="flex-1 min-w-0 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-xs"
          maxLength={50}
        />
        <input
          type="color"
          value={newColor}
          onChange={(e) => setNewColor(e.target.value)}
          disabled={creating || saving}
          className="h-6 w-7 rounded border border-slate-300 dark:border-slate-700 bg-transparent cursor-pointer"
          title="Pick a colour"
        />
        <button
          type="submit"
          disabled={creating || saving || !newName.trim()}
          className="px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
        >
          + add
        </button>
      </form>

      {error && <p className="text-xs text-red-600">{error}</p>}
    </div>
  )
}