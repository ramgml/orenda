import { useLocation, useNavigate } from 'react-router-dom'
import { useDraggable } from '@dnd-kit/core'

import { openTaskModal } from '@/features/tasks/TaskModal'
import { TaskTagChips } from '@/features/tasks/TaskTagChip'
import type { Task } from '@/shared/api/client'

import {
  dueStateClasses,
  formatDueDate,
  isBlocked,
  priorityBorderClass,
  progressLabel,
  taskDueState,
} from './taskCardBadges'

/**
 * Single draggable task card.
 *
 * Phase 17 (rich cards): the front of a card now answers most
 * "what's on this thing?" questions at a glance:
 *
 *   - Priority: 3px left border colour (urgent=red, high=orange, low=slate).
 *   - Due badge: state-coloured (overdue=red, today=amber, done=green).
 *   - Awaiting: small badge when awaiting='human' or 'agent'.
 *   - Progress: children (↳) and checklist (☑) ratios when > 0.
 *   - Counters: 💬 N comments, 📎 N attachments.
 *   - Assignee: agent=🤖 + name, user=initials.
 *   - Tags: existing chips (Phase 13).
 *   - Blocked: red badge if Phase 15 blockers count > 0.
 *
 * Density: a localStorage toggle ("compact" / "detailed") hides the
 * secondary counters and progress. Default is "detailed".
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

  const detailed = !useCompactMode()

  function handleClick(): void {
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
    ? { borderLeftColor: task.color }
    : undefined

  const priorityBorder = priorityBorderClass(task.priority)
  const dueState = taskDueState(task)
  const counters = task.counters
  const blockedBy = task.blocked_by_count ?? 0

  return (
    <div
      ref={setNodeRef}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      {...attributes}
      {...listeners}
      style={stripeStyle}
      data-testid="task-card"
      className={`rounded border border-l-4 border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-950 p-2 text-sm cursor-grab select-none ${priorityBorder} ${
        isDragging ? 'opacity-40 border-orenda-500' : ''
      }`}
    >
      <div className="flex items-start gap-1">
        <div className="flex-1 min-w-0">
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
        </div>
        <AssigneeChip task={task} />
      </div>

      {task.tags && task.tags.length > 0 && detailed && (
        <TaskTagChips tags={task.tags} className="mt-1.5" />
      )}

      {detailed && (
        <div className="flex items-center gap-2 mt-1.5 flex-wrap text-[10px]">
          {dueState !== 'none' && task.due_at && (
            <span
              data-testid="due-badge"
              className={`inline-flex items-center px-1.5 py-0.5 rounded border ${dueStateClasses(dueState)}`}
            >
              📅 {formatDueDate(task.due_at)}
            </span>
          )}
          {task.awaiting && task.awaiting !== 'none' && (
            <span
              data-testid="awaiting-badge"
              className="inline-flex items-center px-1.5 py-0.5 rounded border bg-blue-100 text-blue-800 border-blue-300"
            >
              ⏳ {task.awaiting === 'human' ? 'me' : 'agent'}
            </span>
          )}
          {isBlocked(blockedBy) && (
            <span
              data-testid="blocked-badge"
              className="inline-flex items-center px-1.5 py-0.5 rounded border bg-red-100 text-red-700 border-red-300"
              title={`${blockedBy} unfinished blocker${blockedBy === 1 ? '' : 's'}`}
            >
              🚫 blocked {blockedBy}
            </span>
          )}
          {counters && counters.children_total > 0 && (
            <span className="text-slate-500">
              ↳ {progressLabel(counters.children_done, counters.children_total)}
            </span>
          )}
          {counters && counters.checklist_total > 0 && (
            <span className="text-slate-500">
              ☑ {progressLabel(counters.checklist_done, counters.checklist_total)}
            </span>
          )}
          {counters && counters.comments > 0 && (
            <span className="text-slate-500" title="comments">
              💬 {counters.comments}
            </span>
          )}
          {counters && counters.attachments > 0 && (
            <span className="text-slate-500" title="attachments">
              📎 {counters.attachments}
            </span>
          )}
        </div>
      )}
    </div>
  )
}

/**
 * Render the assignee corner. Agents get a 🤖 + name; users get
 * initials. Hidden when no assignee.
 */
function AssigneeChip({ task }: { task: Task }): JSX.Element | null {
  if (!task.assignee_type || !task.assignee_id) return null
  if (task.assignee_type === 'agent') {
    return (
      <span
        data-testid="assignee-agent"
        className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] bg-violet-100 text-violet-700 border border-violet-300"
        title={`Agent: ${task.assignee_id}`}
      >
        🤖 <span className="ml-0.5 truncate max-w-[6rem]">{task.assignee_id.slice(0, 6)}</span>
      </span>
    )
  }
  const initials = task.assignee_id.replace(/-/g, '').slice(0, 2).toUpperCase()
  return (
    <span
      data-testid="assignee-user"
      className="inline-flex items-center justify-center h-5 w-5 rounded-full text-[10px] bg-slate-200 text-slate-700 font-medium"
      title={`User: ${task.assignee_id}`}
    >
      {initials}
    </span>
  )
}

/**
 * Compact mode persisted in localStorage. No-op on first render.
 * Returns false (detailed) for SSR / non-browser contexts.
 */
function useCompactMode(): boolean {
  if (typeof window === 'undefined') return false
  try {
    return window.localStorage.getItem('orenda.kanban.cardDensity') === 'compact'
  } catch {
    return false
  }
}