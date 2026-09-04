import { useLocation, useNavigate } from 'react-router';
import { useDraggable } from '@dnd-kit/core';

import { openTaskModal } from '@/features/tasks/TaskModal';
import { TaskNumberChip } from '@/features/tasks/TaskNumberChip';
import { TaskTagChips } from '@/features/tasks/TaskTagChip';
import { useAgents } from '@/shared/hooks/useAgents';
import type { Agent, Task } from '@/shared/api/client';

import {
  dueStateClasses,
  formatDueDate,
  isStatusBlocked,
  priorityBorderClass,
  progressLabel,
  taskDueState,
} from './taskCardBadges';
import { TimeBadge } from './TimeBadge';

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
 *   - Assignee: agent=🤖 + name, user=initials. Always visible at the
 *     bottom of the card so a long agent name never squeezes the title.
 *   - Tags: existing chips (Phase 13).
 *   - Blocked: red badge if Phase 15 blockers count > 0.
 *
 * Density: a localStorage toggle ("compact" / "detailed") hides the
 * secondary counters and progress. Default is "detailed". The assignee
 * chip stays visible in both modes — see the render() body for how the
 * compact-mode fallback re-mounts the chip on its own line.
 */
export function TaskCard({
  task,
  onOpen,
  dragHandleProps,
}: {
  task: Task;
  /**
   * Called when the user clicks the card without starting a drag.
   * Left optional so non-kanban consumers can render read-only cards.
   */
  onOpen?: (taskId: string) => void;
  /**
   * T118: when the parent supplies sortable listeners (kanban board,
   * where cards are SortableContext items for same-column reorder),
   * they replace the built-in useDraggable wiring so the card has
   * ONE drag identity. Inbox/consumers without a sortable parent keep
   * the legacy useDraggable behaviour unchanged.
   */
  dragHandleProps?: Record<string, unknown>;
}): JSX.Element {
  const navigate = useNavigate();
  const location = useLocation();
  const fallback = useDraggable({ id: task.id });
  const draggable = dragHandleProps
    ? { attributes: {}, listeners: undefined, setNodeRef: undefined, isDragging: false }
    : fallback;

  const detailed = !useCompactMode();

  // Phase 28.19: AssigneeChip wants the agent's free-form labels
  // for the title attribute. One query per page, dedup'd by
  // TanStack Query across every TaskCard on the board.
  const { data: agents } = useAgents();
  const assignedAgent =
    task.assignee_type === 'agent' && agents
      ? agents.find((a) => a.id === task.assignee_id)
      : undefined;

  function handleClick(): void {
    if (onOpen) {
      onOpen(task.id);
    } else {
      openTaskModal(navigate, location, task.id);
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLDivElement>): void {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleClick();
    }
  }

  const stripeStyle: React.CSSProperties | undefined = task.color
    ? { borderLeftColor: task.color }
    : undefined;

  const priorityBorder = priorityBorderClass(task.priority);
  const dueState = taskDueState(task);
  const counters = task.counters;
  const blockedBy = task.blocked_by_count ?? 0;

  return (
    <div
      ref={draggable.setNodeRef}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      {...draggable.attributes}
      {...(draggable.listeners ?? {})}
      {...(dragHandleProps ?? {})}
      style={stripeStyle}
      data-testid="task-card"
      className={`w-full min-w-0 rounded border border-l-4 border-border bg-background p-2 text-sm cursor-grab select-none ${priorityBorder} ${
        draggable.isDragging ? 'opacity-40 border-orenda-500' : ''
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
          <div className="text-foreground">
            <TaskNumberChip number={task.number} />{' '}
            <span className="break-words">{task.title}</span>
          </div>
        </div>
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
          {/* Task #34: assignee chip moved here from the title row
              so a long agent name (Phase 28.19's labels) can no
              longer squeeze the title into a narrow column. Sits
              between the due and awaiting badges — close to
              awaiting as requested, but the visual order keeps the
              primary time signal (due) first. */}
          <AssigneeChip task={task} agent={assignedAgent} />
          {task.awaiting && task.awaiting !== 'none' && (
            <span
              data-testid="awaiting-badge"
              className="inline-flex items-center px-1.5 py-0.5 rounded border bg-blue-100 text-blue-800 border-blue-300"
            >
              ⏳ {task.awaiting === 'human' ? 'me' : 'agent'}
            </span>
          )}
          {isStatusBlocked(task) && (
            <span
              data-testid="blocked-badge"
              className="inline-flex items-center px-1.5 py-0.5 rounded border bg-red-100 text-red-700 border-red-300"
              title={
                blockedBy > 0
                  ? task.blockers && task.blockers.length > 0
                    ? task.blockers
                        .map((b) => (b.number > 0 ? `#${b.number} ` : '') + b.title)
                        .join('\n')
                    : `${blockedBy} unfinished blocker${blockedBy === 1 ? '' : 's'}`
                  : 'Blocked by a dependency (was ' + (task.blocked_prev_status || 'unknown') + ')'
              }
            >
              🚫 blocked{blockedBy > 0 ? ` ${blockedBy}` : ''}
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
          {/* Phase 30.12: time-spent / estimate badge. Renders only
              when at least one of the two is set; turns red when
              spent exceeds the estimate (operator has overrun). The
              active-timer "●" replaces the spent count when a
              started_at timestamp is present without a completed_at
              (single-active-timer constraint, Phase 4). Hidden in
              compact mode to keep the row height stable. */}
          {detailed &&
            (task.time_estimate_s != null ||
              task.time_spent_s > 0 ||
              (task.started_at != null && task.completed_at == null)) && (
              <TimeBadge
                estimateS={task.time_estimate_s ?? null}
                spentS={task.time_spent_s}
                timerActive={task.started_at != null && task.completed_at == null}
              />
            )}
        </div>
      )}

      {/* Task #34: in compact mode the badges row above is hidden, so
          the assignee signal would disappear entirely. Re-mount the
          chip on its own line so it stays visible at every density.
          AssigneeChip returns null when no assignee is set, so an
          unassigned task adds zero extra height. */}
      {!detailed && <AssigneeChip task={task} agent={assignedAgent} />}
    </div>
  );
}

/**
 * Render the assignee chip. Agents get a 🤖 + name; users get
 * initials. Hidden when no assignee.
 *
 * Task #34: previously rendered in the title row, where a long
 * agent name would compress the title into a narrow column. The
 * chip now lives at the bottom of the card (inside the detailed
 * badges row, or on its own line in compact mode) — see TaskCard
 * for the placement.
 *
 * Phase 28.19: when the agent record is available (TaskCard looks it
 * up via useAgents), the title attribute carries the agent's name +
 * its free-form label set, so hovering surfaces "agent-qwen-alpha
 * (qwen, installer)" instead of an opaque id. The chip's visible
 * label stays the id slice so dense columns remain scannable.
 */
function AssigneeChip({ task, agent }: { task: Task; agent?: Agent }): JSX.Element | null {
  if (!task.assignee_type || !task.assignee_id) return null;
  if (task.assignee_type === 'agent') {
    const labelSuffix = agent && agent.type.length > 0 ? ` (${agent.type.join(', ')})` : '';
    const title = agent ? `Agent: ${agent.name}${labelSuffix}` : `Agent: ${task.assignee_id}`;
    const display = agent?.name ?? task.assignee_id.slice(0, 6);
    return (
      <span
        data-testid="assignee-agent"
        className="inline-flex items-center min-w-0 max-w-full px-1.5 py-0.5 rounded text-[10px] bg-violet-100 text-violet-700 border border-violet-300"
        title={title}
      >
        🤖 <span className="ml-0.5 truncate max-w-[6rem]">{display}</span>
      </span>
    );
  }
  const initials = task.assignee_id.replace(/-/g, '').slice(0, 2).toUpperCase();
  return (
    <span
      data-testid="assignee-user"
      className="inline-flex items-center justify-center h-5 w-5 rounded-full text-[10px] bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-200 font-medium"
      title={`User: ${task.assignee_id}`}
    >
      {initials}
    </span>
  );
}

/**
 * Compact mode persisted in localStorage. No-op on first render.
 * Returns false (detailed) for SSR / non-browser contexts.
 */
function useCompactMode(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem('orenda.kanban.cardDensity') === 'compact';
  } catch {
    return false;
  }
}
