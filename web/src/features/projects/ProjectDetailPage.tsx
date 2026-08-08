import { FormEvent, useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'

import { api, type ProjectBoard, type Task } from '@/shared/api/client'

/**
 * /projects/:id — single project board view.
 *
 * Phase 1 renders columns + their tasks in read-only mode; drag-and-drop
 * lands in Phase 2. Inline task creation is already wired so the user has
 * something to click while waiting.
 */
export function ProjectDetailPage(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const [board, setBoard] = useState<ProjectBoard | null>(null)
  const [tasks, setTasks] = useState<Task[]>([])
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState<string | null>(null) // column_id
  const [title, setTitle] = useState('')

  useEffect(() => {
    if (!id) return
    let cancelled = false
    ;(async () => {
      try {
        const b = await api.getBoard(id)
        if (cancelled) return
        setBoard(b)
        const all = await api.listProjectTasks(id)
        if (cancelled) return
        setTasks(all)
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [id])

  if (error) {
    return <p className="text-red-700">{error}</p>
  }
  if (!board) {
    return <p className="text-slate-500">Loading…</p>
  }

  const tasksByCol = new Map<string, Task[]>()
  for (const t of tasks) {
    const list = tasksByCol.get(t.column_id ?? '') ?? []
    list.push(t)
    tasksByCol.set(t.column_id ?? '', list)
  }

  async function onCreate(e: FormEvent<HTMLFormElement>, columnId: string): Promise<void> {
    e.preventDefault()
    if (!id || !title.trim()) return
    try {
      const t = await api.createTask(id, { title: title.trim(), column_id: columnId })
      setTasks((prev) => [...prev, t])
      setTitle('')
      setCreating(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <section>
      <h1 className="text-2xl font-semibold mb-4">{board.board.project_id.slice(0, 8)}…</h1>

      <div className="grid grid-cols-1 md:grid-cols-5 gap-3">
        {board.columns.map((col) => {
          const colTasks = tasksByCol.get(col.id) ?? []
          return (
            <div
              key={col.id}
              className="rounded-lg border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900 p-3 flex flex-col"
            >
              <div className="flex items-center justify-between mb-2">
                <h2 className="font-medium text-sm uppercase tracking-wide text-slate-600 dark:text-slate-300">
                  {col.name}
                </h2>
                <span className="text-xs text-slate-400">{colTasks.length}</span>
              </div>

              <ul className="space-y-2 flex-1">
                {colTasks.map((t) => (
                  <li
                    key={t.id}
                    className="rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-950 p-2 text-sm"
                  >
                    {t.title}
                  </li>
                ))}
              </ul>

              {creating === col.id ? (
                <form onSubmit={(e) => onCreate(e, col.id)} className="mt-2 flex gap-1">
                  <input
                    autoFocus
                    value={title}
                    onChange={(e) => setTitle(e.target.value)}
                    placeholder="New task"
                    className="flex-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-sm"
                  />
                  <button
                    type="submit"
                    className="px-2 py-1 rounded bg-orenda-600 text-white text-xs"
                  >
                    Add
                  </button>
                </form>
              ) : (
                <button
                  type="button"
                  onClick={() => setCreating(col.id)}
                  className="mt-2 text-xs text-slate-500 hover:text-orenda-600 self-start"
                >
                  + Add task
                </button>
              )}
            </div>
          )
        })}
      </div>
    </section>
  )
}