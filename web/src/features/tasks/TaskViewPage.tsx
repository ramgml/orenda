import { FormEvent, useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'

import { useAuth } from '@/features/auth/AuthContext'
import {
  api,
  type Checklist,
  type ChecklistItem,
  type Comment as TaskComment,
  type Subtask,
  type Task,
  type TaskActivity,
  type TaskAttachment,
} from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'
import { StartTimer } from '@/features/tasks/TimerWidget'

import { CommentsList } from './CommentsList'
import { SubtasksList } from './SubtasksList'
import { AttachmentsList } from './AttachmentsList'
import { ChecklistsList } from './ChecklistsList'

/**
 * /tasks/:id — full task view.
 *
 * Layout (loosely after Weeek / Trello):
 *   ┌─ main column ────────────────────┬─ sidebar ──┐
 *   │ Title (editable)                  │ Status     │
 *   │ Description (editable, markdown)  │ Priority   │
 *   │ Subtasks                          │ Assignee   │
 *   │ Attachments                       │ Due        │
 *   │ Comments                          │ Time track │
 *   │ Activity                          │ Review btn │
 *   └──────────────────────────────────┴────────────┘
 */
export function TaskViewPage(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const { user } = useAuth()
  const [task, setTask] = useState<Task | null>(null)
  const [subs, setSubs] = useState<Subtask[]>([])
  const [attachments, setAttachments] = useState<TaskAttachment[]>([])
  const [checklists, setChecklists] = useState<Checklist[]>([])
  const [checklistItems, setChecklistItems] = useState<Record<string, ChecklistItem[]>>({})
  const [activity, setActivity] = useState<TaskActivity[]>([])
  const [comments, setComments] = useState<TaskComment[]>([])
  const [error, setError] = useState<string | null>(null)
  const [composer, setComposer] = useState('')
  const [busy, setBusy] = useState(false)
  const [reviewComment, setReviewComment] = useState('')

  async function load(): Promise<void> {
    if (!id) return
    try {
      const [t, subsR, attR, actR, comments, clR] = await Promise.all([
        api.getTask(id),
        api.listSubtasks(id),
        api.listTaskAttachments(id),
        api.listTaskActivity(id),
        api.listTaskComments(id),
        api.listChecklists(id),
      ])
      setTask(t)
      setSubs(subsR.subtasks ?? [])
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
            const r = await api.listChecklistItems(id, l.id)
            return [l.id, r.items ?? []] as const
          } catch {
            return [l.id, [] as ChecklistItem[]] as const
          }
        }),
      )
      const next: Record<string, ChecklistItem[]> = {}
      for (const [id, its] of pairs) next[id] = its
      setChecklistItems(next)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  // Live updates on any task event.
  useWebSocketTopic('tasks', () => {
    load()
  })

  async function onPostComment(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!id || !composer.trim()) return
    setBusy(true)
    try {
      await api.createTaskComment(id, composer.trim())
      setComposer('')
      const list = await api.listTaskComments(id)
      setComments(list.comments ?? [])
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  async function onReview(decision: 'approve' | 'reject'): Promise<void> {
    if (!id) return
    setBusy(true)
    try {
      await api.reviewTask(id, decision, reviewComment)
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
    if (!id) return
    setBusy(true)
    try {
      const t = await api.patchTask(id, { description })
      setTask(t)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  // Title patch.
  const onSaveTitle = async (title: string): Promise<void> => {
    if (!id || !title.trim()) return
    setBusy(true)
    try {
      const t = await api.patchTask(id, { title: title.trim() })
      setTask(t)
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
        <EditableTitle
          value={task.title}
          onSave={onSaveTitle}
          busy={busy}
        />

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

        <SubtasksList taskId={task.id} initial={subs} />

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
      </div>

      <aside className="space-y-3 text-sm">
        <SidebarField label="Status" value={task.status} />
        <SidebarField label="Priority" value={task.priority} />
        <SidebarField
          label="Assignee"
          value={task.assignee_type ? `${task.assignee_type}:${task.assignee_id}` : '—'}
        />
        {task.awaiting && task.awaiting !== 'none' && (
          <div className="rounded border border-blue-300 bg-blue-50 p-3 text-blue-900 text-sm">
            Awaiting <span className="font-mono">{task.awaiting}</span> action
          </div>
        )}
        {task.due_at && <SidebarField label="Due" value={task.due_at} />}
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
    'task.subtask_added': 'added a subtask',
    'task.subtask_done': 'completed a subtask',
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
