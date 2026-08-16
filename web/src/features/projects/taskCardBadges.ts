/**
 * Phase 17: pure functions that compute the visual state of a task card.
 *
 * The helpers are split out from TaskCard so they're unit-testable
 * without React. TaskCard just maps the result to JSX.
 */

/** Possible visual states for the due-date badge. */
export type DueState = 'overdue' | 'today' | 'upcoming' | 'done' | 'none';

/**
 * Classify a task's due date for the badge. `now` is passed in so
 * tests can pin time without monkey-patching Date.
 */
export function taskDueState(
  task: {
    due_at?: string | null;
    status?: string;
    completed_at?: string | null;
  },
  now: Date = new Date(),
): DueState {
  if (task.status === 'done' || task.completed_at) return 'done';
  if (!task.due_at) return 'none';
  const due = new Date(task.due_at);
  if (Number.isNaN(due.getTime())) return 'none';
  const startOfDay = (d: Date): Date => {
    const x = new Date(d);
    x.setHours(0, 0, 0, 0);
    return x;
  };
  const a = startOfDay(due).getTime();
  const b = startOfDay(now).getTime();
  if (a < b) return 'overdue';
  if (a === b) return 'today';
  return 'upcoming';
}

/** Tailwind class for the due-date badge background. */
export function dueStateClasses(state: DueState): string {
  switch (state) {
    case 'overdue':
      return 'bg-red-100 text-red-700 border-red-300';
    case 'today':
      return 'bg-amber-100 text-amber-800 border-amber-300';
    case 'upcoming':
      return 'bg-slate-100 text-slate-600 border-slate-300';
    case 'done':
      return 'bg-emerald-100 text-emerald-700 border-emerald-300';
    default:
      return '';
  }
}

/** Compact due-date label: "12 авг" or "12 авг 2027" for non-current-year. */
export function formatDueDate(iso: string | null | undefined, now: Date = new Date()): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const sameYear = d.getFullYear() === now.getFullYear();
  const month = d.toLocaleString('ru-RU', { month: 'short' });
  const day = d.getDate();
  return sameYear ? `${day} ${month}` : `${day} ${month} ${d.getFullYear()}`;
}

/** Tailwind class for the priority border colour. */
export function priorityBorderClass(priority: string | undefined): string {
  switch (priority) {
    case 'urgent':
      return 'border-l-red-500';
    case 'high':
      return 'border-l-orange-400';
    case 'low':
      return 'border-l-slate-300';
    default:
      return ''; // medium is invisible
  }
}

/** Build a displayable progress string for children / checklist. */
export function progressLabel(done: number, total: number): string {
  if (total <= 0) return '';
  return `${done}/${total}`;
}

/** Should the "blocked" badge render? */
export function isBlocked(blockedByCount: number | undefined): boolean {
  return (blockedByCount ?? 0) > 0;
}
