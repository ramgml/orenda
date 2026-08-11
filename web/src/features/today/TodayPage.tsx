import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { api, type Task } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'

/**
 * Phase 20: "Today" dashboard. One screen with everything the user
 * wants to look at when they open the app:
 *
 *   1. Overdue (red section) — due_at < today, still open.
 *   2. Due today (amber) — due_at is today.
 *   3. Scheduled today (calendar items that overlap today).
 *   4. A line showing awaiting-human count linking to /review.
 *   5. Empty state when nothing is owed.
 *
 * Single round-trip: GET /api/v1/today returns everything. WS
 * 'tasks' invalidates so a freshly-submitted agent work shows up
 * without a refresh.
 */
export function TodayPage(): JSX.Element {
  const [data, setData] = useState<TodayResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async (): Promise<void> => {
    try {
      const r = await api.getToday()
      setData(r)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // Re-fetch on any task event — the today list reflects tasks across
  // every project, so any mutation might add or remove an entry.
  useWebSocketTopic('tasks', () => {
    void load()
  })

  if (loading) {
    return <p className="p-6 text-sm text-slate-400 italic">Loading…</p>
  }
  if (error) {
    return <p className="p-6 text-sm text-red-600">{error}</p>
  }
  if (!data) return <></>

  const total =
    data.overdue.length + data.due_today.length + data.scheduled_today.length

  if (total === 0 && data.awaiting_count === 0) {
    return (
      <section className="p-6 max-w-3xl mx-auto">
        <header>
          <h1 className="text-2xl font-semibold">Today</h1>
          <p className="text-sm text-slate-500 mt-1">
            Nothing scheduled, nothing overdue, nothing awaiting your verdict.
          </p>
        </header>
        <div className="mt-6 rounded border border-emerald-200 bg-emerald-50 p-6 text-center text-emerald-800">
          <p className="text-3xl mb-2">✓</p>
          <p className="text-sm">Day is clear. Capture a thought with <kbd>q</kbd>.</p>
        </div>
      </section>
    )
  }

  return (
    <section className="p-6 max-w-3xl mx-auto space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Today</h1>
        <p className="text-sm text-slate-500 mt-1">
          {total} thing{total === 1 ? '' : 's'} need attention.
        </p>
      </header>

      {data.awaiting_count > 0 && (
        <Link
          to="/review"
          className="block rounded border border-amber-300 bg-amber-50 p-3 text-amber-900 text-sm hover:bg-amber-100"
        >
          ⏳ <strong>{data.awaiting_count}</strong> task
          {data.awaiting_count === 1 ? '' : 's'} awaiting your review → /review
        </Link>
      )}

      <TodaySection
        title="Overdue"
        color="red"
        tasks={data.overdue}
        emptyText="Nothing overdue."
      />
      <TodaySection
        title="Due today"
        color="amber"
        tasks={data.due_today}
        emptyText="Nothing due today."
      />
      <TodaySection
        title="Scheduled today"
        color="slate"
        tasks={data.scheduled_today}
        emptyText="No calendar items."
        showTime
      />
    </section>
  )
}

type TodayResponse = {
  overdue: Task[]
  due_today: Task[]
  scheduled_today: Task[]
  awaiting_count: number
}

function TodaySection({
  title,
  color,
  tasks,
  emptyText,
  showTime,
}: {
  title: string
  color: 'red' | 'amber' | 'slate'
  tasks: Task[]
  emptyText: string
  showTime?: boolean
}): JSX.Element {
  const dotColor =
    color === 'red' ? 'bg-red-500' : color === 'amber' ? 'bg-amber-500' : 'bg-slate-400'

  return (
    <div>
      <div className="flex items-center gap-2 mb-2">
        <span aria-hidden className={`inline-block h-2 w-2 rounded-full ${dotColor}`} />
        <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">
          {title} ({tasks.length})
        </h2>
      </div>
      {tasks.length === 0 ? (
        <p className="text-xs text-slate-400 italic pl-4">{emptyText}</p>
      ) : (
        <ul className="space-y-1">
          {tasks.map((t) => (
            <li key={t.id} className="rounded border border-slate-200 dark:border-slate-800 p-2 text-sm bg-white dark:bg-slate-950">
              <Link to={`/tasks/${t.id}`} className="text-slate-800 dark:text-slate-100 hover:underline">
                {t.title}
              </Link>
              <span className="ml-2 text-[10px] text-slate-400 font-mono">
                {showTime && t.start_at
                  ? new Date(t.start_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
                  : t.due_at
                  ? new Date(t.due_at).toLocaleDateString()
                  : t.status}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}