import { FormEvent, useCallback, useEffect, useMemo, useState } from 'react'

import { useAuth } from '@/features/auth/AuthContext'
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
} from '@/shared/api/client'
import { queueUpdateTask } from '@/shared/offline/outbox'
import { useWebSocketTopic } from '@/shared/ws'
import { StartTimer } from '@/features/tasks/TimerWidget'
import { usePasteImage } from '@/features/attachments/usePasteImage'

import { CommentsList } from './CommentsList'
import { ChildTasksList } from './ChildTasksList'
import { AttachmentsList } from './AttachmentsList'
import { ChecklistsList } from './ChecklistsList'
import { TagsList } from './TagsList'
import { BlockedByList } from './BlockedByList'
import { TaskLink } from './TaskModal'
import { TaskFieldControls } from './TaskFieldControls'

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
 * return the optimistic merged task the caller can render until
 * the next fetch lands.
 */
async function patchTaskOrQueue(taskId: string, patch: Record<string, unknown>): Promise<Task> {
  if (typeof navigator !== 'undefined' && !navigator.onLine) {
    await queueUpdateTask(taskId, patch)
    // Optimistic local merge — the queue will replace it on sync.
    return { ...(patch as unknown as Task), id: taskId } as Task
  }
  return api.patchTask(taskId, patch)
}

export function TaskViewBody({
  taskId,
  onClose,
}: {
  taskId: string
  /** Optional hook fired after a successful destructive action (e.g. delete). */
  onClose?: () => void
}): JSX.Element {
  const { user } = useAuth()
  const [task, setTask] = useState<Task | null>(null)
  // Parent task is fetched lazily (only when the current task has a
  // parent_task_id) so top-level tasks don't pay for an extra GET.
  const [parentTask, setParentTask] = useState<Task | null>(null)
  const [childTasks, setChildTasks] = useState<Task[]>([])
  const [childProgress, setChildProgress] = useState<ChildTaskProgress>({ total: 0, done: 0 })
  const [attachments, setAttachments] = useState<TaskAttachment[]>([])
  const [checklists, setChecklists] = useState<Checklist[]>([])
  const [checklistItems, setChecklistItems] = useState<Record<string, ChecklistItem[]>>({})
  const [activity, setActivity] = useState<TaskActivity[]>([])
  const [comments, setComments] = useState<TaskComment[]>([])
  const [tags, setTags] = useState<Tag[]>([])
  const [error, setError] = useState<string | null>(null)
  const [composer, setComposer] = useState('')
  const [busy, setBusy] = useState(false)
  const [reviewComment, setReviewComment] = useState('')

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
      ])
      setTask(t)
      // Lazily fetch the parent for the breadcrumb. Done after
      // setTask so we know the parent id; failures are silent
      // (the breadcrumb just won't render).
      if (t.parent_task_id) {
        try {
          setParentTask(await api.getTask(t.parent_task_id))
        } catch {
          setParentTask(null)
        }
      } else {
        setParentTask(null)
      }
      setChildTasks(childrenR.tasks ?? [])
      setChildProgress(childrenR.progress ?? { total: 0, done: 0 })
      setAttachments(attR.attachments ?? [])
      setActivity(actR.activity ?? [])
      setComments(comments.comments ?? [])
      const cls = clR.checklists ?? []
      setChecklists(cls)
      // Hydrate items per list in parallel; skip a list silently if
      // its items can't load — better to show an empty list than to
      // nuke the rest of the page.
      const pairs = await Promise.all(
        cls.map(async (l) => {
          try {
            const r = await api.listChecklistItems(taskId, l.id)
            return [l.id, r.items ?? []] as const
          } catch {
            return [l.id, [] as ChecklistItem[]] as const
          }
        }),
      )
      const next: Record<string, ChecklistItem[]> = {}
      for (const [id, its] of pairs) next[id] = its
      setChecklistItems(next)
      setTags(tagsR.tags ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    setTask(null) // Reset between taskIds so loading state is honest.
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId])

  // Live updates on any task event.
  useWebSocketTopic('tasks', () => {
    load()
  })

  // Ctrl+V anywhere on the page → drop a screenshot into this task's
  // attachments, as long as focus is not inside an editable surface
  // (handled inside the hook).
  const onPasteImage = useCallback(
    async (file: File) => {
      try {
        const a = await api.uploadTaskAttachment(taskId, file)
        setAttachments((cur) => [...cur, a])
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
    },
    [taskId],
  )
  usePasteImage(onPasteImage)

  async function onPostComment(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!composer.trim()) return
    setBusy(true)
    try {
      await api.createTaskComment(taskId, composer.trim())
      setComposer('')
      const list = await api.listTaskComments(taskId)
      setComments(list.comments ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function onReview(decision: 'approve' | 'reject'): Promise<void> {
    setBusy(true)
    try {
      await api.reviewTask(taskId, decision, reviewComment)
      setReviewComment('')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // Description patch.
  const onSaveDescription = async (description: string): Promise<void> => {
    setBusy(true)
    try {
      const t = await patchTaskOrQueue(taskId, { description })
      setTask(t)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // Title patch.
  const onSaveTitle = async (title: string): Promise<void> => {
    if (!title.trim()) return
    setBusy(true)
    try {
      const t = await patchTaskOrQueue(taskId, { title: title.trim() })
      setTask(t)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // Color patch. Empty string explicitly clears the colour label
  // (per the *string semantics in the backend). The picker always
  // shows a value; "clear" is its own button so users have an
  // obvious path to remove the stripe.
  const onSaveColor = async (color: string): Promise<void> => {
    setBusy(true)
    try {
      const t = await patchTaskOrQueue(taskId, { color })
      setTask(t)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(): Promise<void> {
    if (!window.confirm('Delete this task? This cannot be undone.')) return
    setBusy(true)
    try {
      await api.deleteTask(taskId)
      onClose?.()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const canReview =
    task?.status === 'review' && (task.assignee_type === 'agent' || user !== null)

  if (error && !task) {
    return <p className="text-red-700">{error}</p>
  }
  if (!task) {
    return <p className="text-slate-500">Loading…</p>
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

        <EditableTitle value={task.title} onSave={onSaveTitle} busy={busy} />

        <DescriptionEditor
          value={task.description ?? ''}
          onSave={onSaveDescription}
          busy={busy}
        />

        {task.context_md && (
          <details className="rounded border border-slate-200 dark:border-slate-800 p-3">
            <summary className="cursor-pointer text-sm text-slate-500">
              Agent context (md)
            </summary>
            <pre className="mt-2 text-xs whitespace-pre-wrap font-mono">
              {task.context_md}
            </pre>
          </details>
        )}

        {task.agent_notes && (
          <div className="rounded border border-amber-300 bg-amber-50 p-3 text-sm">
            <p className="font-semibold text-amber-900 mb-1">Agent note</p>
            <p className="text-amber-900 whitespace-pre-wrap">{task.agent_notes}</p>
          </div>
        )}

        {canReview && (
          <div className="rounded border border-slate-200 dark:border-slate-800 p-3 space-y-2">
            <p className="text-sm font-semibold">Review</p>
            <textarea
              value={reviewComment}
              onChange={(e) => setReviewComment(e.target.value)}
              placeholder="Optional feedback for the agent"
              rows={2}
              className="w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
            />
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => onReview('approve')}
                disabled={busy}
                className="px-3 py-1.5 rounded bg-green-600 hover:bg-green-700 disabled:opacity-50 text-white text-sm"
              >
                Approve
              </button>
              <button
                type="button"
                onClick={() => onReview('reject')}
                disabled={busy}
                className="px-3 py-1.5 rounded bg-amber-600 hover:bg-amber-700 disabled:opacity-50 text-white text-sm"
              >
                Reject
              </button>
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

        <ChecklistsList
          taskId={task.id}
          initialLists={checklists}
          initialItems={checklistItems}
        />

        <div>
          <h2 className="text-sm font-semibold mb-2 text-slate-500">
            Comments ({comments.length})
          </h2>
          <CommentsList comments={comments} />
          <form onSubmit={onPostComment} className="mt-2 flex gap-2">
            <input
              type="text"
              value={composer}
              onChange={(e) => setComposer(e.target.value)}
              placeholder="Add a comment (supports @user:<id> / @agent:<id>)"
              className="flex-1 px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
            />
            <button
              type="submit"
              disabled={busy || !composer.trim()}
              className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
            >
              Post
            </button>
          </form>
        </div>

        <ActivityLog items={activity} />

        {onClose && (
          <div className="pt-2 border-t border-slate-200 dark:border-slate-800">
            <button
              type="button"
              onClick={onDelete}
              disabled={busy}
              className="px-3 py-1.5 rounded border border-red-300 text-red-700 hover:bg-red-50 disabled:opacity-50 text-sm"
            >
              Delete task
            </button>
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
          onChanged={(t) => setTask(t)}
          onError={(msg) => setError(msg)}
        />
        {task.awaiting && task.awaiting !== 'none' && (
          <div className="rounded border border-blue-300 bg-blue-50 p-3 text-blue-900 text-sm">
            Awaiting <span className="font-mono">{task.awaiting}</span> action
          </div>
        )}
        {task.due_at && <SidebarField label="Due" value={task.due_at} />}
        <ColorPicker value={task.color} onSave={onSaveColor} busy={busy} />
        <TagsList taskId={taskId} initial={tags} />
        <BlockedByList taskId={taskId} projectId={task.project_id || ''} />
        <div className="rounded border border-slate-200 dark:border-slate-800 p-3">
          <p className="text-xs text-slate-500 mb-1">Time tracking</p>
          <p className="font-mono mb-2">{(task.time_spent_s / 60).toFixed(1)} min</p>
          <button
            type="button"
            onClick={() => StartTimer(task)}
            className="w-full px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-xs"
          >
            Start timer
          </button>
        </div>
      </aside>
    </section>
  )
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function EditableTitle({
  value,
  onSave,
  busy,
}: {
  value: string
  onSave: (s: string) => Promise<void>
  busy: boolean
}): JSX.Element {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)

  useEffect(() => {
    setDraft(value)
  }, [value])

  if (!editing) {
    return (
      <h1
        className="text-2xl font-semibold cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-900 rounded px-2 py-1"
        onClick={() => setEditing(true)}
        title="Click to edit"
      >
        {value}
      </h1>
    )
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (draft.trim() && draft !== value) {
          void onSave(draft)
        }
        setEditing(false)
      }}
      className="flex gap-2"
    >
      <input
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            setDraft(value)
            setEditing(false)
          }
        }}
        className="text-2xl font-semibold bg-transparent border-b border-orenda-500 focus:outline-none flex-1"
      />
      <button
        type="submit"
        disabled={busy || !draft.trim() || draft === value}
        className="px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
      >
        Save
      </button>
      <button
        type="button"
        onClick={() => {
          setDraft(value)
          setEditing(false)
        }}
        className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 text-xs"
      >
        Cancel
      </button>
    </form>
  )
}

function DescriptionEditor({
  value,
  onSave,
  busy,
}: {
  value: string
  onSave: (s: string) => Promise<void>
  busy: boolean
}): JSX.Element {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value)

  useEffect(() => {
    setDraft(value)
  }, [value])

  if (!editing) {
    if (!value) {
      return (
        <button
          type="button"
          onClick={() => setEditing(true)}
          className="block text-slate-400 text-sm italic hover:text-slate-700"
        >
          + Add description
        </button>
      )
    }
    return (
      <button
        type="button"
        onClick={() => setEditing(true)}
        className="block text-left text-slate-700 dark:text-slate-300 whitespace-pre-wrap hover:bg-slate-50 dark:hover:bg-slate-900 rounded px-2 py-1 w-full"
      >
        {value}
      </button>
    )
  }
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        if (draft !== value) {
          void onSave(draft)
        }
        setEditing(false)
      }}
      className="space-y-2"
    >
      <textarea
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
            e.preventDefault()
            if (draft !== value) void onSave(draft)
            setEditing(false)
          } else if (e.key === 'Escape') {
            setDraft(value)
            setEditing(false)
          }
        }}
        rows={4}
        placeholder="What's this task about? Markdown is supported."
        className="w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm font-mono"
      />
      <div className="flex gap-2">
        <button
          type="submit"
          disabled={busy || draft === value}
          className="px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
        >
          Save
        </button>
        <button
          type="button"
          onClick={() => {
            setDraft(value)
            setEditing(false)
          }}
          className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 text-xs"
        >
          Cancel
        </button>
        <span className="text-xs text-slate-400 self-center">
          Ctrl+Enter to save · Esc to cancel
        </span>
      </div>
    </form>
  )
}

function SidebarField({ label, value }: { label: string; value: string }): JSX.Element {
  return (
    <div className="rounded border border-slate-200 dark:border-slate-800 p-3">
      <p className="text-xs text-slate-500">{label}</p>
      <p className="font-mono">{value}</p>
    </div>
  )
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
  value: string
  onSave: (color: string) => Promise<void>
  busy: boolean
}): JSX.Element {
  const [draft, setDraft] = useState(value)

  // Re-sync draft when the parent task reloads (WS event, save
  // round-trip, etc.). Without this, an external colour change
  // wouldn't show up until the user unfocused the picker.
  useEffect(() => {
    setDraft(value)
  }, [value])

  function commit(next: string): void {
    if (next === value) return
    setDraft(next)
    void onSave(next)
  }

  return (
    <div className="rounded border border-slate-200 dark:border-slate-800 p-3 space-y-2">
      <p className="text-xs text-slate-500">Colour label</p>
      <div className="flex items-center gap-2">
        <input
          type="color"
          value={draft || '#3b82f6'}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => commit(draft)}
          disabled={busy}
          className="h-7 w-9 rounded border border-slate-300 dark:border-slate-700 bg-transparent cursor-pointer"
          title="Pick a colour"
        />
        <span className="font-mono text-xs text-slate-600 dark:text-slate-300 flex-1">
          {draft || 'none'}
        </span>
        {draft && (
          <button
            type="button"
            disabled={busy}
            onClick={() => commit('')}
            className="px-2 py-0.5 rounded border border-slate-300 dark:border-slate-700 text-xs disabled:opacity-50"
            title="Remove the colour label"
          >
            clear
          </button>
        )}
      </div>
    </div>
  )
}

function ActivityLog({ items }: { items: TaskActivity[] }): JSX.Element {
  // Action verbs we render as a human label.
  const verb: Record<string, string> = {
    'task.created': 'created the task',
    'task.moved': 'moved the task',
    'task.status_changed': 'changed the status',
    'task.title_changed': 'changed the title',
    'task.priority_changed': 'changed the priority',
    'task.assigned': 'assigned the task',
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
  }
  // Most recent first.
  const sorted = useMemo(
    () => [...items].sort((a, b) => b.created_at.localeCompare(a.created_at)),
    [items],
  )
  return (
    <section>
      <h2 className="text-sm font-semibold mb-2 text-slate-500">
        Activity ({items.length})
      </h2>
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
                <span className="text-xs text-slate-400 truncate">
                  · {a.payload}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
