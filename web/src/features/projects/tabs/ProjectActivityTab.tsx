import { useEffect, useState } from 'react';
import { useParams } from 'react-router';

import { TaskLink } from '@/features/tasks/TaskModal';
import { activityDetails } from '@/features/tasks/activityDetails';
import { api, type ProjectActivityItem } from '@/shared/api/client';

// activityVerb maps an audit action to a human label — the same
// table as ActivityLog in TaskViewBody, kept in sync (task 113).
// Keys are stored unprefixed: the audit mixes `task.*` rows (27.9+)
// with legacy unprefixed rows (`tags_replaced`, `color_changed`,
// pre-27.9 status/priority/assignee), so both spellings normalize
// to one lookup — mirroring activityDetails().
const activityVerb: Record<string, string> = {
  created: 'created the task',
  moved: 'moved the task',
  status_changed: 'changed the status',
  title_changed: 'changed the title',
  priority_changed: 'changed the priority',
  assigned: 'assigned the task',
  claimed: 'claimed the task',
  released: 'released the task',
  submitted: 'submitted the task for review',
  approved: 'approved the task',
  rejected: 'rejected the task',
  reviewed: 'reviewed the task',
  commented: 'left a comment',
  attachment_added: 'attached a file',
  subtask_added: 'added a subtask (legacy)',
  subtask_done: 'completed a subtask (legacy)',
  child_added: 'added a child task',
  child_status_changed: 'changed child task status',
  checklist_added: 'added a checklist',
  checklist_item_added: 'added a checklist item',
  checklist_item_done: 'completed a checklist item',
  tags_replaced: 'changed the tag set',
  color_changed: 'changed the colour label',
};

// activityVerbFor strips the `task.` prefix (see activityVerb).
function activityVerbFor(action: string): string {
  const key = action.startsWith('task.') ? action.slice(5) : action;
  return activityVerb[key] ?? action;
}

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
          <span
            className="px-1.5 py-0.5 rounded text-xs uppercase tracking-wide bg-muted text-muted-foreground"
            title={it.payload}
          >
            {activityVerbFor(it.action)}
          </span>
          <TaskLink taskId={it.task_id} className="truncate hover:text-orenda-600 hover:underline">
            {it.task_title || it.task_id.slice(0, 8)}
          </TaskLink>
          {(() => {
            // Same human-detail contract as ActivityLog (task 113).
            const detail = activityDetails(it.action, it.payload);
            return (
              detail !== '' && (
                <span className="text-xs text-slate-400 min-w-0 truncate">{`· ${detail}`}</span>
              )
            );
          })()}
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
