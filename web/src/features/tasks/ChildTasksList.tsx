import { FormEvent, useEffect, useState } from 'react'

import { api, type ChildTaskProgress, type Task } from '@/shared/api/client'
import { TaskLink } from './TaskModal'

/**
 * Status badge colour for a child task. Mirrors the kanban palette so
 * users instantly recognise whether the child is in_progress / done
 * etc. without reading the label.
 */
function statusBadgeClass(s: string): string {
  switch (s) {
    case 'done':
      return 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
    case 'in_progress':
      return 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200'
    case 'review':
      return 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200'
    case 'backlog':
      return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200'
    default:
      return 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200'
  }
}

/**
 * ChildTasksList renders the direct children of a task as small
 * cards (Phase 14: subtasks became first-class tasks via
 * parent_task_id). Each row shows title + status badge + assignee
 * chip; the whole row is a link to the child's task page.
 *
 * The block also exposes a tiny form to add a new child task — the
 * caller passes `projectId` because creating a task requires it
 * (POST /api/v1/projects/:pid/tasks). Status defaults to `todo`.
 */
export function ChildTasksList({
  taskId,
  projectId,
  initialTasks,
  initialProgress,
}: {
  taskId: string
  projectId: string
  initialTasks: Task[]
  initialProgress: ChildTaskProgress
}) {
  const [tasks, setTasks] = useState<Task[]>(initialTasks)
  const [progress, setProgress] = useState<ChildTaskProgress>(initialProgress)
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setTasks(initialTasks)
    setProgress(initialProgress)
  }, [initialTasks, initialProgress])

  async function reload(): Promise<void> {
    const r = await api.listChildTasks(taskId)
    setTasks(r.tasks ?? [])
    setProgress(r.progress ?? { total: 0, done: 0 })
  }

  async function onAdd(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!title.trim()) return
    setBusy(true)
    try {
      // Phase 16: projectId may be empty (the parent is an Inbox
      // task). Use the dedicated inbox endpoint so the server stores
      // project_id IS NULL instead of erroring on an empty target.
      const t = projectId
        ? await api.createChildTask(projectId, {
            title: title.trim(),
            parent_task_id: taskId,
          })
        : await api.createInboxTask({
            title: title.trim(),
            parent_task_id: taskId,
          })
      setTasks((cur) => [...cur, t])
      setProgress((p) => ({ total: p.total + 1, done: t.status === 'done' ? p.done + 1 : p.done }))
      setTitle('')
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(t: Task): Promise<void> {
    if (!window.confirm(`Delete child task "${t.title}"?`)) return
    setTasks((cur) => cur.filter((x) => x.id !== t.id))
    setProgress((p) => ({
      total: Math.max(0, p.total - 1),
      done: Math.max(0, p.done - (t.status === 'done' ? 1 : 0)),
    }))
    try {
      await api.deleteChildTask(t.id)
    } catch {
      reload()
    }
  }

  const pct = progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0

  return (
    <section>
      <div className="flex items-center justify-between mb-2">
        <h2 className="text-sm font-semibold text-slate-500 flex items-center gap-2">
          Child tasks
          <span className="text-xs text-slate-400">
            {progress.done}/{progress.total}
          </span>
        </h2>
      </div>

      {progress.total > 0 && (
        <div className="mb-3">
          <div className="h-1.5 rounded bg-slate-100 dark:bg-slate-800 overflow-hidden">
            <div
              className="h-full bg-green-500 transition-all"
              style={{ width: `${pct}%` }}
              role="progressbar"
              aria-valuenow={progress.done}
              aria-valuemin={0}
              aria-valuemax={progress.total}
            />
          </div>
          <p className="text-xs text-slate-400 mt-1">{pct}% complete</p>
        </div>
      )}

      {tasks.length === 0 ? (
        <p className="text-xs text-slate-400 italic mb-2">No child tasks yet.</p>
      ) : (
        <ul className="space-y-1">
          {tasks.map((t) => (
            <li
              key={t.id}
              className="flex items-center gap-2 group rounded border border-transparent hover:border-slate-200 dark:hover:border-slate-700 px-2 py-1"
            >
              <TaskLink
                taskId={t.id}
                className="flex-1 text-sm hover:underline truncate"
              >
                {t.title}
              </TaskLink>
              <span
                className={`text-xs px-1.5 py-0.5 rounded ${statusBadgeClass(t.status)}`}
              >
                {t.status}
              </span>
              {t.assignee_type && (
                <span className="text-xs text-slate-400 font-mono">
                  {t.assignee_type}:{(t.assignee_id ?? '').slice(0, 8)}
                </span>
              )}
              <button
                type="button"
                onClick={() => onDelete(t)}
                title="Delete"
                className="opacity-0 group-hover:opacity-100 text-xs text-slate-400 hover:text-red-500"
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}

      <form onSubmit={onAdd} className="mt-2 flex gap-2">
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="+ Add child task"
          className="flex-1 px-2 py-1 text-sm rounded border border-slate-200 dark:border-slate-700 bg-transparent"
        />
        <button
          type="submit"
          disabled={busy || !title.trim()}
          className="px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
        >
          Add
        </button>
      </form>
    </section>
  )
}
