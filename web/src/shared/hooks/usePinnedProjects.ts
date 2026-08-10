/**
 * Persistent per-user "pinned project" set, stored in localStorage
 * under the `orenda.pinnedProjects` key.
 *
 * The hook returns a tuple of [pinnedIds, { toggle, isPinned }]
 * similar to React's `useState` convention so consumers can use it
 * declaratively. We also listen to the `storage` event so a second
 * browser tab stays in sync.
 *
 * Storage layout: a JSON array of project id strings. We tolerate
 * malformed values silently and reset to an empty set — the user's
 * worst-case is "loses pins", never a crash.
 */
import { useCallback, useEffect, useState } from 'react'

const STORAGE_KEY = 'orenda.pinnedProjects'

function readStorage(): string[] {
  try {
    if (typeof window === 'undefined') return []
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((v): v is string => typeof v === 'string')
  } catch {
    return []
  }
}

function writeStorage(ids: string[]): void {
  try {
    if (typeof window === 'undefined') return
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(ids))
  } catch {
    // Quota exceeded or storage disabled — fail silently; the in-memory
    // state still works for the current session.
  }
}

export interface PinnedProjectsAPI {
  /** Toggle a project in/out of the pinned set. */
  toggle: (projectId: string) => void
  /** Pin a specific project (idempotent). */
  pin: (projectId: string) => void
  /** Remove a project from the pin set (no-op if not present). */
  unpin: (projectId: string) => void
  /** True if the project is currently pinned. */
  isPinned: (projectId: string) => boolean
}

export function usePinnedProjects(): readonly [string[], PinnedProjectsAPI] {
  const [pinned, setPinned] = useState<string[]>(() => readStorage())

  // Cross-tab sync: another tab editing pins triggers a manual refresh.
  useEffect(() => {
    function onStorage(ev: StorageEvent): void {
      if (ev.key !== STORAGE_KEY) return
      setPinned(readStorage())
    }
    if (typeof window !== 'undefined') {
      window.addEventListener('storage', onStorage)
      return () => window.removeEventListener('storage', onStorage)
    }
    return undefined
  }, [])

  const update = useCallback((next: string[]) => {
    setPinned(next)
    writeStorage(next)
  }, [])

  const toggle = useCallback(
    (projectId: string) => {
      setPinned((prev) => {
        const next = prev.includes(projectId)
          ? prev.filter((id) => id !== projectId)
          : [...prev, projectId]
        writeStorage(next)
        return next
      })
    },
    [],
  )

  const pin = useCallback((projectId: string) => {
    setPinned((prev) => {
      if (prev.includes(projectId)) return prev
      const next = [...prev, projectId]
      writeStorage(next)
      return next
    })
  }, [])

  const unpin = useCallback((projectId: string) => {
    setPinned((prev) => {
      if (!prev.includes(projectId)) return prev
      const next = prev.filter((id) => id !== projectId)
      writeStorage(next)
      return next
    })
  }, [])

  const isPinned = useCallback(
    (projectId: string) => pinned.includes(projectId),
    [pinned],
  )

  // update is exposed for future bulk ops but currently unused by callers.
  void update

  return [pinned, { toggle, pin, unpin, isPinned }] as const
}
