import { useCallback, type ReactNode } from 'react';
import * as DialogPrimitive from '@radix-ui/react-dialog';
import {
  Link,
  useLocation,
  useNavigate,
  useParams,
  type Location,
  type NavigateFunction,
} from 'react-router-dom';

import { useBodyScrollLock } from '@/shared/hooks/useBodyScrollLock';
import { Button } from '@/shared/ui/button';
import { Dialog, DialogOverlay, DialogPortal } from '@/shared/ui/dialog';
import { TaskViewBody } from './TaskViewBody';

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
 *
 * Phase 28.3 (polish): two fixes for the long-content scroll bug.
 *   1. Centring: the backdrop used `flex items-start md:items-center`
 *      with `overflow-y-auto`. When the card grew past the viewport,
 *      `items-center` placed the flex item so its top edge went into
 *      negative offset — un-scrollable. We keep `items-start` (no
 *      centering on the cross axis) and let the card sit at the top
 *      with a `my-auto` margin: `margin: auto` centres short cards
 *      while naturally collapsing to the padding edge when the card
 *      overflows, so the user can scroll all the way to the top.
 *   2. Scroll lock: `<body>` kept its scroll while the modal was
 *      open — the background page drifted under the user's wheel.
 *      We toggle `document.body.style.overflow` on mount/cleanup.
 *      Crucially, the lock keys on mount, not on `id`: navigating
 *      from one task to another keeps the same `TaskModal` mounted
 *      (just `useParams().id` changes), so the lock stays on.
 *      Closing (any path) unmounts the component and restores the
 *      the previous overflow value — not just "visible", because a
 *      previous host app could have set its own value.
 */
export function TaskModal(): JSX.Element | null {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const close = useCallback(() => {
    // `navigate(-1)` pops the entry the modal pushed when opening, so
    // the user lands back on the page that was behind it. If they
    // opened the modal from outside any history (e.g. pasted a deep
    // link that the router still resolved here), -1 lands at "/"
    // which is the dashboard — still a sane place to be.
    navigate(-1);
  }, [navigate]);

  // Phase 28.3: lock the background page from scrolling while the
  // modal is mounted — implementation lives in `@/shared/hooks/useBodyScrollLock`
  // so other modals can reuse it and so it stays testable in
  // isolation. The hook keys on mount, not on `id`: navigating from
  // one task to another keeps the same `TaskModal` mounted (just
  // `useParams().id` changes), so the lock stays on the whole time.
  // Closing (any path) unmounts the component and the hook restores
  // the previous overflow value — not just "visible", because a
  // previous host could have set its own value.
  //
  // Phase 32.13 (shadcn/ui migration): the previous build also
  // attached a window-level Escape listener. The Dialog primitive's
  // `onEscapeKeyDown` (capture-phase on the document) catches Esc
  // inside any input — the input's own handler fires in bubble
  // phase, so the modal closes on the first press regardless of
  // focus.
  useBodyScrollLock();

  if (!id) return null;

  // Phase 32.13 (shadcn/ui migration): we compose DialogOverlay +
  // DialogPrimitive.Content directly instead of using the
  // `DialogContent` wrapper because this modal's "long-content
  // scroll" contract (Phase 28.3) requires the backdrop itself to
  // be the scroll container with a flex layout that centres a
  // short card via `my-auto` and lets a tall card reach the
  // padding edge. `DialogContent` hardcodes `fixed left-[50%]
  // top-[50%]` centring + a centred content box; we override both.
  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o) close();
      }}
    >
      <DialogPortal>
        <DialogOverlay className="z-[60] bg-black/50" />
        <DialogPrimitive.Content
          aria-label="Task details"
          className="fixed inset-0 z-[60] max-w-none h-screen flex items-start justify-center p-2 md:p-6 overflow-y-auto bg-transparent border-0 shadow-none sm:rounded-none gap-0"
          onClick={(e) => {
            // Pre-migration contract: clicking the overlay (the
            // empty padding area around the card) closes the
            // modal. The card itself used `stopPropagation`, so
            // only the scroll container's own click target —
            // never a descendant — fires this. Phase 32.13
            // (shadcn migration): we are the scroll container
            // (DialogPrimitive.Content, not a sibling overlay),
            // so Radix's onPointerDownOutside doesn't fire when
            // the user clicks our empty padding — we catch it
            // ourselves via the target-equals-currentTarget check.
            if (e.target === e.currentTarget) close();
          }}
        >
          <div className="bg-card rounded-lg shadow-2xl max-w-4xl w-full my-auto relative">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={close}
              aria-label="Close task"
              className="absolute top-2 right-2 z-10 h-8 w-8 rounded-full text-slate-500 hover:text-slate-900 dark:text-slate-300 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-slate-800"
            >
              ×
            </Button>
            <div className="p-4 md:p-6">
              <TaskViewBody taskId={id} onClose={close} />
            </div>
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </Dialog>
  );
}
// Keeping the navigation contract in one place means the
// background-location trick works for every entry point without each
// caller having to remember the magic option.
// ---------------------------------------------------------------------------

type BackgroundState = { backgroundLocation?: Location };

// isInModal reports whether the current location was reached through
// a backgroundLocation state — i.e. the user is already looking at a
// task modal on top of another page. We use this to decide whether a
// follow-up navigation should push a new history entry or replace the
// current one:
//
//   - First click from a normal page → push (so the user can return
//     to that page on close).
//   - Click from inside an already-open modal → replace (so opening
//     a child doesn't stack modals on top of each other).
//
// `location.state` may be `null`; the optional-chain handles that.
function isInModal(location: Location): boolean {
  const s = (location.state ?? null) as BackgroundState | null;
  return Boolean(s?.backgroundLocation);
}

/**
 * Programmatic equivalent of `<TaskLink>` — use with `useNavigate()`.
 *
 * Example:
 *   const navigate = useNavigate()
 *   const location = useLocation()
 *   <Button onClick={() => openTaskModal(navigate, location, task.id)}>
 *
 * Replaces the current history entry iff we're already inside an
 * open modal; otherwise pushes. This keeps the stack at exactly two
 * entries deep ([background page, current task]) so closing the
 * modal always lands the user back where they started — kanban,
 * search, project activity tab, inbox project, whatever — regardless
 * of how many child tasks they drilled through.
 */
export function openTaskModal(
  navigate: NavigateFunction,
  location: Location,
  taskId: string,
): void {
  navigate(`/tasks/${taskId}`, {
    replace: isInModal(location),
    state: { backgroundLocation: location } satisfies BackgroundState,
  });
}

/**
 * `<Link>` wrapper that opens the task as a modal while keeping the
 * current page rendered behind it.
 *
 * Example:
 *   <TaskLink taskId={hit.id} className="hover:underline">View</TaskLink>
 *
 * Same push-vs-replace rule as `openTaskModal` — see its doc
 * comment.
 */
export function TaskLink({
  taskId,
  children,
  className,
  title,
}: {
  taskId: string;
  children: ReactNode;
  className?: string;
  title?: string;
}): JSX.Element {
  const location = useLocation();
  const replace = isInModal(location);
  return (
    <Link
      to={`/tasks/${taskId}`}
      replace={replace}
      state={{ backgroundLocation: location } satisfies BackgroundState}
      className={className}
      title={title}
    >
      {children}
    </Link>
  );
}
