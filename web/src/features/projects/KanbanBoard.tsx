import { useEffect, useState } from 'react'
import {
  DndContext,
  DragEndEvent,
  DragOverlay,
  DragStartEvent,
  PointerSensor,
  useSensor,
  useSensors,
} from '@dnd-kit/core'

import { useAuth } from '@/features/auth/AuthContext'
import { api, type Column, type Task } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'

import { ColumnView } from './ColumnView'
import { TaskCard } from './TaskCard'

/**
 * Kanban board for one project: 5 columns + drag-and-drop via @dnd-kit/core.
 *
 * Tasks are loaded on mount and after every WS event; on drop we update
 * optimistically and POST to the move endpoint. On failure we revert.
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

  // Re-fetch on every task event. Simple, correct, and acceptable at
  // Phase 2 scale (one owner, one board, <1k tasks).
  useWebSocketTopic('tasks', () => {
    load()
  })

  function onDragStart(ev: DragStartEvent): void {
    const t = tasks.find((x) => x.id === ev.active.id)
    if (t) setActiveTask(t)
  }

  async function onDragEnd(ev: DragEndEvent): Promise<void> {
    setActiveTask(null)
    const taskId = String(ev.active.id)
    const targetColumnId = ev.over ? String(ev.over.id) : null
    if (!targetColumnId) return

    const current = tasks.find((t) => t.id === taskId)
    if (!current || current.column_id === targetColumnId) return

    const prev = tasks
    setTasks((cur) =>
      cur.map((t) => (t.id === taskId ? { ...t, column_id: targetColumnId } : t)),
    )

    try {
      await api.moveTask(taskId, targetColumnId)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setTasks(prev)
    }
  }

  const tasksByCol = new Map<string, Task[]>()
  for (const t of tasks) {
    const k = t.column_id ?? ''
    const list = tasksByCol.get(k) ?? []
    list.push(t)
    tasksByCol.set(k, list)
  }

  return (
    <div className="space-y-3">
      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}
      <DndContext sensors={sensors} onDragStart={onDragStart} onDragEnd={onDragEnd}>
        <div className="grid grid-cols-1 md:grid-cols-5 gap-3">
          {cols.map((col) => (
            <ColumnView
              key={col.id}
              columnId={col.id}
              projectId={projectId}
              name={col.name}
              tasks={tasksByCol.get(col.id) ?? []}
              onCreate={async (title) => {
                const t = await api.createTask(projectId, { title, column_id: col.id })
                setTasks((cur) => [...cur, t])
              }}
              onColumnUpdated={(updated) =>
                setCols((cur) => cur.map((c) => (c.id === updated.id ? updated : c)))
              }
            />
          ))}
        </div>
        <DragOverlay>
          {activeTask ? <TaskCard task={activeTask} /> : null}
        </DragOverlay>
      </DndContext>
    </div>
  )
}