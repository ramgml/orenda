import { useEffect } from 'react'

/**
 * Phase 28.3 (polish): lock the document body from scrolling while
 * the calling component is mounted.
 *
 * Why a hook:
 *   - `TaskModal` is one consumer; future modals (drawers, command
 *     palette, full-screen confirm) need the same lock.
 *   - Isolating the behaviour makes it unit-testable without
 *     dragging the modal's router context into the test.
 *
 * Behaviour:
 *   - On mount, snapshot whatever value `document.body.style.overflow`
 *     holds (could be '', 'hidden', or anything a previous host set)
 *     and overwrite it with `'hidden'`.
 *   - On unmount, restore the snapshot — not just `''` — so we don't
 *     clobber legitimate inline styling.
 *   - The effect deliberately has no dependencies: the lock keys on
 *     mount, not on any prop. Inside a stacked modal (e.g. user
 *     navigates from one task to another via `<TaskLink>`), the
 *     `TaskModal` component instance is the same — only `useParams`
 *     changes — and we want the lock to stay on the whole time.
 *
 * Caveats:
 *   - iOS Safari used to require `position: fixed` on body to
 *     defeat rubber-band scrolling. Orenda is desktop-first; if a
 *     mobile PWA install becomes a goal, revisit. The desktop
 *     browsers this product runs on respect `overflow: hidden`
 *     cleanly enough for our use.
 *   - `document` exists in jsdom; the hook is safe to render in
 *     tests without extra guards.
 *
 * @example
 *   function TaskModal() {
 *     useBodyScrollLock()
 *     return <div role="dialog">…</div>
 *   }
 */
export function useBodyScrollLock(): void {
  useEffect(() => {
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = previous
    }
  }, [])
}
