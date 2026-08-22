import { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';

import { TaskLink } from '@/features/tasks/TaskModal';
import { api, type ProjectActivityItem } from '@/shared/api/client';

/**
 * /projects/:id/activity — cross-task activity feed.
 *
 * Aggregates every action (move / submit / review / comment / …) that
 * the audit log has for any task belonging to this project, newest
 * first. Each row links to the underlying task so the user can jump
 * from "beta was moved" straight to the task view.
 */
export function ProjectActivityTab(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const [items, setItems] = useState<ProjectActivityItem[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    api
      .getProjectActivity(id)
      .then((r) => {
        if (!cancelled) setItems(r.activity);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (error) return <p className="text-red-700 text-sm">{error}</p>;
  if (items === null) return <p className="text-slate-500">Loading activity…</p>;

  if (items.length === 0) {
    return (
      <div className="rounded border border-border p-6 text-center text-sm text-slate-500">
        No activity yet. Move a task, comment, or submit it for review to see entries here.
      </div>
    );
  }

  return (
    <ol className="space-y-2">
      {items.map((it) => (
        <li
          key={it.id}
          className="rounded border border-border bg-background px-3 py-2 text-sm flex items-center gap-3"
        >
          <span className="font-mono text-xs text-slate-400 w-32 flex-shrink-0">
            {formatRelative(it.created_at)}
          </span>
          <span className="px-1.5 py-0.5 rounded text-xs uppercase tracking-wide bg-muted text-muted-foreground">
            {it.action}
          </span>
          <TaskLink taskId={it.task_id} className="truncate hover:text-orenda-600 hover:underline">
            {it.task_title || it.task_id.slice(0, 8)}
          </TaskLink>
          <span className="ml-auto text-xs text-slate-400">{it.actor_type}</span>
        </li>
      ))}
    </ol>
  );
}

// formatRelative renders "5m ago", "3h ago", "2d ago" — coarse but
// matches what users expect from a feed where exact timestamps would
// be noise. Falls back to the absolute timestamp for anything > 7d.
function formatRelative(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso;
  const diff = Date.now() - then;
  if (diff < 0) return 'just now';
  const m = Math.floor(diff / 60_000);
  if (m < 1) return 'just now';
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 7) return `${d}d ago`;
  return new Date(iso).toISOString().slice(0, 10);
}
