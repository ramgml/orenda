import { useCallback, useEffect, useState } from 'react';

import { api, type ReviewQueueItem, type Task } from '@/shared/api/client';
import { TaskNumberChip } from '@/features/tasks/TaskNumberChip';
import { Button } from '@/shared/ui/button';
import { EmptyState } from '@/shared/ui/EmptyState';
import { ErrorBanner } from '@/shared/ui/ErrorBanner';
import { Loading } from '@/shared/ui/Loading';
import { useWebSocketTopic } from '@/shared/ws';

/**
 * Phase 19: review queue — one screen with every task waiting on the
 * owner's verdict. Closes the agent → human half of the delegation
 * loop.
 *
 * Data flow:
 *  - GET /api/v1/review-queue → {tasks: ReviewQueueItem[], count}
 *  - WS topic "tasks" → re-fetch (any task mutation can move a task
 *    in or out of the queue)
 *  - Inline Accept / Reject buttons → POST /api/v1/tasks/{id}/review
 *    (Phase 3 endpoint, surfaced here for one-click resolution)
 *
 * Empty state: a green check + a one-liner. Acceptance is the goal;
 * an empty queue is the steady state.
 */
export function ReviewPage(): JSX.Element {
  const [items, setItems] = useState<ReviewQueueItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async (): Promise<void> => {
    try {
      const r = await api.listReviewQueue();
      setItems(r.tasks ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Re-fetch on any task event (move, status change, comment, review).
  // The full list is cheap to refresh and we don't track per-task
  // membership here, so a blanket re-fetch is the simplest correct
  // approach.
  useWebSocketTopic('tasks', () => {
    void load();
  });

  async function onAccept(item: ReviewQueueItem): Promise<void> {
    if (!item.task) return;
    setBusyId(item.task.id);
    setError(null);
    try {
      await api.reviewTask(item.task.id, 'approve');
      // Optimistic remove; the WS event will reconcile if needed.
      setItems((cur) => cur.filter((i) => i.task.id !== item.task.id));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyId(null);
    }
  }

  async function onReject(item: ReviewQueueItem): Promise<void> {
    if (!item.task) return;
    const comment = window.prompt('What needs to change? (the agent will see your feedback)', '');
    if (comment === null) return; // cancelled
    setBusyId(item.task.id);
    setError(null);
    try {
      await api.reviewTask(item.task.id, 'reject', comment);
      // Task is now awaiting the agent — drop it from the human queue.
      setItems((cur) => cur.filter((i) => i.task.id !== item.task.id));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyId(null);
    }
  }

  function openTask(t: Task): void {
    // Inline navigation; the queue page itself doesn't preserve scroll
    // across modal closes, so a full nav is fine here.
    window.location.href = `/tasks/${t.id}`;
  }

  return (
    <section className="space-y-6 p-6 max-w-3xl mx-auto">
      <header>
        <h1 className="text-2xl font-semibold">Review queue</h1>
        <p className="text-sm text-slate-500 mt-1">Tasks waiting for your verdict. Newest first.</p>
      </header>

      {error && <ErrorBanner message={error} />}

      {loading ? (
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState icon="✓" message="Nothing to review — everything is up to date." />
      ) : (
        <ul className="space-y-2">
          {items.map((it) => (
            <ReviewRow
              key={it.task.id}
              item={it}
              busy={busyId === it.task.id}
              onAccept={() => void onAccept(it)}
              onReject={() => void onReject(it)}
              onOpen={() => openTask(it.task)}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function ReviewRow({
  item,
  busy,
  onAccept,
  onReject,
  onOpen,
}: {
  item: ReviewQueueItem;
  busy: boolean;
  onAccept: () => void;
  onReject: () => void;
  onOpen: () => void;
}): JSX.Element {
  const projectLabel = item.project_name || 'Inbox';
  const projectColor = item.project_color || '#6b7280';
  return (
    <li
      data-testid="review-row"
      className="rounded border border-border bg-background dark:bg-background p-3 flex gap-3 items-start"
    >
      <Button
        type="button"
        variant="ghost"
        onClick={onOpen}
        className="flex-1 min-w-0 h-auto justify-start p-0 text-left"
      >
        <div className="flex items-center gap-2 text-xs text-slate-500">
          <span
            aria-hidden
            className="inline-block h-2 w-2 rounded-full"
            style={{ background: projectColor }}
          />
          <span>{projectLabel}</span>
          <span>·</span>
          <span>{new Date(item.task.updated_at).toLocaleString()}</span>
        </div>
        <p className="text-foreground dark:text-foreground mt-1">
          <TaskNumberChip number={item.task.number} /> <span>{item.task.title}</span>
        </p>
        {item.task.description && (
          <p className="text-xs text-slate-500 mt-1 line-clamp-2">{item.task.description}</p>
        )}
        <div className="flex gap-3 text-[10px] text-slate-400 mt-1 font-mono">
          <span>{item.task.status}</span>
          <span>{item.task.priority}</span>
          {item.task.assignee_type && item.task.assignee_id && (
            <span>
              {item.task.assignee_type}:{item.task.assignee_id.slice(0, 6)}
            </span>
          )}
        </div>
      </Button>
      <div className="flex flex-col gap-1 items-end shrink-0">
        <Button
          type="button"
          size="sm"
          onClick={onAccept}
          disabled={busy}
          data-testid="review-accept"
          className="bg-emerald-600 hover:bg-emerald-700 text-xs"
        >
          Accept
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={onReject}
          disabled={busy}
          data-testid="review-reject"
          className="bg-amber-600 hover:bg-amber-700 text-xs"
        >
          Return
        </Button>
      </div>
    </li>
  );
}
