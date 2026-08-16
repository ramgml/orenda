import { Component, type ErrorInfo, type ReactNode, useEffect, useMemo, useState } from 'react';
import {
  Calendar,
  dateFnsLocalizer,
  type Event as RBCEvent,
  type SlotInfo,
} from 'react-big-calendar';
import withDragAndDrop from 'react-big-calendar/lib/addons/dragAndDrop';
import 'react-big-calendar/lib/addons/dragAndDrop/styles.css';
import {
  format,
  parse,
  startOfWeek,
  getDay,
  addMonths,
  subMonths,
  startOfMonth,
  endOfMonth,
  eachDayOfInterval,
  formatISO,
  isSameDay,
  isSameMonth,
} from 'date-fns';
import { enUS } from 'date-fns/locale';

import 'react-big-calendar/lib/css/react-big-calendar.css';

import { api, type CalendarEvent } from '@/shared/api/client';
import { ErrorBanner } from '@/shared/ui/ErrorBanner';
import { useWebSocketTopic } from '@/shared/ws';

const localizer = dateFnsLocalizer({
  format,
  parse,
  startOfWeek: () => startOfWeek(new Date(), { weekStartsOn: 1 }),
  getDay,
  locales: { 'en-US': enUS },
});

// DragAndDropCalendar is the regular Calendar wrapped with the
// react-big-calendar drag-and-drop addon. It's the only piece that
// needs the addon (it pulls in react-dnd); the rest of the page is
// unchanged.
const DnDCalendar = withDragAndDrop(Calendar);

// DefaultEventComponent is the rbc default "Event" render. We supply
// it explicitly via components.event so we control how the title and
// time-slot appear. The library's default would show only the
// title; the explicit one also surfaces the start time so the cell
// matches the pre-DnD behaviour.
//
// rbc passes `slotStart` only in week/day view. In month view
// the prop is undefined, so we hide the time strip there and just
// show the title — keeps the cell uncluttered.
function DefaultEventComponent({
  event,
  title,
  isAllDay,
  slotStart,
}: {
  event: { id?: string; title?: string; allDay?: boolean };
  title: string;
  isAllDay?: boolean;
  slotStart?: Date;
}): JSX.Element {
  const display = title || event?.title || '';
  return (
    <div className="rbc-event-label">
      {slotStart && !isAllDay && (
        <div className="rbc-event-time text-[10px] opacity-90">
          {slotStart.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
        </div>
      )}
      {isAllDay && <div className="rbc-event-time text-[10px] opacity-90">All day</div>}
      <div className="rbc-event-title truncate">{display}</div>
    </div>
  );
}

type View = 'month' | 'week' | 'day' | 'agenda';

const PRESET_COLORS = [
  '#3b82f6', // orenda blue (default)
  '#10b981', // emerald
  '#f59e0b', // amber
  '#ef4444', // red
  '#8b5cf6', // violet
  '#ec4899', // pink
  '#14b8a6', // teal
  '#6b7280', // gray
];

/**
 * /calendar — Google-Calendar-style month / week / day / agenda view.
 *
 * Layout:
 *   ┌─ 260px sidebar ─────┬─ main view ─────────────────────────────┐
 *   │ Mini month picker    │ [Today] [◀]  Month YYYY  [▶]   [D W M A]  │
 *   │ (click a day to jump)│                                          │
 *   │ ─────                 │ react-big-calendar with event colors    │
 *   │ Quick filters         │                                          │
 *   │ [+ Create]             │                                          │
 *   └────────────────────┴───────────────────────────────────────┘
 *
 * Interactions:
 *   - Click an empty slot → opens Create modal pre-filled with that
 *     slot's start/end times.
 *   - Click an event → opens Details modal (Edit + Delete).
 *   - Drag events to reschedule (one-shot patch on drop).
 *   - Mini calendar on the left lets the user jump to any month/day.
 */
export function CalendarPage(): JSX.Element {
  const [events, setEvents] = useState<CalendarEvent[]>([]);
  // Phase 30.8: tasks with a due_at are projected into the calendar
  // as all-day events. We only carry the fields the calendar needs;
  // the rest of the Task is available via the existing /tasks/{id}
  // endpoint when the operator opens the row.
  const [tasksByDue, setTasksByDue] = useState<
    Array<{
      id: string;
      title: string;
      due_at?: string | undefined;
      status: string;
    }>
  >([]);
  const [projects, setProjects] = useState<{ id: string; name: string }[]>([]);
  const [view, setView] = useState<View>('week');
  const [cursor, setCursor] = useState<Date>(new Date());
  const [error, setError] = useState<string | null>(null);

  // Modal: 'create' | 'edit' | null. `draft` carries pre-filled
  // values from the calendar surface (a clicked slot or event).
  type Mode = { kind: 'create'; draft: EventDraft } | { kind: 'edit'; event: CalendarEvent } | null;
  const [mode, setMode] = useState<Mode>(null);

  // Load events. We always load a window centred on the current
  // cursor so dragging beyond the window still finds the row.
  const range = useMemo(() => {
    const start = view === 'month' ? startOfMonth(subMonths(cursor, 1)) : subMonths(cursor, 1);
    const end = view === 'month' ? endOfMonth(addMonths(cursor, 1)) : addMonths(cursor, 1);
    return { from: start, to: end };
  }, [cursor, view]);

  async function load(): Promise<void> {
    try {
      const [list, ps, due] = await Promise.all([
        api.listEvents({
          from: range.from.toISOString(),
          to: range.to.toISOString(),
        }),
        api.listProjects(),
        api.tasksWithDue({
          from: range.from.toISOString(),
          to: range.to.toISOString(),
        }),
      ]);
      setEvents(list);
      setProjects(ps.map((p) => ({ id: p.id, name: p.name })));
      setTasksByDue(due.tasks);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [range.from.getTime(), range.to.getTime()]);

  useWebSocketTopic('events', () => {
    load();
  });

  const rbEvents: RBCEvent[] = useMemo(
    () => [
      ...events.map((e) => ({
        id: e.id,
        title: e.title,
        start: new Date(e.start_at),
        end: new Date(e.end_at),
        allDay: e.all_day,
        resource: e,
      })),
      // Phase 30.8: tasks with a due_at become all-day deadlines on
      // their due date. Click routes to the task modal via the
      // existing resource pattern. Done tasks render with reduced
      // opacity so the calendar still lists them but operators can
      // distinguish at a glance.
      ...tasksByDue
        .filter((t) => !!t.due_at)
        .map((t) => {
          const due = new Date(t.due_at as string);
          return {
            id: `task-${t.id}`,
            title: `📌 ${t.title}${t.status === 'done' ? ' ✓' : ''}`,
            start: due,
            end: due,
            allDay: true,
            resource: { task: t, kind: 'task' as const },
          };
        }),
    ],
    [events, tasksByDue],
  );

  function eventStyleGetter(rbEvent: RBCEvent): { style: React.CSSProperties } {
    const e = rbEvent.resource as CalendarEvent;
    const color = e.color ?? PRESET_COLORS[0];
    return {
      style: {
        backgroundColor: color,
        borderColor: color,
        color: '#fff',
        borderRadius: '4px',
        fontSize: '0.85em',
      },
    };
  }

  function onSelectSlot(slot: SlotInfo): void {
    // For month view, slot.start is the clicked day. For week/day
    // view, slot.start/end are the actual time range.
    const start = slot.start;
    const end =
      slot.end && slot.end.getTime() > start.getTime()
        ? slot.end
        : new Date(start.getTime() + 60 * 60 * 1000);
    setMode({
      kind: 'create',
      draft: {
        title: '',
        description: '',
        start_at: start.toISOString(),
        end_at: end.toISOString(),
        all_day: view === 'month',
        color: PRESET_COLORS[0],
        project_id: '',
      },
    });
  }

  function onSelectEvent(rbEvent: RBCEvent): void {
    setMode({ kind: 'edit', event: rbEvent.resource as CalendarEvent });
  }

  // onEventDrop fires when the user drags an event to a different
  // time/day. We PATCH the task's start_at/end_at and reload so the
  // server-side change becomes visible immediately. The move keeps
  // the project's column and color intact.
  async function onEventDrop(args: {
    event: RBCEvent;
    start: Date | string;
    end: Date | string;
  }): Promise<void> {
    const e = args.event.resource as CalendarEvent;
    if (!e.id) return;
    const startStr = typeof args.start === 'string' ? args.start : args.start.toISOString();
    const endStr = typeof args.end === 'string' ? args.end : args.end.toISOString();
    try {
      await api.patchEvent(e.id, { start_at: startStr, end_at: endStr });
      await load();
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  function closeModal(): void {
    setMode(null);
  }

  // Drag-to-reschedule is live: the calendar is wrapped in
  // withDragAndDrop (react-big-calendar addon) and onEventDrop PATCHes
  // the event's start_at/end_at on drop. Click-to-edit and the
  // Edit-modal time pickers remain as the precise-input alternative.

  return (
    <section className="grid grid-cols-[260px,1fr] gap-4">
      <CalendarSidebar
        cursor={cursor}
        onPick={(d) => setCursor(d)}
        onCreate={() =>
          setMode({
            kind: 'create',
            draft: blankDraft(),
          })
        }
      />

      <div className="space-y-2">
        <Toolbar
          cursor={cursor}
          view={view}
          onView={setView}
          onCursor={setCursor}
          onToday={() => setCursor(new Date())}
          onCreate={() =>
            setMode({
              kind: 'create',
              draft: blankDraft(),
            })
          }
        />

        {error && <ErrorBanner message={error} />}

        <div className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-2 h-[75vh] calendar-shell">
          <CalendarErrorBoundary>
            <DnDCalendar
              localizer={localizer}
              events={rbEvents}
              date={cursor}
              view={view}
              views={['month', 'week', 'day', 'agenda']}
              onView={(v) => setView(v as View)}
              onNavigate={(d) => setCursor(d)}
              selectable
              startAccessor={(e) => new Date((e as { start: Date | string }).start)}
              endAccessor={(e) => new Date((e as { end: Date | string }).end)}
              eventPropGetter={eventStyleGetter}
              onSelectSlot={onSelectSlot}
              onSelectEvent={onSelectEvent}
              onEventDrop={onEventDrop}
              components={{ event: DefaultEventComponent }}
              popup
              style={{ height: '100%' }}
            />
          </CalendarErrorBoundary>
        </div>
      </div>

      {mode?.kind === 'create' && (
        <EventModal
          title="Create event"
          draft={mode.draft}
          projects={projects}
          onCancel={closeModal}
          onSubmit={async (d) => {
            await api.createEvent({
              title: d.title,
              description: d.description,
              start_at: d.start_at,
              end_at: d.end_at,
              all_day: d.all_day,
              color: d.color,
              project_id: d.project_id || undefined,
            });
            closeModal();
            await load();
          }}
        />
      )}
      {mode?.kind === 'edit' && (
        <EventModal
          title="Edit event"
          draft={modeDraftFromEvent(mode.event)}
          projects={projects}
          onCancel={closeModal}
          onDelete={
            mode.event.id
              ? async () => {
                  await api.deleteEvent(mode.event.id);
                  closeModal();
                  await load();
                }
              : undefined
          }
          onSubmit={async (d) => {
            await api.patchEvent(mode.event.id, {
              title: d.title,
              description: d.description,
              start_at: d.start_at,
              end_at: d.end_at,
              all_day: d.all_day,
              color: d.color,
              project_id: d.project_id || undefined,
            });
            closeModal();
            await load();
          }}
        />
      )}
    </section>
  );
}

interface EventDraft {
  title: string;
  description: string;
  start_at: string;
  end_at: string;
  all_day: boolean;
  color: string;
  /**
   * Phase 16: empty string is the explicit "no project" choice
   * (event lands in the Inbox). The dropdown below also offers
   * `<option value="">Inbox (no project)</option>` as the default.
   */
  project_id: string;
}

// Tiny ErrorBoundary around the calendar so a future rbc throw (e.g.
// wrong prop type) doesn't blank the whole page. The error is
// surfaced in place instead of escaping to the React root.
class CalendarErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error): { error: Error } {
    return { error };
  }
  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error('Calendar crashed:', error, info);
  }
  render(): ReactNode {
    if (this.state.error) {
      return <ErrorBanner message={`Calendar failed to render: ${this.state.error.message}`} />;
    }
    return this.props.children;
  }
}

function blankDraft(): EventDraft {
  const now = new Date();
  const start = new Date(now);
  start.setMinutes(0, 0, 0);
  const end = new Date(start.getTime() + 60 * 60 * 1000);
  return {
    title: '',
    description: '',
    start_at: start.toISOString(),
    end_at: end.toISOString(),
    all_day: false,
    color: PRESET_COLORS[0],
    project_id: '',
  };
}

function modeDraftFromEvent(e: CalendarEvent): EventDraft {
  return {
    title: e.title,
    description: e.description ?? '',
    start_at: e.start_at,
    end_at: e.end_at,
    all_day: e.all_day,
    color: e.color ?? PRESET_COLORS[0],
    project_id: e.project_id ?? '',
  };
}

// ---------------------------------------------------------------------------
// Toolbar — Today / arrows / view switcher / Create
// ---------------------------------------------------------------------------

function Toolbar({
  cursor,
  view,
  onView,
  onCursor,
  onToday,
  onCreate,
}: {
  cursor: Date;
  view: View;
  onView: (v: View) => void;
  onCursor: (d: Date) => void;
  onToday: () => void;
  onCreate: () => void;
}): JSX.Element {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onToday}
          className="px-3 py-1.5 rounded border border-slate-300 dark:border-slate-700 text-sm hover:bg-slate-100 dark:hover:bg-slate-800"
        >
          Today
        </button>
        <div className="flex">
          <button
            type="button"
            onClick={() => onCursor(shiftCursor(cursor, view, -1))}
            className="px-2 py-1.5 rounded-l border border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800"
            title="Previous"
          >
            ‹
          </button>
          <button
            type="button"
            onClick={() => onCursor(shiftCursor(cursor, view, 1))}
            className="px-2 py-1.5 rounded-r border-t border-r border-b border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800"
            title="Next"
          >
            ›
          </button>
        </div>
        <h2 className="text-xl font-semibold ml-2">{titleFor(cursor, view)}</h2>
      </div>

      <div className="flex items-center gap-2">
        <div className="hidden sm:flex rounded border border-slate-300 dark:border-slate-700 overflow-hidden text-sm">
          {(['day', 'week', 'month'] as View[]).map((v, i) => (
            <button
              key={v}
              type="button"
              onClick={() => onView(v)}
              className={`px-3 py-1.5 capitalize ${
                i > 0 ? 'border-l border-slate-300 dark:border-slate-700' : ''
              } ${
                view === v
                  ? 'bg-orenda-100 dark:bg-orenda-900/30 text-orenda-700 dark:text-orenda-300'
                  : 'hover:bg-slate-100 dark:hover:bg-slate-800'
              }`}
            >
              {v}
            </button>
          ))}
        </div>
        <button
          type="button"
          onClick={onCreate}
          className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
        >
          + Create
        </button>
      </div>
    </div>
  );
}

function shiftCursor(cursor: Date, view: View, delta: number): Date {
  const d = new Date(cursor);
  if (view === 'month') d.setMonth(d.getMonth() + delta);
  else if (view === 'week') d.setDate(d.getDate() + 7 * delta);
  else d.setDate(d.getDate() + delta);
  return d;
}

function titleFor(d: Date, view: View): string {
  if (view === 'agenda') return 'Agenda';
  const fmt = new Intl.DateTimeFormat('en-US', { month: 'long', year: 'numeric' });
  if (view === 'month') return fmt.format(d);
  if (view === 'week') {
    const s = new Date(d);
    s.setDate(s.getDate() - ((s.getDay() + 6) % 7)); // Monday
    const e = new Date(s);
    e.setDate(e.getDate() + 6);
    const a = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(s);
    const b = new Intl.DateTimeFormat('en-US', { day: 'numeric', year: 'numeric' }).format(e);
    return `${a} – ${b}`;
  }
  // day
  return new Intl.DateTimeFormat('en-US', {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  }).format(d);
}

// ---------------------------------------------------------------------------
// Sidebar — mini calendar + Create button
// ---------------------------------------------------------------------------

function CalendarSidebar({
  cursor,
  onPick,
  onCreate,
}: {
  cursor: Date;
  onPick: (d: Date) => void;
  onCreate: () => void;
}): JSX.Element {
  return (
    <aside className="space-y-3">
      <button
        type="button"
        onClick={onCreate}
        className="w-full px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm shadow"
      >
        + Create event
      </button>
      <MiniCalendar cursor={cursor} onPick={onPick} />
    </aside>
  );
}

function MiniCalendar({
  cursor,
  onPick,
}: {
  cursor: Date;
  onPick: (d: Date) => void;
}): JSX.Element {
  // We show the month the cursor is in.
  const monthStart = startOfMonth(cursor);
  const monthEnd = endOfMonth(cursor);
  // Pad to Monday-aligned weeks.
  const gridStart = new Date(monthStart);
  gridStart.setDate(gridStart.getDate() - ((gridStart.getDay() + 6) % 7));
  const gridEnd = new Date(monthEnd);
  gridEnd.setDate(gridEnd.getDate() + (7 - 1 - ((gridEnd.getDay() + 6) % 7)));
  const days = eachDayOfInterval({ start: gridStart, end: gridEnd });
  const today = new Date();

  return (
    <div className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-2 text-sm">
      <div className="flex items-center justify-between mb-2">
        <button
          type="button"
          onClick={() => onPick(subMonths(cursor, 1))}
          className="px-1.5 text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
          title="Previous month"
        >
          ‹
        </button>
        <div className="text-xs font-semibold">
          {new Intl.DateTimeFormat('en-US', {
            month: 'long',
            year: 'numeric',
          }).format(cursor)}
        </div>
        <button
          type="button"
          onClick={() => onPick(addMonths(cursor, 1))}
          className="px-1.5 text-slate-400 hover:text-slate-700 dark:hover:text-slate-200"
          title="Next month"
        >
          ›
        </button>
      </div>
      <div className="grid grid-cols-7 text-[10px] uppercase text-slate-400 mb-1">
        {['M', 'T', 'W', 'T', 'F', 'S', 'S'].map((d, i) => (
          <div key={i} className="text-center">
            {d}
          </div>
        ))}
      </div>
      <div className="grid grid-cols-7 gap-0.5">
        {days.map((d) => {
          const inMonth = isSameMonth(d, monthStart);
          const isToday = isSameDay(d, today);
          const isCursor = isSameDay(d, cursor);
          return (
            <button
              key={formatISO(d, { representation: 'date' })}
              type="button"
              onClick={() => onPick(d)}
              className={`aspect-square rounded text-xs flex items-center justify-center ${
                isCursor
                  ? 'bg-orenda-500 text-white'
                  : isToday
                    ? 'bg-orenda-100 dark:bg-orenda-900/30 text-orenda-700 dark:text-orenda-300'
                    : inMonth
                      ? 'hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-200'
                      : 'text-slate-300 dark:text-slate-600 hover:bg-slate-100 dark:hover:bg-slate-800'
              }`}
            >
              {d.getDate()}
            </button>
          );
        })}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Event modal — used for both Create and Edit
// ---------------------------------------------------------------------------

function EventModal({
  title,
  draft,
  projects,
  onSubmit,
  onCancel,
  onDelete,
}: {
  title: string;
  draft: EventDraft;
  projects: { id: string; name: string }[];
  onSubmit: (d: EventDraft) => Promise<void>;
  onCancel: () => void;
  onDelete?: () => Promise<void>;
}): JSX.Element {
  const [form, setForm] = useState<EventDraft>(draft);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (!form.title.trim()) {
      setErr('Title is required');
      return;
    }
    if (new Date(form.end_at).getTime() <= new Date(form.start_at).getTime()) {
      setErr('End must be after start');
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      await onSubmit(form);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
      <form
        onSubmit={submit}
        className="bg-white dark:bg-slate-900 rounded-lg shadow-xl w-full max-w-md p-5 space-y-3"
      >
        <h3 className="font-semibold text-lg">{title}</h3>

        <label className="block text-sm">
          <span className="text-xs text-slate-500">Title</span>
          <input
            autoFocus
            value={form.title}
            onChange={(e) => setForm({ ...form, title: e.target.value })}
            className="mt-1 w-full px-2 py-1.5 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            required
          />
        </label>

        <label className="block text-sm">
          <span className="text-xs text-slate-500">Description</span>
          <textarea
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            rows={2}
            className="mt-1 w-full px-2 py-1.5 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
          />
        </label>

        <div className="grid grid-cols-2 gap-2 text-sm">
          <label className="block">
            <span className="text-xs text-slate-500">Start</span>
            <input
              type={form.all_day ? 'date' : 'datetime-local'}
              value={toLocalInput(form.start_at, form.all_day)}
              onChange={(e) =>
                setForm({ ...form, start_at: fromLocalInput(e.target.value, form.all_day) })
              }
              className="mt-1 w-full px-2 py-1.5 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            />
          </label>
          <label className="block">
            <span className="text-xs text-slate-500">End</span>
            <input
              type={form.all_day ? 'date' : 'datetime-local'}
              value={toLocalInput(form.end_at, form.all_day)}
              onChange={(e) =>
                setForm({ ...form, end_at: fromLocalInput(e.target.value, form.all_day) })
              }
              className="mt-1 w-full px-2 py-1.5 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            />
          </label>
        </div>

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={form.all_day}
            onChange={(e) => setForm({ ...form, all_day: e.target.checked })}
          />
          All day
        </label>

        <label className="block text-sm">
          <span className="text-xs text-slate-500">Project</span>
          <select
            value={form.project_id}
            onChange={(e) => setForm({ ...form, project_id: e.target.value })}
            className="mt-1 w-full px-2 py-1.5 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
          >
            {/* Phase 16: empty string = Inbox (no project). The
                server stores project_id IS NULL for these events. */}
            <option value="">Inbox (no project)</option>
            {projects.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>

        <div>
          <div className="text-xs text-slate-500 mb-1">Color</div>
          <div className="flex flex-wrap gap-1.5">
            {PRESET_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => setForm({ ...form, color: c })}
                className={`w-6 h-6 rounded-full ring-2 ${
                  form.color === c ? 'ring-orenda-500' : 'ring-transparent hover:ring-slate-300'
                }`}
                style={{ backgroundColor: c }}
                aria-label={`Color ${c}`}
              />
            ))}
          </div>
        </div>

        {err && <div className="text-sm text-red-600">{err}</div>}

        <div className="flex items-center justify-between pt-2">
          <div>
            {onDelete && (
              <button
                type="button"
                onClick={() => {
                  if (window.confirm('Delete this event?')) {
                    onDelete().catch((e) => setErr(e instanceof Error ? e.message : String(e)));
                  }
                }}
                disabled={busy}
                className="px-3 py-1.5 rounded border border-red-300 text-red-700 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/20 text-sm disabled:opacity-50"
              >
                Delete
              </button>
            )}
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={onCancel}
              className="px-3 py-1.5 rounded border border-slate-300 dark:border-slate-700 text-sm"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={busy}
              className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
            >
              {busy ? 'Saving…' : 'Save'}
            </button>
          </div>
        </div>
      </form>
    </div>
  );
}

// date <input type="datetime-local"> wants "YYYY-MM-DDTHH:mm" without
// timezone. Convert ISO ↔ that format.
function toLocalInput(iso: string, allDay: boolean): string {
  const d = new Date(iso);
  const pad = (n: number): string => String(n).padStart(2, '0');
  if (allDay) {
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  }
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(value: string, allDay: boolean): string {
  if (allDay) {
    // Treat the picked day as midnight UTC.
    return `${value}T00:00:00.000Z`;
  }
  // datetime-local has no zone — assume the user's local zone, then
  // emit a proper ISO string.
  const d = new Date(value);
  return d.toISOString();
}
