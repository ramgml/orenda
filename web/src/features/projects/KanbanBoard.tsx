import { FormEvent, useEffect, useMemo, useState } from 'react'
import {
  DndContext,
  DragEndEvent,
  DragOverlay,
  DragStartEvent,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
} from '@dnd-kit/core'
import {
  SortableContext,
  arrayMove,
  horizontalListSortingStrategy,
  useSortable,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'

import { useAuth } from '@/features/auth/AuthContext'
import { api, type Column, type Task } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'
import { queueMoveTask } from '@/shared/offline/outbox'

import { ColumnView } from './ColumnView'
import { TaskCard } from './TaskCard'

/**
 * Kanban board for one project: drag-and-drop columns AND tasks via
 * @dnd-kit. Phase 12 added column-level reordering and the "+ Add
 * column" affordance.
 *
 * Two DnD layers share the same DndContext:
 *  - Task drag: `activeTask.id` matches a task id; drop target is a
 *    column id (the existing column droppable).
 *  - Column drag: `activeColumnId` matches a column id; drop target is
 *    another column id; we use SortableContext(horizontal) and reorder
 *    the columns array, then PATCH the moved column with a midpoint
 *    position.
 *
 * Tasks are loaded on mount and after every WS event; on drop we update
 * optimistically and POST to the move endpoint. On failure we revert.
 *
 * Child tasks (Phase 14: rows with `parent_task_id` set) live on the
 * board alongside their parents — they aren't hidden behind the
 * "ChildTasksList" UI anymore. By default they show up; the toggle at
 * the top of the board lets the user hide them when the board gets
 * crowded. Their cards carry a small "↳ child" badge so they stand
 * out from top-level work.
 *
 * Tasks whose `column_id` is NULL (legacy after migration 013, or any
 * future bug that lets one slip through) fall back to the first
 * column instead of disappearing entirely. The backend's
 * `createTaskHandler` also defaults new child tasks to the parent's
 * column so this fallback should be rare in practice.
 */
export function KanbanBoard({
  projectId,
  columns,
}: {
  projectId: string
  columns: Column[]
}): JSX.Element {
  // useAuth is consumed only to keep the WS hook's context alive.
  useAuth()
  const [tasks, setTasks] = useState<Task[]>([])
  const [cols, setCols] = useState<Column[]>(columns)
  const [activeTask, setActiveTask] = useState<Task | null>(null)
  const [error, setError] = useState<string | null>(null)
  // Persist the toggle in localStorage so the user's choice survives
  // navigation. Defaults to true (show) per Phase 14 UX request.
  const [showChildren, setShowChildren] = useState<boolean>(() => {
    if (typeof window === 'undefined') return true
    const v = window.localStorage.getItem('orenda.kanban.showChildren')
    return v === null ? true : v === 'true'
  })

  useEffect(() => {
    if (typeof window === 'undefined') return
    window.localStorage.setItem('orenda.kanban.showChildren', String(showChildren))
  }, [showChildren])

  // Keep local cols in sync if the parent re-fetches the board.
  useEffect(() => {
    setCols(columns)
  }, [columns])

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
  )

  async function load(): Promise<void> {
    try {
      setTasks(await api.listProjectTasks(projectId))
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId])

  // Re-fetch on every task/column event. Simple, correct, and
  // acceptable at Phase 2/12 scale (one owner, one board, <1k tasks).
  useWebSocketTopic('tasks', () => {
    load()
  })

  function isColumnId(id: string): boolean {
    return cols.some((c) => c.id === id)
  }

  function onDragStart(ev: DragStartEvent): void {
    if (isColumnId(String(ev.active.id))) return // column drag handled in onDragEnd
    const t = tasks.find((x) => x.id === ev.active.id)
    if (t) setActiveTask(t)
  }

  async function onDragEnd(ev: DragEndEvent): Promise<void> {
    setActiveTask(null)
    const activeId = String(ev.active.id)
    const overId = ev.over ? String(ev.over.id) : null
    if (!overId) return

    // Column reorder: both endpoints are columns.
    if (isColumnId(activeId) && isColumnId(overId) && activeId !== overId) {
      await reorderColumns(activeId, overId)
      return
    }

    // Task move into a column.
    const targetColumnId = overId
    const current = tasks.find((t) => t.id === activeId)
    if (!current || current.column_id === targetColumnId) return

    const prev = tasks
    setTasks((cur) =>
      cur.map((t) => (t.id === activeId ? { ...t, column_id: targetColumnId } : t)),
    )

    try {
      // Phase Wave 4 PR 2: route the move through the offline
      // outbox when the client is disconnected, so a dnd-while-
      // offline lands the position on the server once connectivity
      // returns. Online path is the same as before (axios call
      // catches the error and the optimistic update stays in place).
      if (typeof navigator !== 'undefined' && !navigator.onLine) {
        await queueMoveTask(activeId, targetColumnId)
      } else {
        await api.moveTask(activeId, targetColumnId)
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setTasks(prev)
    }
  }

  /**
   * Reorder columns locally + PATCH the moved column with a position
   * computed from its new neighbours. On PATCH failure we revert by
   * re-fetching the board (the WS broadcast on column update would do
   * it anyway, but doing it inline makes the failure feel immediate).
   */
  async function reorderColumns(activeId: string, overId: string): Promise<void> {
    const fromIdx = cols.findIndex((c) => c.id === activeId)
    const toIdx = cols.findIndex((c) => c.id === overId)
    if (fromIdx < 0 || toIdx < 0 || fromIdx === toIdx) return

    const reordered = arrayMove(cols, fromIdx, toIdx)
    const prev = cols
    setCols(reordered)

    // Midpoint between the columns now surrounding the moved one.
    const before = toIdx > 0 ? reordered[toIdx - 1].position : null
    const after = toIdx < reordered.length - 1 ? reordered[toIdx + 1].position : null
    let newPos: number
    if (before != null && after != null) {
      newPos = (before + after) / 2
    } else if (before != null) {
      // Moved to the very end.
      newPos = before + 1024
    } else if (after != null) {
      // Moved to the very front.
      newPos = after - 1024
    } else {
      // Single-column board — position is moot.
      newPos = 1024
    }

    try {
      const updated = await api.updateColumn(activeId, { position: newPos })
      setCols((cur) => cur.map((c) => (c.id === updated.id ? updated : c)))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setCols(prev)
    }
  }

  // Build per-column buckets. Tasks without a column land in the first
  // column so they're still visible (defensive — should be rare
  // since the backend defaults child tasks to the parent's column).
  const tasksByCol = useMemo(() => {
    const map = new Map<string, Task[]>()
    const fallback = cols[0]?.id
    const visible = showChildren ? tasks : tasks.filter((t) => !t.parent_task_id)
    for (const t of visible) {
      const k = t.column_id ?? fallback ?? ''
      if (!k) continue
      const list = map.get(k) ?? []
      list.push(t)
      map.set(k, list)
    }
    return map
  }, [tasks, cols, showChildren])

  const childCount = useMemo(() => tasks.filter((t) => !!t.parent_task_id).length, [tasks])

  return (
    <div className="space-y-3">
      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}
      <div className="flex items-center justify-between">
        <label className="flex items-center gap-2 text-xs text-slate-500 cursor-pointer">
          <input
            type="checkbox"
            checked={showChildren}
            onChange={(e) => setShowChildren(e.target.checked)}
            className="rounded border-slate-300"
          />
          <span>
            Show child tasks{' '}
            <span className="text-slate-400">
              ({childCount})
            </span>
          </span>
        </label>
      </div>
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragStart={onDragStart}
        onDragEnd={onDragEnd}
      >
        <SortableContext
          items={cols.map((c) => c.id)}
          strategy={horizontalListSortingStrategy}
        >
          <div className="grid grid-cols-1 md:grid-cols-5 gap-3">
            {cols.map((col) => (
              <SortableColumnView
                key={col.id}
                column={col}
                projectId={projectId}
                tasks={tasksByCol.get(col.id) ?? []}
                onCreate={async (title) => {
                  const t = await api.createTask(projectId, { title, column_id: col.id })
                  setTasks((cur) => [...cur, t])
                }}
                onColumnUpdated={(updated) =>
                  setCols((cur) => cur.map((c) => (c.id === updated.id ? updated : c)))
                }
                onColumnDeleted={(colId) => {
                  // Phase 12.6: drop the column locally so the UI
                  // updates immediately; the WS broadcast does the
                  // same on every other tab.
                  setCols((cur) => cur.filter((c) => c.id !== colId))
                  setTasks((cur) => cur.filter((t) => t.column_id !== colId))
                }}
              />
            ))}
            <AddColumnTile projectId={projectId} onCreated={(c) => setCols((cur) => [...cur, c])} />
          </div>
        </SortableContext>
        <DragOverlay>
          {activeTask ? <TaskCard task={activeTask} /> : null}
        </DragOverlay>
      </DndContext>
    </div>
  )
}

/**
 * Sortable wrapper around ColumnView that provides dnd-kit handle
 * listeners via useSortable. The column itself remains a useDroppable
 * target (for task drops) — useSortable's setNodeRef composes with
 * useDroppable's setNodeRef via forwarding ref. We pass the listeners
 * down to ColumnView's header via dragHandleProps.
 */
function SortableColumnView({
  column,
  projectId,
  tasks,
  onCreate,
  onColumnUpdated,
  onColumnDeleted,
}: {
  column: Column
  projectId: string
  tasks: Task[]
  onCreate: (title: string) => Promise<void>
  onColumnUpdated: (col: Column) => void
  onColumnDeleted: (colId: string) => void
}): JSX.Element {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: column.id })
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  }
  return (
    <div ref={setNodeRef} style={style} {...attributes}>
      <ColumnView
        columnId={column.id}
        projectId={projectId}
        name={column.name}
        tasks={tasks}
        // Phase 27.10: pipe the saved colour + WIP into ColumnView
        // so the header renders the dot and the edit modal opens
        // with the persisted values (rename then Save no longer
        // wipes the colour).
        color={column.color}
        wipLimit={column.wip_limit}
        onCreate={onCreate}
        onColumnUpdated={onColumnUpdated}
        onColumnDeleted={onColumnDeleted}
        dragHandleProps={listeners}
      />
    </div>
  )
}

/**
 * "+ Add column" affordance at the end of the board. Inline form: name +
 * optional color, submit calls api.createColumn. Optimistic append on
 * success; on failure shows the error inline.
 */
function AddColumnTile({
  projectId,
  onCreated,
}: {
  projectId: string
  onCreated: (col: Column) => void
}): JSX.Element {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [color, setColor] = useState('#94a3b8')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) {
      setError('Name is required')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const col = await api.createColumn(projectId, { name: trimmed, color })
      onCreated(col)
      setName('')
      setColor('#94a3b8')
      setOpen(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        data-testid="add-column-tile"
        className="rounded-lg border border-dashed border-slate-300 dark:border-slate-700 bg-transparent hover:bg-slate-50 dark:hover:bg-slate-900 text-xs text-slate-500 hover:text-orenda-600 min-h-[200px] flex items-center justify-center"
      >
        + Add column
      </button>
    )
  }

  return (
    <form
      onSubmit={submit}
      data-testid="add-column-form"
      className="rounded-lg border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900 p-3 flex flex-col gap-2 min-h-[200px]"
    >
      <input
        autoFocus
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Column name"
        className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-sm"
      />
      <label className="flex items-center gap-2 text-xs text-slate-500">
        Color
        <input
          type="color"
          value={color}
          onChange={(e) => setColor(e.target.value)}
          className="w-10 h-6 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
        />
      </label>
      {error && <p className="text-xs text-red-600">{error}</p>}
      <div className="flex gap-1 mt-auto">
        <button
          type="submit"
          disabled={busy}
          className="flex-1 px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
        >
          {busy ? 'Adding…' : 'Add'}
        </button>
        <button
          type="button"
          onClick={() => {
            setOpen(false)
            setError(null)
            setName('')
          }}
          className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 text-xs"
        >
          Cancel
        </button>
      </div>
    </form>
  )
}