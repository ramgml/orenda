import { useEffect, useMemo, useState } from 'react'
import { Calendar, dateFnsLocalizer, type Event as RBCEvent } from 'react-big-calendar'
import { format, parse, startOfWeek, getDay } from 'date-fns'
import { enUS } from 'date-fns/locale'

import 'react-big-calendar/lib/css/react-big-calendar.css'

import { api, type CalendarEvent } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'

const localizer = dateFnsLocalizer({
  format,
  parse,
  startOfWeek: () => startOfWeek(new Date(), { weekStartsOn: 1 }),
  getDay,
  locales: { 'en-US': enUS },
})

type View = 'month' | 'week' | 'day' | 'agenda'

/**
 * /calendar — the unified calendar view.
 *
 * Phase 4 ships a month/week/day/agenda view backed by react-big-calendar
 * with the events API as the source of truth. Drag-to-reschedule lands
 * in a follow-up; for now events are click-to-edit via the side panel.
 */
export function CalendarPage(): JSX.Element {
  const [events, setEvents] = useState<CalendarEvent[]>([])
  const [view, setView] = useState<View>('month')
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [newTitle, setNewTitle] = useState('')
  const [newStart, setNewStart] = useState('')
  const [newEnd, setNewEnd] = useState('')
  const [newAllDay, setNewAllDay] = useState(false)

  // Use the last 30 days as the visible window.
  const range = useMemo(() => {
    const now = new Date()
    const from = new Date(now)
    from.setDate(from.getDate() - 30)
    const to = new Date(now)
    to.setDate(to.getDate() + 30)
    return { from: from.toISOString(), to: to.toISOString() }
  }, [])

  async function load(): Promise<void> {
    try {
      const list = await api.listEvents(range)
      setEvents(list)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range.from, range.to])

  useWebSocketTopic('events', () => {
    load()
  })

  const rbEvents: RBCEvent[] = useMemo(
    () =>
      events.map((e) => ({
        id: e.id,
        title: e.title,
        start: new Date(e.start_at),
        end: new Date(e.end_at),
        allDay: e.all_day,
        resource: e,
      })),
    [events],
  )

  async function onCreate(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!newTitle.trim() || !newStart || !newEnd) return
    try {
      await api.createEvent({
        title: newTitle.trim(),
        start_at: new Date(newStart).toISOString(),
        end_at: new Date(newEnd).toISOString(),
        all_day: newAllDay,
      })
      setNewTitle('')
      setNewStart('')
      setNewEnd('')
      setNewAllDay(false)
      setCreating(false)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <section>
      <header className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-semibold">Calendar</h1>
          <p className="text-sm text-slate-500">
            {events.length} events · {view} view
          </p>
        </div>
        <button
          type="button"
          onClick={() => setCreating((v) => !v)}
          className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
        >
          {creating ? 'Cancel' : 'New event'}
        </button>
      </header>

      {error && (
        <div className="mb-4 rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      {creating && (
        <form
          onSubmit={onCreate}
          className="mb-4 p-4 rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 grid gap-2 sm:grid-cols-4"
        >
          <input
            type="text"
            placeholder="Title"
            value={newTitle}
            onChange={(e) => setNewTitle(e.target.value)}
            className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            required
          />
          <input
            type="datetime-local"
            value={newStart}
            onChange={(e) => setNewStart(e.target.value)}
            className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            required
          />
          <input
            type="datetime-local"
            value={newEnd}
            onChange={(e) => setNewEnd(e.target.value)}
            className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            required
          />
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={newAllDay}
                onChange={(e) => setNewAllDay(e.target.checked)}
              />
              All day
            </label>
            <button
              type="submit"
              className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
            >
              Create
            </button>
          </div>
        </form>
      )}

      <div className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-2 h-[70vh]">
        <Calendar
          localizer={localizer}
          events={rbEvents}
          startAccessor="start"
          endAccessor="end"
          view={view}
          onView={(v) => setView(v as View)}
          views={['month', 'week', 'day', 'agenda']}
          onSelectEvent={(ev) => {
            const e = ev.resource as CalendarEvent
            if (confirm(`Delete "${e.title}"?`)) {
              api.deleteEvent(e.id).then(load).catch((err) => setError(String(err)))
            }
          }}
          style={{ height: '100%' }}
        />
      </div>
    </section>
  )
}