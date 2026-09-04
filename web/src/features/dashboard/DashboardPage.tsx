import { useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router';

import { api, type OverviewResponse } from '@/shared/api/client';
import { ErrorBanner } from '@/shared/ui/ErrorBanner';
import { Loading } from '@/shared/ui/Loading';

/**
 * Task 107: system Dashboard — the "readings of the system" screen,
 * deliberately separate from Today (the personal daily slice).
 *
 * Shows entity counts (projects, tasks by status, wiki pages,
 * timed items) and one interactive chart: the 30-day
 * created-vs-completed task series. Hovering a bar shows exact
 * values; a range switch (7/30 days) filters the series.
 *
 * Single round-trip: GET /api/v1/overview.
 */
export function DashboardPage(): JSX.Element {
  const [data, setData] = useState<OverviewResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [rangeDays, setRangeDays] = useState<7 | 30>(30);
  const [hover, setHover] = useState<number | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .getOverview()
      .then((r) => {
        if (!cancelled) setData(r);
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const activity = useMemo(() => {
    if (!data) return [];
    return data.activity.slice(-rangeDays);
  }, [data, rangeDays]);

  if (loading) return <Loading />;
  if (error)
    return (
      <div className="p-6">
        <ErrorBanner message={`Failed to load overview: ${error}`} />
      </div>
    );
  if (!data) return <Loading />;

  const tasksTotal = Object.values(data.tasks_by_status).reduce((a, b) => a + b, 0);
  const maxBar = Math.max(1, ...activity.map((d) => Math.max(d.created, d.completed)));
  const hoverDay = hover !== null ? activity[hover] : null;

  return (
    <div className="p-6 max-w-5xl mx-auto space-y-6" data-testid="dashboard-page">
      <header>
        <h1 className="text-2xl font-semibold">Dashboard</h1>
        <p className="text-sm text-slate-500 mt-1">
          System readings —{' '}
          <Link to="/" className="text-orenda-600 hover:underline">
            your day lives in Today
          </Link>
          .
        </p>
      </header>

      {/* Metric cards */}
      <section className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <MetricCard label="Projects" value={data.projects} />
        <MetricCard label="Tasks" value={tasksTotal} />
        <MetricCard label="Wiki pages" value={data.wiki_pages} />
        <MetricCard label="Timed items (30d)" value={data.events} />
      </section>

      {/* Tasks by status */}
      <section>
        <h2 className="text-sm font-medium text-slate-500 uppercase tracking-wide mb-2">
          Tasks by status
        </h2>
        <div className="flex flex-wrap gap-2">
          {Object.entries(data.tasks_by_status).map(([status, count]) => (
            <span
              key={status}
              className="rounded border border-border bg-slate-50 dark:bg-slate-800 px-2.5 py-1 text-sm"
              data-testid={`status-${status}`}
            >
              {status}: <strong>{count}</strong>
            </span>
          ))}
          {Object.keys(data.tasks_by_status).length === 0 && (
            <span className="text-sm text-slate-500">No tasks yet.</span>
          )}
        </div>
      </section>

      {/* Activity chart */}
      <section>
        <div className="flex items-center justify-between mb-2">
          <h2 className="text-sm font-medium text-slate-500 uppercase tracking-wide">
            Activity — created vs completed
          </h2>
          <div className="flex gap-1" role="group" aria-label="Chart range">
            {([7, 30] as const).map((d) => (
              <button
                key={d}
                type="button"
                onClick={() => setRangeDays(d)}
                aria-pressed={rangeDays === d}
                className={`rounded px-2 py-0.5 text-xs border ${
                  rangeDays === d
                    ? 'bg-orenda-500 text-white border-orenda-500'
                    : 'border-border hover:bg-slate-100 dark:hover:bg-slate-800'
                }`}
              >
                {d}d
              </button>
            ))}
          </div>
        </div>
        <div
          className="relative flex items-end gap-[2px] h-40 rounded border border-border bg-slate-50 dark:bg-slate-800 p-2"
          data-testid="activity-chart"
          onMouseLeave={() => setHover(null)}
        >
          {activity.map((day, i) => (
            <div
              key={day.date}
              className="relative flex flex-col justify-end flex-1 h-full gap-[1px] cursor-pointer"
              onMouseEnter={() => setHover(i)}
              data-testid={`bar-${day.date}`}
              data-created={day.created}
              data-completed={day.completed}
            >
              <div
                className="w-full bg-emerald-500 rounded-t"
                style={{
                  height: `${Math.max((day.completed / maxBar) * 100, day.completed > 0 ? 1.5 : 0)}%`,
                }}
              />
              <div
                className="w-full bg-orenda-500 rounded-t"
                style={{
                  height: `${Math.max((day.created / maxBar) * 100, day.created > 0 ? 1.5 : 0)}%`,
                }}
              />
            </div>
          ))}
          {hoverDay && (
            <div
              className="absolute top-2 right-2 rounded bg-white dark:bg-slate-900 border border-border px-2 py-1 text-xs shadow"
              data-testid="chart-tooltip"
            >
              <div className="font-medium">{hoverDay.date}</div>
              <div>created: {hoverDay.created}</div>
              <div>completed: {hoverDay.completed}</div>
            </div>
          )}
        </div>
        <div className="mt-1 flex gap-4 text-xs text-slate-500">
          <span className="flex items-center gap-1">
            <span className="inline-block w-2 h-2 bg-orenda-500 rounded-sm" /> created
          </span>
          <span className="flex items-center gap-1">
            <span className="inline-block w-2 h-2 bg-emerald-500 rounded-sm" /> completed
          </span>
        </div>
      </section>
    </div>
  );
}

function MetricCard({ label, value }: { label: string; value: number }): JSX.Element {
  return (
    <div
      className="rounded border border-border bg-white dark:bg-slate-900 p-4"
      data-testid={`metric-${label.toLowerCase().replace(/\s+/g, '-')}`}
    >
      <div className="text-2xl font-semibold" data-testid="metric-value">
        {value.toLocaleString('en-US')}
      </div>
      <div className="text-sm text-slate-500 mt-1">{label}</div>
    </div>
  );
}
