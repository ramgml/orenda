import { useEffect, useMemo, useState } from 'react'

import { api, type TimeReport } from '@/shared/api/client'

/**
 * /reports — time aggregation per task over a window.
 *
 * The backend groups by task only (Phase 4.5), so the chart is a
 * horizontal bar per task. Default window: last 7 days.
 */
export function ReportsPage(): JSX.Element {
  // Default: today and 7 days back.
  const today = useMemo(() => isoDate(new Date()), [])
  const sevenAgo = useMemo(() => {
    const d = new Date()
    d.setDate(d.getDate() - 6) // 7 days inclusive of today
    return isoDate(d)
  }, [])

  const [from, setFrom] = useState(sevenAgo)
  const [to, setTo] = useState(today)
  const [report, setReport] = useState<TimeReport | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function load(): Promise<void> {
    setError(null)
    try {
      const r = await api.getTimeReport({
        from: `${from}T00:00:00Z`,
        to: `${to}T23:59:59Z`,
      })
      setReport(r)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [from, to])

  const tasks = report?.tasks ?? []
  const max = tasks.reduce((m, t) => Math.max(m, t.total_sec), 0)

  return (
    <section className="space-y-4">
      <header>
        <h1 className="text-2xl font-semibold">Time report</h1>
        <p className="text-sm text-slate-500">Per-task totals across the window you choose.</p>
      </header>

      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      <div className="flex items-end gap-2 text-sm">
        <label className="block">
          <span className="block text-slate-500 text-xs">From</span>
          <input
            type="date"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
            className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
          />
        </label>
        <label className="block">
          <span className="block text-slate-500 text-xs">To</span>
          <input
            type="date"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
          />
        </label>
        <div className="ml-auto text-xs text-slate-500">
          Total: <span className="font-mono">{formatHM(report?.total_sec ?? 0)}</span>
        </div>
      </div>

      <div className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950">
        <h2 className="font-semibold mb-2">Tasks</h2>
        {tasks.length === 0 ? (
          <p className="text-slate-500 text-sm">No time logged in this window.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-slate-500 border-b border-slate-200 dark:border-slate-800">
                <th className="py-2">Task</th>
                <th>Total</th>
                <th className="w-1/2">Distribution</th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((t) => (
                <tr key={t.task_id} className="border-b border-slate-100 dark:border-slate-800">
                  <td className="py-2">
                    {t.title ?? <span className="text-slate-400 font-mono text-xs">{t.task_id.slice(0, 8)}…</span>}
                  </td>
                  <td className="font-mono">{formatHM(t.total_sec)}</td>
                  <td>
                    <div className="h-3 bg-slate-100 dark:bg-slate-800 rounded overflow-hidden">
                      <div
                        className="h-full bg-orenda-500"
                        style={{ width: `${max ? Math.round((t.total_sec / max) * 100) : 0}%` }}
                        aria-label={`${formatHM(t.total_sec)} of ${formatHM(max)}`}
                      />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  )
}

function formatHM(sec: number): string {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (h === 0) return `${m}m`
  return `${h}h ${m}m`
}

function isoDate(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}