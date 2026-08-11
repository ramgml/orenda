import { useLocation, useNavigate } from 'react-router-dom'
import { useDraggable } from '@dnd-kit/core'

import { openTaskModal } from '@/features/tasks/TaskModal'
import { TaskTagChips } from '@/features/tasks/TaskTagChip'
import type { Task } from '@/shared/api/client'

/**
 * Single draggable task card.
 *
 * Implementation note — draggable + click navigation:
 *  We deliberately do NOT wrap the card in `<a href>` or `<Link>`.
 *  `@dnd-kit/core`'s `useDraggable` listeners spread onto the wrapper
 *  register `onPointerDown`, which in some browsers cancels the
 *  pointer-up that would have fired the anchor's click. The symptom
 *  was: clicking a task on the kanban did not navigate at all (or in
 *  the dev-server case, kicked the user back to the dashboard via
 *  the SPA fallback). The fix is to handle navigation through
 *  `onClick` + `useNavigate`, which @dnd-kit leaves alone as long as
 *  `PointerSensor` doesn't start a real drag (activation distance is
 *  set to 4px in KanbanBoard.tsx, so taps pass through untouched).
 *
 * Modal-on-top:
 *  Clicking the card opens the task as a Trello-style overlay via
 *  `openTaskModal` instead of replacing the kanban with a full-page
 *  route. The parent (ColumnView) keeps its scroll position, the
 *  dnd state, and the filter chips visible behind the modal.
 *
 * Phase 13 visual additions:
 *  - Left colour stripe: 3px wide, derived from `task.color`; absent
 *    when colour is empty (the default for most tasks). Phase 17
 *    will fold priority into the stripe too; Phase 13 keeps the two
 *    channels independent so they don't conflict.
 *  - Tag chips below the title: rendered through the dumb
 *    `TaskTagChips` component, which Phase 17 may also reuse inside
 *    its bigger card redesign.
 */
export function TaskCard({
  task,
  onOpen,
}: {
  task: Task
  /**
   * Called when the user clicks the card without starting a drag.
   * Left optional so non-kanban consumers can render read-only cards.
   */
  onOpen?: (taskId: string) => void
}): JSX.Element {
  const navigate = useNavigate()
  const location = useLocation()
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: task.id,
  })

  function handleClick(): void {
    // Don't block programmatically if no opener is wired up; that way
    // the card is still useful in non-clickable contexts (e.g. read
    // previews in dashboards).
    if (onOpen) {
      onOpen(task.id)
    } else {
      openTaskModal(navigate, location, task.id)
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLDivElement>): void {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      handleClick()
    }
  }

  const stripeStyle: React.CSSProperties | undefined = task.color
    ? { borderLeftWidth: '3px', borderLeftColor: task.color, borderLeftStyle: 'solid' as const }
    : undefined

  return (
    <div
      ref={setNodeRef}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      {...attributes}
      {...listeners}
      style={stripeStyle}
      className={`rounded border bg-white dark:bg-slate-950 p-2 text-sm cursor-grab select-none ${
        isDragging ? 'opacity-40 border-orenda-500' : 'border-slate-200 dark:border-slate-700'
      }`}
    >
      {task.parent_task_id && (
        <span
          className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-slate-400 mb-1"
          title="This is a child task"
        >
          <span aria-hidden="true">↳</span>
          child
        </span>
      )}
      <div className="text-slate-800 dark:text-slate-100">{task.title}</div>
      {task.tags && task.tags.length > 0 && (
        <TaskTagChips tags={task.tags} className="mt-1.5" />
      )}
    </div>
  )
}
