import { useCallback, useEffect, type ReactNode } from 'react'
import {
  Link,
  useLocation,
  useNavigate,
  useParams,
  type Location,
  type NavigateFunction,
} from 'react-router-dom'

import { TaskViewBody } from './TaskViewBody'

/**
 * Trello-style modal overlay for a single task.
 *
 * The component mounts on top of whatever page was rendered before
 * (kanban / activity / search) thanks to the `backgroundLocation`
 * trick in App.tsx: when a task is opened via `openTaskModal()` or
 * `<TaskLink>`, the underlying page stays rendered behind a dimmed
 * backdrop. Closing the modal pops the history entry and reveals the
 * page again without losing scroll / filters / dnd state.
 *
 * Closing is possible three ways:
 *   - Esc key
 *   - click on the backdrop (outside the modal box)
 *   - the × button
 *   - browser back (history pop)
 *
 * Direct deep-links to /tasks/:id (no background state) fall back to
 * the full TaskViewPage route — see App.tsx.
 */
export function TaskModal(): JSX.Element | null {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const close = useCallback(() => {
    // `navigate(-1)` pops the entry the modal pushed when opening, so
    // the user lands back on the page that was behind it. If they
    // opened the modal from outside any history (e.g. pasted a deep
    // link that the router still resolved here), -1 lands at "/"
    // which is the dashboard — still a sane place to be.
    navigate(-1)
  }, [navigate])

  // Esc closes. We attach to `window` (not the modal) so a focused
  // input doesn't swallow the keydown — `Escape` already means
  // "cancel" inside inputs, and a second Escape then closes the
  // modal. The native input-handler in TaskViewBody consumes the
  // first Escape; this listener catches the second.
  useEffect(() => {
    function onKey(e: KeyboardEvent): void {
      if (e.key === 'Escape') {
        e.stopPropagation()
        close()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [close])

  if (!id) return null

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Task details"
      className="fixed inset-0 z-[60] bg-black/50 flex items-start md:items-center justify-center p-2 md:p-6 overflow-y-auto"
      onClick={close}
    >
      <div
        className="bg-white dark:bg-slate-900 rounded-lg shadow-2xl max-w-4xl w-full my-4 md:my-0 relative"
        // stopPropagation so clicks inside the modal don't bubble to
        // the backdrop (which would close the modal).
        onClick={(e) => e.stopPropagation()}
      >
        <button
          type="button"
          onClick={close}
          aria-label="Close task"
          className="absolute top-2 right-2 z-10 h-8 w-8 rounded-full flex items-center justify-center text-slate-500 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-slate-800"
        >
          ×
        </button>
        <div className="p-4 md:p-6">
          <TaskViewBody taskId={id} onClose={close} />
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Helpers used by callers (kanban card, activity link, search hit, …).
// Keeping the navigation contract in one place means the
// background-location trick works for every entry point without each
// caller having to remember the magic option.
// ---------------------------------------------------------------------------

type BackgroundState = { backgroundLocation?: Location }

/**
 * Programmatic equivalent of `<TaskLink>` — use with `useNavigate()`.
 *
 * Example:
 *   const navigate = useNavigate()
 *   const location = useLocation()
 *   <button onClick={() => openTaskModal(navigate, location, task.id)}>
 *
 * The navigation uses `replace: true` so opening a child task from
 * inside an already-open modal swaps the entry instead of stacking a
 * new one on top. This matches Trello / Linear: closing the modal
 * always pops back to whatever was on the screen before the user
 * opened the first task (the kanban, a search page, an activity
 * tab…), regardless of how many tasks they drilled through.
 */
export function openTaskModal(
  navigate: NavigateFunction,
  location: Location,
  taskId: string,
): void {
  navigate(`/tasks/${taskId}`, {
    replace: true,
    state: { backgroundLocation: location } satisfies BackgroundState,
  })
}

/**
 * `<Link>` wrapper that opens the task as a modal while keeping the
 * current page rendered behind it.
 *
 * Example:
 *   <TaskLink taskId={hit.id} className="hover:underline">View</TaskLink>
 *
 * Uses `replace: true` for the same reason as `openTaskModal` — see
 * that function's doc comment.
 */
export function TaskLink({
  taskId,
  children,
  className,
  title,
}: {
  taskId: string
  children: ReactNode
  className?: string
  title?: string
}): JSX.Element {
  const location = useLocation()
  return (
    <Link
      to={`/tasks/${taskId}`}
      replace
      state={{ backgroundLocation: location } satisfies BackgroundState}
      className={className}
      title={title}
    >
      {children}
    </Link>
  )
}
