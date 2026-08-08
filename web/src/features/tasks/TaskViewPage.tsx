import { FormEvent, useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'

import { useAuth } from '@/features/auth/AuthContext'
import { api, type Comment, type Task } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'

import { CommentsList } from './CommentsList'

/**
 * /tasks/:id — full task view with comments, attachments and
 * review actions.
 *
 * Phase 3 ships the minimum: a comment composer, the comments list,
 * and approve/reject buttons when the task is in `review` status. The
 * full attachments and activity panels will be wired in Phase 5+.
 */
export function TaskViewPage(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const { user } = useAuth()
  const [task, setTask] = useState<Task | null>(null)
  const [comments, setComments] = useState<Comment[]>([])
  const [error, setError] = useState<string | null>(null)
  const [composer, setComposer] = useState('')
  const [busy, setBusy] = useState(false)
  const [reviewComment, setReviewComment] = useState('')

  async function load(): Promise<void> {
    if (!id) return
    try {
      const t = await api.getTask(id)
      const list = await api.listTaskComments(id)
      setTask(t)
      setComments(list.comments ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  // Refresh on every WS task event (live updates from other tabs / agents).
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
      // Comments come back via WS or the next load.
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

  if (error) {
    return <p className="text-red-700">{error}</p>
  }
  if (!task) {
    return <p className="text-slate-500">Loading…</p>
  }

  const canReview =
    task.status === 'review' &&
    (task.assignee_type === 'agent' || user !== null)

  return (
    <section className="grid gap-6 md:grid-cols-3">
      <div className="md:col-span-2 space-y-4">
        <div>
          <h1 className="text-2xl font-semibold">{task.title}</h1>
          <p className="text-xs text-slate-500 font-mono mt-1">{task.id}</p>
        </div>

        {task.description && (
          <p className="text-slate-700 dark:text-slate-300 whitespace-pre-wrap">
            {task.description}
          </p>
        )}

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
      </div>

      <aside className="space-y-3 text-sm">
        <div className="rounded border border-slate-200 dark:border-slate-800 p-3">
          <p className="text-xs text-slate-500">Status</p>
          <p className="font-mono">{task.status}</p>
        </div>
        <div className="rounded border border-slate-200 dark:border-slate-800 p-3">
          <p className="text-xs text-slate-500">Priority</p>
          <p className="font-mono">{task.priority}</p>
        </div>
        <div className="rounded border border-slate-200 dark:border-slate-800 p-3">
          <p className="text-xs text-slate-500">Assignee</p>
          <p className="font-mono">
            {task.assignee_type ? `${task.assignee_type}:${task.assignee_id}` : '—'}
          </p>
        </div>
        {task.awaiting && task.awaiting !== 'none' && (
          <div className="rounded border border-blue-300 bg-blue-50 p-3 text-blue-900 text-sm">
            Awaiting <span className="font-mono">{task.awaiting}</span> action
          </div>
        )}
        {task.due_at && (
          <div className="rounded border border-slate-200 dark:border-slate-800 p-3">
            <p className="text-xs text-slate-500">Due</p>
            <p className="font-mono">{task.due_at}</p>
          </div>
        )}
      </aside>
    </section>
  )
}