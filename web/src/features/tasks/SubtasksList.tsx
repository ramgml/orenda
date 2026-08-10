import { FormEvent, useEffect, useState } from 'react'

import { api, type Subtask } from '@/shared/api/client'

/**
 * Subtasks block on the task view. Each subtask is a checkbox with
 * an inline title; new ones are added with Enter.
 */
export function SubtasksList({ taskId, initial }: { taskId: string; initial: Subtask[] }) {
  const [subs, setSubs] = useState<Subtask[]>(initial)
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setSubs(initial)
  }, [initial])

  const done = subs.filter((s) => s.done).length

  async function reload(): Promise<void> {
    const r = await api.listSubtasks(taskId)
    setSubs(r.subtasks ?? [])
  }

  async function onAdd(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!title.trim()) return
    setBusy(true)
    try {
      const s = await api.addSubtask(taskId, { title: title.trim() })
      setSubs((cur) => [...cur, s])
      setTitle('')
    } finally {
      setBusy(false)
    }
  }

  async function onToggle(s: Subtask): Promise<void> {
    setSubs((cur) => cur.map((x) => (x.id === s.id ? { ...x, done: !x.done } : x)))
    try {
      await api.updateSubtask(s.id, { done: !s.done })
    } catch {
      // Roll back on failure.
      setSubs((cur) => cur.map((x) => (x.id === s.id ? { ...x, done: s.done } : x)))
    }
  }

  async function onDelete(s: Subtask): Promise<void> {
    if (!window.confirm(`Delete subtask "${s.title}"?`)) return
    setSubs((cur) => cur.filter((x) => x.id !== s.id))
    try {
      await api.deleteSubtask(s.id)
    } catch {
      reload()
    }
  }

  return (
    <section>
      <h2 className="text-sm font-semibold mb-2 text-slate-500 flex items-center gap-2">
        Subtasks
        <span className="text-xs text-slate-400">
          {done}/{subs.length}
        </span>
      </h2>
      {subs.length === 0 ? (
        <p className="text-xs text-slate-400 italic mb-2">No subtasks yet.</p>
      ) : (
        <ul className="space-y-1">
          {subs.map((s) => (
            <li key={s.id} className="flex items-center gap-2 group">
              <input
                type="checkbox"
                checked={s.done}
                onChange={() => onToggle(s)}
                className="shrink-0"
              />
              <span className={`flex-1 text-sm ${s.done ? 'line-through text-slate-400' : ''}`}>
                {s.title}
              </span>
              <button
                type="button"
                onClick={() => onDelete(s)}
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
          placeholder="+ Add subtask"
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
