import { useEffect, useState } from 'react'

import { api, type HealthResponse } from '@/shared/api/client'

/**
 * Small badge in the nav bar showing server health.
 *
 * Polls /healthz every 30s; turns red on failure. Phase 0 liveness is enough;
 * /readyz will follow in Phase 1.
 */
export function HealthBadge(): JSX.Element {
  const [health, setHealth] = useState<HealthResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const tick = (): void => {
      api
        .health()
        .then((h) => {
          if (cancelled) return
          setHealth(h)
          setError(null)
        })
        .catch((e: unknown) => {
          if (cancelled) return
          setError(e instanceof Error ? e.message : String(e))
        })
    }
    tick()
    const id = window.setInterval(tick, 30_000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [])

  if (error) {
    return (
      <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded text-xs bg-red-100 text-red-800">
        <span className="h-2 w-2 rounded-full bg-red-500" /> offline
      </span>
    )
  }
  if (!health) {
    return (
      <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded text-xs bg-slate-100 text-slate-500">
        <span className="h-2 w-2 rounded-full bg-slate-400 animate-pulse" /> checking…
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded text-xs bg-green-100 text-green-800">
      <span className="h-2 w-2 rounded-full bg-green-500" /> {health.status} · {health.version}
    </span>
  )
}