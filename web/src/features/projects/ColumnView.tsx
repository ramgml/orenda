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
  onColumnDeleted,
  dragHandleProps,
}: {
  columnId: string
  name: string
  projectId: string
  tasks: Task[]
  onCreate: (title: string) => Promise<void>
  onColumnUpdated?: (col: Column) => void
  /**
   * Optional callback fired after the backend confirms a column
   * delete. Phase 12.6 uses it to drop the column from local state
   * without a full board refetch; the WS broadcast will also remove
   * the column from every other tab.
   */
  onColumnDeleted?: (colId: string) => void
  /**
   * Optional dnd-kit props for the column-as-a-whole drag handle (the
   * header area). When present, the header becomes draggable so the
   * user can reorder columns. Phase 12 wires this in via
   * KanbanBoard's horizontal SortableContext; default is undefined so
   // this component stays usable standalone (tests, future board
   // layouts that don't support column reordering).
   */
  dragHandleProps?: Record<string, unknown>
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
      <div
        className="flex items-center justify-between mb-2 cursor-grab active:cursor-grabbing select-none"
        // Drag the whole column by its header. dnd-kit's useSortable
        // (wired in KanbanBoard via dragHandleProps) gives us listeners;
        // spread them on the header so the body remains free for task
        // drops. Double-click opens the same edit modal as the ⚙ button
        // — discoverability was the point of Phase 12.4.
        {...(dragHandleProps ?? {})}
        onDoubleClick={() => setEditing(true)}
      >
        <h2 className="font-medium text-sm uppercase tracking-wide text-slate-600 dark:text-slate-300">
          {name}
        </h2>
        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-400">{tasks.length}</span>
          <button
            type="button"
            onClick={(e) => {
              // Clicks on ⚙ shouldn't start a column drag.
              e.stopPropagation()
              setEditing(true)
            }}
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
          currentTaskCount={tasks.length}
          onClose={() => setEditing(false)}
          onSaved={(col) => {
            setEditing(false)
            onColumnUpdated?.(col)
          }}
          onDeleted={() => {
            setEditing(false)
            onColumnDeleted?.(columnId)
          }}
        />
      )}
    </div>
  )
}

interface EditColumnModalProps {
  columnId: string
  initialName: string
  /** Number of tasks currently in the column — shown as a hint before
   * the user confirms the delete. Mirrors what the server will
   * enforce (422 when non-zero). */
  currentTaskCount: number
  onClose: () => void
  onSaved: (col: Column) => void
  onDeleted: () => void
}

/** Small inline form to rename a column, change its color, set WIP
 *  limit, and — since Phase 12.6 — delete the column when it's empty.
 *  Delete is gated by a two-step confirmation because it's
 *  irreversible: a stray click shouldn't be able to wipe a column. */
function EditColumnModal({
  columnId,
  initialName,
  currentTaskCount,
  onClose,
  onSaved,
  onDeleted,
}: EditColumnModalProps): JSX.Element {
  const [name, setName] = useState(initialName)
  const [color, setColor] = useState('#94a3b8')
  const [wip, setWip] = useState<string>('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // Two-step confirm: first click arms the button ("Delete column"),
  // second click actually fires. Resets if the user changes their mind
  // or types in the form fields.
  const [confirmDelete, setConfirmDelete] = useState(false)

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

  async function onDelete(): Promise<void> {
    if (currentTaskCount > 0) {
      // Defence in depth — the server is the source of truth, but
      // failing fast here saves a round-trip and makes the UX clearer.
      setError(
        `Move the ${currentTaskCount} task${currentTaskCount === 1 ? '' : 's'} in this column to another column first.`,
      )
      setConfirmDelete(false)
      return
    }
    if (!confirmDelete) {
      setConfirmDelete(true)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await api.deleteColumn(columnId)
      onDeleted()
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(/column_not_empty/.test(msg) ? 'Tasks were added to this column while you were editing. Move them out first.' : msg)
      setConfirmDelete(false)
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
              onChange={(e) => {
                setName(e.target.value)
                setConfirmDelete(false)
              }}
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
        <div className="border-t border-slate-200 dark:border-slate-800 pt-3 space-y-1">
          <p className="text-xs text-slate-500">
            {currentTaskCount === 0
              ? 'This column is empty — safe to delete.'
              : `This column holds ${currentTaskCount} task${
                  currentTaskCount === 1 ? '' : 's'
                }. Move them out first.`}
          </p>
          <button
            type="button"
            onClick={onDelete}
            disabled={busy}
            data-testid="delete-column-button"
            className={
              confirmDelete
                ? 'w-full px-3 py-1.5 rounded bg-red-600 hover:bg-red-700 disabled:opacity-50 text-white text-sm font-semibold'
                : 'w-full px-3 py-1.5 rounded border border-red-300 dark:border-red-800 text-red-700 dark:text-red-300 hover:bg-red-50 dark:hover:bg-red-900/30 disabled:opacity-50 text-sm'
            }
          >
            {confirmDelete
              ? `Click again to delete "${initialName}"`
              : 'Delete column'}
          </button>
        </div>
      </div>
    </div>
  )
}