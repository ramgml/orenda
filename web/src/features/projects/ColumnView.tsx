import { FormEvent, useState } from 'react'
import { useDroppable } from '@dnd-kit/core'
import { useLocation, useNavigate } from 'react-router-dom'

import { TaskCard } from './TaskCard'
import { openTaskModal } from '@/features/tasks/TaskModal'
import { api, type Column, type Task } from '@/shared/api/client'
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
  onColumnUpdated,
}: {
  columnId: string
  name: string
  projectId: string
  tasks: Task[]
  onCreate: (title: string) => Promise<void>
  onColumnUpdated?: (col: Column) => void
}): JSX.Element {
  const { setNodeRef, isOver } = useDroppable({ id: columnId })
  const navigate = useNavigate()
  const location = useLocation()
  const [creating, setCreating] = useState(false)
  const [title, setTitle] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)

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

  function openTask(taskId: string): void {
    openTaskModal(navigate, location, taskId)
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
        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-400">{tasks.length}</span>
          <button
            type="button"
            onClick={() => setEditing(true)}
            title="Edit column"
            className="text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 text-sm leading-none"
          >
            ⚙
          </button>
        </div>
      </div>

      <ul className="space-y-2 flex-1">
        {tasks.map((t) => (
          <li key={t.id}>
            <TaskCard task={t} onOpen={openTask} />
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

      {editing && (
        <EditColumnModal
          columnId={columnId}
          initialName={name}
          onClose={() => setEditing(false)}
          onSaved={(col) => {
            setEditing(false)
            onColumnUpdated?.(col)
          }}
        />
      )}
    </div>
  )
}

interface EditColumnModalProps {
  columnId: string
  initialName: string
  onClose: () => void
  onSaved: (col: Column) => void
}

/** Small inline form to rename a column, change its color and set WIP limit. */
function EditColumnModal({
  columnId,
  initialName,
  onClose,
  onSaved,
}: EditColumnModalProps): JSX.Element {
  const [name, setName] = useState(initialName)
  const [color, setColor] = useState('#94a3b8')
  const [wip, setWip] = useState<string>('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!name.trim()) {
      setError('Name is required')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const wipNum = wip === '' ? null : parseInt(wip, 10)
      if (wip !== '' && Number.isNaN(wipNum)) {
        setError('WIP limit must be a number')
        setBusy(false)
        return
      }
      const col = await api.updateColumn(columnId, {
        name: name.trim(),
        color,
        wip_limit: wipNum,
      })
      onSaved(col)
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      // axios 422: extract server message if present
      setError(/wip_limit_too_small/.test(msg) ? 'WIP limit is below the current task count.' : msg)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
      <div className="bg-white dark:bg-slate-900 rounded-lg shadow-xl max-w-sm w-full p-5 space-y-3">
        <h3 className="font-semibold">Edit column</h3>
        <form onSubmit={submit} className="space-y-2">
          <label className="block text-sm">
            Name
            <input
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="mt-1 block w-full px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            />
          </label>
          <label className="block text-sm">
            Color
            <input
              type="color"
              value={color}
              onChange={(e) => setColor(e.target.value)}
              className="mt-1 block w-12 h-8 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            />
          </label>
          <label className="block text-sm">
            WIP limit (empty = no limit)
            <input
              type="number"
              min="0"
              value={wip}
              onChange={(e) => setWip(e.target.value)}
              placeholder="unlimited"
              className="mt-1 block w-full px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            />
          </label>
          {error && <p className="text-xs text-red-600">{error}</p>}
          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 rounded border border-slate-300 dark:border-slate-700 text-sm"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={busy}
              className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
            >
              {busy ? 'Saving…' : 'Save'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}