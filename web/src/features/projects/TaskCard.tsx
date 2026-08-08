import { useDraggable } from '@dnd-kit/core'

import type { Task } from '@/shared/api/client'

/**
 * Single draggable task card.
 *
 * `useDraggable` from @dnd-kit registers the element; we apply the
 * attributes/listeners to a wrapper div. The drag itself is handled at the
 * DndContext level (in KanbanBoard) — this component just reports what
 * id was grabbed.
 */
export function TaskCard({ task }: { task: Task }): JSX.Element {
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: task.id,
  })

  return (
    <div
      ref={setNodeRef}
      {...listeners}
      {...attributes}
      className={`rounded border bg-white dark:bg-slate-950 p-2 text-sm cursor-grab select-none ${
        isDragging ? 'opacity-40 border-orenda-500' : 'border-slate-200 dark:border-slate-700'
      }`}
    >
      {task.title}
    </div>
  )
}