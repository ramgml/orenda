import { FormEvent, useState } from 'react'
import { useDroppable } from '@dnd-kit/core'

import { TaskCard } from './TaskCard'
import type { Task } from '@/shared/api/client'
import { queueCreateTask } from '@/shared/offline/outbox'

/**
 * One kanban column with its droppable zone and inline task creation.
 *
 * Offline-aware: when the browser is offline, the create call is queued
 * into the IndexedDB outbox and flushed by syncNow() on reconnect.
 */
export function ColumnView({
  columnId,
  name,
  projectId,
  tasks,
  onCreate,
}: {
  columnId: string
  name: string
  projectId: string
  tasks: Task[]
  onCreate: (title: string) => Promise<void>
}): JSX.Element {
  const { setNodeRef, isOver } = useDroppable({ id: columnId })
  const [creating, setCreating] = useState(false)
  const [title, setTitle] = useState('')
  const [error, setError] = useState<string | null>(null)

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!title.trim()) return
    try {
      if (navigator.onLine) {
        await onCreate(title.trim())
      } else {
        await queueCreateTask(projectId, { title: title.trim(), column_id: columnId })
      }
      setTitle('')
      setCreating(false)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div
      ref={setNodeRef}
      className={`rounded-lg border bg-slate-50 dark:bg-slate-900 p-3 flex flex-col min-h-[200px] transition-colors ${
        isOver
          ? 'border-orenda-500 bg-orenda-50 dark:bg-orenda-900/20'
          : 'border-slate-200 dark:border-slate-800'
      }`}
    >
      <div className="flex items-center justify-between mb-2">
        <h2 className="font-medium text-sm uppercase tracking-wide text-slate-600 dark:text-slate-300">
          {name}
        </h2>
        <span className="text-xs text-slate-400">{tasks.length}</span>
      </div>

      <ul className="space-y-2 flex-1">
        {tasks.map((t) => (
          <li key={t.id}>
            <a
              href={`/tasks/${t.id}`}
              onClick={(e) => {
                // Drag-and-drop handles its own click suppression via the
                // activation constraint; regular clicks navigate.
                if (e.defaultPrevented) return
              }}
              className="block"
            >
              <TaskCard task={t} />
            </a>
          </li>
        ))}
      </ul>

      {creating ? (
        <form onSubmit={submit} className="mt-2 flex gap-1">
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
          {error && <span className="text-xs text-red-600">{error}</span>}
        </form>
      ) : (
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="mt-2 text-xs text-slate-500 hover:text-orenda-600 self-start"
        >
          + Add task
        </button>
      )}
    </div>
  )
}