import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { api, type StudyProposalView, type Task } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { ErrorBanner } from '@/shared/ui/ErrorBanner';
import { Loading } from '@/shared/ui/Loading';
import { useWebSocketTopic } from '@/shared/ws';

/**
 * Phase 20: "Today" dashboard. One screen with everything the user
 * wants to look at when they open the app:
 *
 *   1. Overdue (red section) — due_at < today, still open.
 *   2. Due today (amber) — due_at is today.
 *   3. Scheduled today (calendar items that overlap today).
 *   4. Active timer (if any) with elapsed time.
 *   5. A line showing awaiting-human count linking to /review.
 *   6. Next 7 days (one row per day) — Phase 20.3.
 *   7. Empty state when nothing is owed.
 *
 * Single round-trip: GET /api/v1/today returns everything. WS
 * 'tasks' invalidates so a freshly-submitted agent work shows up
 * without a refresh.
 */
export function TodayPage(): JSX.Element {
  const [data, setData] = useState<TodayResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [now, setNow] = useState(() => Date.now());

  const load = useCallback(async (): Promise<void> => {
    try {
      const r = await api.getToday();
      setData(r);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Re-fetch on any task event — the today list reflects tasks across
  // every project, so any mutation might add or remove an entry.
  useWebSocketTopic('tasks', () => {
    void load();
  });

  // Tick the "active timer elapsed" display once a minute so the
  // number doesn't go stale. The timer data itself is from the API;
  // only the relative timestamp is recomputed locally.
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(id);
  }, []);

  if (loading) {
    return <Loading className="p-6" />;
  }
  if (error) {
    return <ErrorBanner message={error} className="m-6" />;
  }
  if (!data) return <></>;

  const total = data.overdue.length + data.due_today.length + data.scheduled_today.length;

  if (
    total === 0 &&
    data.awaiting_count === 0 &&
    !data.active_timer &&
    (data.upcoming_week ?? []).length === 0 &&
    (data.proposals ?? []).length === 0
  ) {
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
          <p className="text-sm">
            Day is clear. Capture a thought with <kbd>q</kbd>.
          </p>
        </div>
      </section>
    );
  }

  return (
    <section className="p-6 max-w-3xl mx-auto space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Today</h1>
        <p className="text-sm text-slate-500 mt-1">
          {total} thing{total === 1 ? '' : 's'} need attention.
        </p>
      </header>

      {data.active_timer && <ActiveTimerRow timer={data.active_timer} now={now} />}

      {data.awaiting_count > 0 && (
        <Link
          to="/review"
          className="block rounded border border-amber-300 bg-amber-50 p-3 text-amber-900 text-sm hover:bg-amber-100"
        >
          ⏳ <strong>{data.awaiting_count}</strong> task
          {data.awaiting_count === 1 ? '' : 's'} awaiting your review → /review
        </Link>
      )}

      <TodaySection title="Overdue" color="red" tasks={data.overdue} emptyText="Nothing overdue." />
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

      <UpcomingWeek days={data.upcoming_week ?? []} />

      <ProposalTray proposals={data.proposals} onChange={() => void load()} />
    </section>
  );
}

/**
 * ProposalTray — Phase 31.9. The Dashboard section that lists
 * pending study proposals. Each card carries accept / dismiss
 * buttons; both actions call the server and trigger a re-fetch
 * (the parent owns the data and the WS subscription already covers
 * the "tasks" topic for invalidation).
 *
 * Acceptance is idempotent on the server (200 instead of 201 on a
 * re-accept) so we treat 200 and 201 as success — the UI doesn't
 * need to know.
 */
function ProposalTray({
  proposals,
  onChange,
}: {
  proposals: StudyProposalView[];
  onChange: () => void;
}): JSX.Element {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const accept = useCallback(
    async (id: string): Promise<void> => {
      setBusy(id);
      setError(null);
      try {
        await api.acceptStudyProposal(id);
        onChange();
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(null);
      }
    },
    [onChange],
  );

  const dismiss = useCallback(
    async (id: string): Promise<void> => {
      setBusy(id);
      setError(null);
      try {
        await api.dismissStudyProposal(id);
        onChange();
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(null);
      }
    },
    [onChange],
  );

  if (proposals.length === 0) return <></>;

  return (
    <div data-testid="proposal-tray">
      <div className="flex items-center gap-2 mb-2">
        <span aria-hidden className="inline-block h-2 w-2 rounded-full bg-indigo-500" />
        <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">
          Proposed for today ({proposals.length})
        </h2>
      </div>
      {error && <ErrorBanner message={error} className="mb-2 text-xs" />}
      <ul className="space-y-2">
        {proposals.map((p) => (
          <li
            key={p.id}
            data-testid="proposal-card"
            className="rounded border border-indigo-200 dark:border-indigo-800 bg-indigo-50 dark:bg-indigo-950 p-3 text-sm"
          >
            <div className="flex justify-between items-start gap-2">
              <div className="flex-1 min-w-0">
                <Link
                  to={p.course_id ? `/courses/${p.course_id}` : '#'}
                  className="text-slate-800 dark:text-slate-100 hover:underline font-medium"
                >
                  📖 {p.title}
                </Link>
                {p.course_id && (
                  <Link
                    to={`/courses/${p.course_id}`}
                    className="ml-2 text-xs text-indigo-700 dark:text-indigo-300 hover:underline"
                  >
                    open course →
                  </Link>
                )}
                {p.body_md && (
                  <p className="mt-1 text-xs text-slate-600 dark:text-slate-400 line-clamp-2">
                    {p.body_md}
                  </p>
                )}
                <p className="mt-1 text-[10px] text-slate-500 font-mono">
                  target: {p.target_date} · from agent {p.agent_id.slice(0, 8)}
                </p>
              </div>
              <div className="flex gap-1 shrink-0">
                <Button
                  type="button"
                  size="sm"
                  data-testid="proposal-accept"
                  disabled={busy !== null}
                  onClick={() => void accept(p.id)}
                  className="bg-indigo-600 hover:bg-indigo-700 px-3 py-1 text-xs"
                >
                  {busy === p.id ? '…' : 'Accept'}
                </Button>
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  data-testid="proposal-dismiss"
                  disabled={busy !== null}
                  onClick={() => void dismiss(p.id)}
                  className="px-3 py-1 text-xs"
                >
                  Dismiss
                </Button>
              </div>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

type TodayResponse = {
  overdue: Task[];
  due_today: Task[];
  scheduled_today: Task[];
  upcoming_week?: { date: string; count: number }[];
  awaiting_count: number;
  active_timer?: { task_id: string; started_at: string };
  proposals: StudyProposalView[];
};

function TodaySection({
  title,
  color,
  tasks,
  emptyText,
  showTime,
}: {
  title: string;
  color: 'red' | 'amber' | 'slate';
  tasks: Task[];
  emptyText: string;
  showTime?: boolean;
}): JSX.Element {
  const dotColor =
    color === 'red' ? 'bg-red-500' : color === 'amber' ? 'bg-amber-500' : 'bg-slate-400';

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
          {tasks.map((t) => {
            // Phase 31.9: study-reminders carry a study_course_id
            // and get a 📖-marker that links to the course. The
            // marker is the only thing visually distinguishing a
            // soft reminder from a real project task in the same
            // due_today list.
            const studyLink = t.study_course_id ? `/courses/${t.study_course_id}` : null;
            return (
              <li
                key={t.id}
                data-testid={studyLink ? 'today-study-task' : 'today-task'}
                className="rounded border border-border p-2 text-sm bg-white dark:bg-slate-950"
              >
                <Link
                  to={`/tasks/${t.id}`}
                  className="text-slate-800 dark:text-slate-100 hover:underline"
                >
                  {studyLink ? '📖 ' : ''}
                  {t.title}
                </Link>
                {studyLink && (
                  <Link
                    to={studyLink}
                    data-testid="today-study-marker"
                    className="ml-2 text-[10px] text-indigo-700 dark:text-indigo-300 hover:underline"
                  >
                    open course →
                  </Link>
                )}
                <span className="ml-2 text-[10px] text-slate-400 font-mono">
                  {showTime && t.start_at
                    ? new Date(t.start_at).toLocaleTimeString([], {
                        hour: '2-digit',
                        minute: '2-digit',
                      })
                    : t.due_at
                      ? new Date(t.due_at).toLocaleDateString()
                      : t.status}
                </span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

/**
 * ActiveTimerRow — when a time entry is open, show the task link
 * plus elapsed time. The elapsed text ticks once a minute (parent
 * state); the underlying timeentry itself is from the API.
 */
function ActiveTimerRow({
  timer,
  now,
}: {
  timer: { task_id: string; started_at: string };
  now: number;
}): JSX.Element {
  const startedMs = new Date(timer.started_at).getTime();
  const elapsedMs = Math.max(0, now - startedMs);
  const elapsedMin = Math.floor(elapsedMs / 60_000);
  const elapsedH = Math.floor(elapsedMin / 60);
  const elapsedM = elapsedMin % 60;
  const elapsedLabel = elapsedH > 0 ? `${elapsedH}h ${elapsedM}m` : `${elapsedM}m`;

  return (
    <div
      data-testid="active-timer-row"
      className="rounded border border-emerald-300 bg-emerald-50 p-3 text-emerald-900 text-sm flex items-center justify-between"
    >
      <div>
        <span
          className="inline-block h-2 w-2 rounded-full bg-emerald-500 mr-2 animate-pulse"
          aria-hidden
        />
        Working on{' '}
        <Link to={`/tasks/${timer.task_id}`} className="underline font-medium">
          {timer.task_id.slice(0, 8)}
        </Link>
        {' · '}
        <span className="font-mono">{elapsedLabel}</span>
      </div>
      <Link to={`/tasks/${timer.task_id}#timer`} className="text-xs underline">
        stop
      </Link>
    </div>
  );
}

/**
 * UpcomingWeek — one row per day for the next 7 days. Compact:
 * one line per date with the count of due tasks on that day.
 *
 * Server pre-formats the date as YYYY-MM-DD so the client doesn't
 * have to deal with TZ arithmetic. We render it as a localised
 * weekday + day-of-month for readability.
 */
function UpcomingWeek({ days }: { days: { date: string; count: number }[] }): JSX.Element {
  if (days.length === 0) return <></>;
  return (
    <div>
      <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200 mb-2">Next 7 days</h2>
      <ul className="space-y-1">
        {days.map((d) => {
          const date = new Date(d.date + 'T00:00:00');
          const label = date.toLocaleDateString(undefined, {
            weekday: 'short',
            day: 'numeric',
            month: 'short',
          });
          return (
            <li
              key={d.date}
              data-testid="upcoming-day-row"
              className="flex justify-between items-center rounded border border-border px-3 py-1 text-sm bg-white dark:bg-slate-950"
            >
              <span className="text-slate-700 dark:text-slate-200">{label}</span>
              <span className="text-slate-500 font-mono">{d.count} due</span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
