import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

import { useAuth } from '@/features/auth/AuthContext';
import {
  api,
  type ChildTaskProgress,
  type Checklist,
  type ChecklistItem,
  type Comment as TaskComment,
  type Tag,
  type Task,
  type TaskActivity,
  type TaskAttachment,
} from '@/shared/api/client';
import { queueUpdateTask } from '@/shared/offline/outbox';
import { useWebSocketTopic } from '@/shared/ws';
import { StartTimer } from '@/features/tasks/TimerWidget';
import { usePasteImage } from '@/features/attachments/usePasteImage';

import { CommentsList } from './CommentsList';
import { ChildTasksList } from './ChildTasksList';
import { AttachmentsList } from './AttachmentsList';
import { ChecklistsList } from './ChecklistsList';
import { TagsList } from './TagsList';
import { BlockedByList } from './BlockedByList';
import { TaskLink } from './TaskModal';
import { TaskNumberChip } from './TaskNumberChip';
import { TaskFieldControls } from './TaskFieldControls';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Textarea } from '@/shared/ui/textarea';

/**
 * Shared task-detail content.
 *
 * Used in two places:
 *   - `TaskViewPage` for the standalone `/tasks/:id` deep-link (full
 *     page inside the layout).
 *   - `TaskModal` for the Trello-style overlay that the kanban opens.
 *
 * The component owns its own state (load, edit, post-comment, review)
 * and listens on the `tasks` WS topic so live updates work in both
 * contexts. The optional `onClose` hook lets the modal close itself
 * after a destructive action without needing the body to know about
 * react-router.
 */
/**
 * patchTaskOrQueue sends a PATCH through the offline outbox when the
 * client is disconnected, and falls back to the regular axios call
 * otherwise. Phase Wave 4 PR 2 closes the audit gap "PWA outbox:
 * only create-task" — updates, moves, and comments now share the
 * same offline-safe path as the original create.
 *
 * The shape mirrors api.patchTask; we return the fresh Task so the
 * caller can update local state. On the queue path we can't return
 * the canonical server-side row (the sync hasn't happened yet) — we
 * merge the patch over the task the caller already holds, so every
 * required field stays intact and nothing is fabricated.
 */
async function patchTaskOrQueue(task: Task, patch: Partial<Task>): Promise<Task> {
  if (typeof navigator !== 'undefined' && !navigator.onLine) {
    await queueUpdateTask(task.id, patch);
    // Optimistic local merge over the existing task — the queue will
    // reconcile it with the server on sync.
    return { ...task, ...patch };
  }
  return api.patchTask(task.id, patch);
}

export function TaskViewBody({
  taskId,
  onClose,
}: {
  taskId: string;
  /** Optional hook fired after a successful destructive action (e.g. delete). */
  onClose?: () => void;
}): JSX.Element {
  const { user } = useAuth();
  const [task, setTask] = useState<Task | null>(null);
  // Parent task is fetched lazily (only when the current task has a
  // parent_task_id) so top-level tasks don't pay for an extra GET.
  const [parentTask, setParentTask] = useState<Task | null>(null);
  const [childTasks, setChildTasks] = useState<Task[]>([]);
  const [childProgress, setChildProgress] = useState<ChildTaskProgress>({ total: 0, done: 0 });
  const [attachments, setAttachments] = useState<TaskAttachment[]>([]);
  const [checklists, setChecklists] = useState<Checklist[]>([]);
  const [checklistItems, setChecklistItems] = useState<Record<string, ChecklistItem[]>>({});
  const [activity, setActivity] = useState<TaskActivity[]>([]);
  const [comments, setComments] = useState<TaskComment[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [composer, setComposer] = useState('');
  const [busy, setBusy] = useState(false);
  const [reviewComment, setReviewComment] = useState('');

  async function load(): Promise<void> {
    try {
      const [t, childrenR, attR, actR, comments, clR, tagsR] = await Promise.all([
        api.getTask(taskId),
        api.listChildTasks(taskId),
        api.listTaskAttachments(taskId),
        api.listTaskActivity(taskId),
        api.listTaskComments(taskId),
        api.listChecklists(taskId),
        api.listTaskTags(taskId),
      ]);
      setTask(t);
      // Lazily fetch the parent for the breadcrumb. Done after
      // setTask so we know the parent id; failures are silent
      // (the breadcrumb just won't render).
      if (t.parent_task_id) {
        try {
          setParentTask(await api.getTask(t.parent_task_id));
        } catch {
          setParentTask(null);
        }
      } else {
        setParentTask(null);
      }
      setChildTasks(childrenR.tasks ?? []);
      setChildProgress(childrenR.progress ?? { total: 0, done: 0 });
      setAttachments(attR.attachments ?? []);
      setActivity(actR.activity ?? []);
      setComments(comments.comments ?? []);
      const cls = clR.checklists ?? [];
      setChecklists(cls);
      // Hydrate items per list in parallel; skip a list silently if
      // its items can't load — better to show an empty list than to
      // nuke the rest of the page.
      const pairs = await Promise.all(
        cls.map(async (l) => {
          try {
            const r = await api.listChecklistItems(taskId, l.id);
            return [l.id, r.items ?? []] as const;
          } catch {
            return [l.id, [] as ChecklistItem[]] as const;
          }
        }),
      );
      const next: Record<string, ChecklistItem[]> = {};
      for (const [id, its] of pairs) next[id] = its;
      setChecklistItems(next);
      setTags(tagsR.tags ?? []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    setTask(null); // Reset between taskIds so loading state is honest.
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  // Live updates on any task event.
  useWebSocketTopic('tasks', () => {
    load();
  });

  // Ctrl+V anywhere on the page → drop a screenshot into this task's
  // attachments, as long as focus is not inside an editable surface
  // (handled inside the hook).
  const onPasteImage = useCallback(
    async (file: File) => {
      try {
        const a = await api.uploadTaskAttachment(taskId, file);
        setAttachments((cur) => [...cur, a]);
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
      }
    },
    [taskId],
  );
  usePasteImage(onPasteImage);

  async function onPostComment(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (!composer.trim()) return;
    setBusy(true);
    try {
      await api.createTaskComment(taskId, composer.trim());
      setComposer('');
      const list = await api.listTaskComments(taskId);
      setComments(list.comments ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onReview(decision: 'approve' | 'reject'): Promise<void> {
    setBusy(true);
    try {
      await api.reviewTask(taskId, decision, reviewComment);
      setReviewComment('');
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  // Description patch.
  const onSaveDescription = async (description: string): Promise<void> => {
    if (!task) return;
    setBusy(true);
    try {
      const t = await patchTaskOrQueue(task, { description });
      setTask(t);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // Title patch.
  const onSaveTitle = async (title: string): Promise<void> => {
    if (!title.trim() || !task) return;
    setBusy(true);
    try {
      const t = await patchTaskOrQueue(task, { title: title.trim() });
      setTask(t);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // Color patch. Empty string explicitly clears the colour label
  // (per the *string semantics in the backend). The picker always
  // shows a value; "clear" is its own button so users have an
  // obvious path to remove the stripe.
  const onSaveColor = async (color: string): Promise<void> => {
    if (!task) return;
    setBusy(true);
    try {
      const t = await patchTaskOrQueue(task, { color });
      setTask(t);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  // Due patch (T90). Empty string clears the due date — the backend
  // parseOptionalTime("") returns nil, so the field resets. A date
  // becomes local midnight ISO (QuickCapture convention, Phase
  // 30.10): date-only inputs are TZ-naive so "the 15th" is the 15th
  // in the operator's browser.
  const onSaveDue = async (date: string): Promise<void> => {
    if (!task) return;
    setBusy(true);
    try {
      const due_at = date === '' ? '' : new Date(`${date}T00:00:00`).toISOString();
      const t = await patchTaskOrQueue(task, { due_at });
      setTask(t);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  async function onDelete(): Promise<void> {
    if (!window.confirm('Delete this task? This cannot be undone.')) return;
    setBusy(true);
    try {
      await api.deleteTask(taskId);
      onClose?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  const canReview = task?.status === 'review' && (task.assignee_type === 'agent' || user !== null);

  if (error && !task) {
    return <p className="text-red-700">{error}</p>;
  }
  if (!task) {
    return <p className="text-slate-500">Loading…</p>;
  }

  return (
    <section className="grid gap-6 md:grid-cols-3">
      <div className="md:col-span-2 space-y-6">
        {task.parent_task_id && (
          <div className="text-xs text-slate-500 flex items-center gap-1.5 -mb-2">
            <span aria-hidden="true">↳</span>
            <span>Child of</span>
            <TaskLink
              taskId={task.parent_task_id}
              className="text-orenda-600 hover:underline truncate max-w-xs"
            >
              {parentTask?.title ?? 'parent task'}
            </TaskLink>
          </div>
        )}

        <div className="flex items-center gap-2">
          <TaskNumberChip number={task.number} />
          <div className="flex-1 min-w-0">
            <EditableTitle value={task.title} onSave={onSaveTitle} busy={busy} />
          </div>
        </div>

        <DescriptionEditor value={task.description ?? ''} onSave={onSaveDescription} busy={busy} />

        {task.context_md && (
          <details className="rounded border border-border p-3">
            <summary className="cursor-pointer text-sm text-slate-500">Agent context (md)</summary>
            <pre className="mt-2 text-xs whitespace-pre-wrap font-mono">{task.context_md}</pre>
          </details>
        )}

        {task.agent_notes && (
          <div className="rounded border border-amber-300 bg-amber-50 p-3 text-sm">
            <p className="font-semibold text-amber-900 mb-1">Agent note</p>
            <p className="text-amber-900 whitespace-pre-wrap">{task.agent_notes}</p>
          </div>
        )}

        {canReview && (
          <div className="rounded border border-border p-3 space-y-2">
            <p className="text-sm font-semibold">Review</p>
            <Textarea
              value={reviewComment}
              onChange={(e) => setReviewComment(e.target.value)}
              placeholder="Optional feedback for the agent"
              rows={2}
              className="w-full text-sm"
            />
            <div className="flex gap-2">
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => onReview('approve')}
                disabled={busy}
                className="bg-green-600 hover:bg-green-700 text-white"
              >
                Approve
              </Button>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => onReview('reject')}
                disabled={busy}
                className="bg-amber-600 hover:bg-amber-700 text-white"
              >
                Reject
              </Button>
            </div>
          </div>
        )}

        <ChildTasksList
          taskId={task.id}
          projectId={task.project_id}
          initialTasks={childTasks}
          initialProgress={childProgress}
        />

        <AttachmentsList taskId={task.id} initial={attachments} />

        <ChecklistsList taskId={task.id} initialLists={checklists} initialItems={checklistItems} />

        <div>
          <h2 className="text-sm font-semibold mb-2 text-slate-500">
            Comments ({comments.length})
          </h2>
          <CommentsList comments={comments} />
          <form onSubmit={onPostComment} className="mt-2 flex gap-2">
            <Input
              type="text"
              value={composer}
              onChange={(e) => setComposer(e.target.value)}
              placeholder="Add a comment (supports @user:<id> / @agent:<id>)"
              className="flex-1"
            />
            <Button type="submit" size="sm" disabled={busy || !composer.trim()}>
              Post
            </Button>
          </form>
        </div>

        <ActivityLog items={activity} />

        {onClose && (
          <div className="pt-2 border-t border-border">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onDelete}
              disabled={busy}
              className="border-red-300 text-red-700 hover:bg-red-50 hover:text-red-700"
            >
              Delete task
            </Button>
          </div>
        )}
      </div>

      <aside className="space-y-3 text-sm">
        <TaskFieldControls
          status={task.status}
          priority={task.priority}
          assigneeType={task.assignee_type ?? ''}
          assigneeID={task.assignee_id ?? ''}
          taskID={task.id}
          busy={busy}
          projectID={task.project_id ?? ''}
          onChanged={(t) => setTask(t)}
          onError={(msg) => setError(msg)}
        />
        {task.awaiting && task.awaiting !== 'none' && (
          <div className="rounded border border-blue-300 bg-blue-50 p-3 text-blue-900 text-sm">
            Awaiting <span className="font-mono">{task.awaiting}</span> action
          </div>
        )}
        <DueEditor task={task} busy={busy} onSaveDue={onSaveDue} />
        <ColorPicker value={task.color} onSave={onSaveColor} busy={busy} />
        <TagsList taskId={taskId} initial={tags} />
        <BlockedByList taskId={taskId} projectId={task.project_id || ''} />
        <div className="rounded border border-border p-3">
          <p className="text-xs text-slate-500 mb-1">Time tracking</p>
          <p className="font-mono mb-2">{(task.time_spent_s / 60).toFixed(1)} min</p>
          <Button
            type="button"
            size="sm"
            onClick={() => StartTimer(task)}
            className="w-full text-xs"
          >
            Start timer
          </Button>
        </div>
      </aside>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function EditableTitle({
  value,
  onSave,
  busy,
}: {
  value: string;
  onSave: (s: string) => Promise<void>;
  busy: boolean;
}): JSX.Element {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);

  useEffect(() => {
    setDraft(value);
  }, [value]);

  if (!editing) {
    return (
      <h1
        className="text-2xl font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-900 rounded px-2 py-1"
        onClick={() => setEditing(true)}
        title="Click to edit"
      >
        {value}
      </h1>
    );
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (draft.trim() && draft !== value) {
          void onSave(draft);
        }
        setEditing(false);
      }}
      className="flex gap-2"
    >
      <Input
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            setDraft(value);
            setEditing(false);
          }
        }}
        className="text-2xl font-semibold h-auto bg-transparent border-0 border-b border-orenda-500 rounded-none focus-visible:ring-0 focus-visible:ring-offset-0 px-0 flex-1"
      />
      <Button
        type="submit"
        size="sm"
        disabled={busy || !draft.trim() || draft === value}
        className="text-xs"
      >
        Save
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => {
          setDraft(value);
          setEditing(false);
        }}
        className="text-xs"
      >
        Cancel
      </Button>
    </form>
  );
}

export function DescriptionEditor({
  value,
  onSave,
  busy,
}: {
  value: string;
  onSave: (s: string) => Promise<void>;
  busy: boolean;
}): JSX.Element {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);

  useEffect(() => {
    setDraft(value);
  }, [value]);

  if (!editing) {
    if (!value) {
      return (
        <Button
          type="button"
          variant="link"
          onClick={() => setEditing(true)}
          className="block h-auto p-0 text-slate-400 text-sm italic hover:text-slate-700"
        >
          + Add description
        </Button>
      );
    }
    return (
      <div
        onClick={() => setEditing(true)}
        className="cursor-pointer rounded px-2 py-1 w-full hover:bg-slate-50 dark:hover:bg-slate-900"
      >
        <article className="prose dark:prose-invert max-w-none text-sm">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              // A click on a link inside the description must follow
              // the link, not flip the editor into edit mode.
              a: ({ node, ...props }) => <a {...props} onClick={(e) => e.stopPropagation()} />,
            }}
          >
            {value}
          </ReactMarkdown>
        </article>
      </div>
    );
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (draft !== value) {
          void onSave(draft);
        }
        setEditing(false);
      }}
      className="space-y-2"
    >
      <Textarea
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
            e.preventDefault();
            if (draft !== value) void onSave(draft);
            setEditing(false);
          } else if (e.key === 'Escape') {
            setDraft(value);
            setEditing(false);
          }
        }}
        rows={4}
        placeholder="What's this task about? Markdown is supported."
        className="w-full text-sm font-mono"
      />
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={busy || draft === value} className="text-xs">
          Save
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => {
            setDraft(value);
            setEditing(false);
          }}
          className="text-xs"
        >
          Cancel
        </Button>
        <span className="text-xs text-slate-400 self-center">
          Ctrl+Enter to save · Esc to cancel
        </span>
      </div>
    </form>
  );
}

/**
 * Due date editor (T90) + "Show in calendar" deep link.
 *
 * The date input commits on change (one PATCH per picked date —
 * native pickers already debounce implicit keystrokes) and offers a
 * "clear" path that PATCHes the empty string, mirroring the
 * ColorPicker's explicit-remove affordance. The link jumps to
 * /calendar?date=… seeded with the due date (falling back to
 * start_at); with neither field set there is nothing to show, so
 * the link is not rendered.
 */
function DueEditor({
  task,
  busy,
  onSaveDue,
}: {
  task: Task;
  busy: boolean;
  onSaveDue: (date: string) => Promise<void>;
}): JSX.Element {
  const dateValue = toDateInputValue(task.due_at ?? task.start_at ?? '');

  function commit(next: string): void {
    const current = toDateInputValue(task.due_at ?? '');
    if (next === current) return;
    void onSaveDue(next);
  }

  return (
    <div className="rounded border border-border p-3 space-y-2">
      <p className="text-xs text-slate-500">Due</p>
      <div className="flex items-center gap-2">
        <Input
          type="date"
          defaultValue={dateValue}
          key={dateValue}
          onChange={(e) => commit(e.target.value)}
          disabled={busy}
          className="h-7 text-xs"
          title="Due date"
        />
        {task.due_at && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={busy}
            onClick={() => void onSaveDue('')}
            className="h-6 px-2 text-xs"
            title="Remove the due date"
          >
            clear
          </Button>
        )}
      </div>
      {(task.due_at ?? task.start_at) && (
        <Link
          to={`/calendar?date=${dateValue}`}
          className="text-xs text-orenda-600 hover:underline"
        >
          Show in calendar
        </Link>
      )}
    </div>
  );
}

// toDateInputValue extracts the YYYY-MM-DD an <input type="date">
// needs from an ISO timestamp, in the timestamp's calendar date.
function toDateInputValue(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  return `${y}-${m}-${day}`;
}

/**
 * Colour label picker (Phase 13).
 *
 * The picker is debounced implicitly by blur — we only call onSave
 * when the input loses focus or the user picks a colour. This keeps
 * the backend round-trips to one per change instead of one per
 * keystroke, and avoids emitting activity rows for intermediate
 * values.
 *
 * Layout: small swatch (input[type=color]) + hex readout + "clear"
 * button. The swatch is the actual picker; the hex text is just a
 * human-readable echo.
 */
function ColorPicker({
  value,
  onSave,
  busy,
}: {
  value: string;
  onSave: (color: string) => Promise<void>;
  busy: boolean;
}): JSX.Element {
  const [draft, setDraft] = useState(value);

  // Re-sync draft when the parent task reloads (WS event, save
  // round-trip, etc.). Without this, an external colour change
  // wouldn't show up until the user unfocused the picker.
  useEffect(() => {
    setDraft(value);
  }, [value]);

  function commit(next: string): void {
    if (next === value) return;
    setDraft(next);
    void onSave(next);
  }

  return (
    <div className="rounded border border-border p-3 space-y-2">
      <p className="text-xs text-slate-500">Colour label</p>
      <div className="flex items-center gap-2">
        <input
          type="color"
          value={draft || '#3b82f6'}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => commit(draft)}
          disabled={busy}
          className="h-7 w-9 rounded border border-border bg-transparent cursor-pointer"
          title="Pick a colour"
        />
        <span className="font-mono text-xs text-muted-foreground flex-1">{draft || 'none'}</span>
        {draft && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={busy}
            onClick={() => commit('')}
            className="h-6 px-2 text-xs"
            title="Remove the colour label"
          >
            clear
          </Button>
        )}
      </div>
    </div>
  );
}

function ActivityLog({ items }: { items: TaskActivity[] }): JSX.Element {
  // Action verbs we render as a human label.
  //
  // Phase 27.9: pre-27.9 rows stored status/priority/assignee
  // without the `task.` prefix (status_changed, priority_changed,
  // assigned). We keep a fallback copy with both spellings so old
  // audit rows still render the human label instead of the raw
  // action name. New rows use the prefixed form (the backend
  // constants adopt the prefix in 27.9).
  const verb: Record<string, string> = {
    'task.created': 'created the task',
    'task.moved': 'moved the task',
    'task.status_changed': 'changed the status',
    status_changed: 'changed the status',
    'task.title_changed': 'changed the title',
    title_changed: 'changed the title',
    'task.priority_changed': 'changed the priority',
    priority_changed: 'changed the priority',
    'task.assigned': 'assigned the task',
    assigned: 'assigned the task',
    'task.claimed': 'claimed the task',
    'task.released': 'released the task',
    'task.submitted': 'submitted the task for review',
    'task.approved': 'approved the task',
    'task.rejected': 'rejected the task',
    'task.commented': 'left a comment',
    'task.attachment_added': 'attached a file',
    'task.subtask_added': 'added a subtask (legacy)',
    'task.subtask_done': 'completed a subtask (legacy)',
    'task.child_added': 'added a child task',
    'task.child_status_changed': 'changed child task status',
    'task.checklist_added': 'added a checklist',
    'task.checklist_item_added': 'added a checklist item',
    'task.checklist_item_done': 'completed a checklist item',
    'task.tags_replaced': 'changed the tag set',
    'task.color_changed': 'changed the colour label',
  };
  // Most recent first.
  const sorted = useMemo(
    () => [...items].sort((a, b) => b.created_at.localeCompare(a.created_at)),
    [items],
  );
  return (
    <section>
      <h2 className="text-sm font-semibold mb-2 text-slate-500">Activity ({items.length})</h2>
      {sorted.length === 0 ? (
        <p className="text-xs text-slate-400 italic">No activity yet.</p>
      ) : (
        <ul className="space-y-1 text-sm">
          {sorted.map((a) => (
            <li key={a.id} className="flex items-baseline gap-2">
              <span className="text-xs text-slate-400 font-mono shrink-0">
                {a.created_at.slice(0, 16).replace('T', ' ')}
              </span>
              <span className="text-slate-500">
                {a.actor_type}:{a.actor_id.slice(0, 8)}
              </span>
              <span>{verb[a.action] ?? a.action}</span>
              {a.payload && a.payload !== '{}' && (
                <span className="text-xs text-slate-400 truncate">· {a.payload}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
