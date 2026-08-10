import { FormEvent, useEffect, useState } from 'react'

import { api, type Checklist, type ChecklistItem } from '@/shared/api/client'

/**
 * Checklists block on the task view. Multiple checklists; each
 * holds a set of checkable items. Mirrors the Trello "checklist"
 * card (with an item-by-item checkbox + add/delete).
 */
export function ChecklistsList({
  taskId,
  initialLists,
  initialItems,
}: {
  taskId: string
  initialLists: Checklist[]
  initialItems: Record<string, ChecklistItem[]>
}) {
  const [lists, setLists] = useState<Checklist[]>(initialLists)
  const [itemsByList, setItems] = useState<Record<string, ChecklistItem[]>>(initialItems)
  const [newListTitle, setNewListTitle] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setLists(initialLists)
    setItems(initialItems)
  }, [initialLists, initialItems])

  async function addList(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!newListTitle.trim()) return
    setBusy(true)
    try {
      const l = await api.addChecklist(taskId, { title: newListTitle.trim() })
      setLists((cur) => [...cur, l])
      setItems((cur) => ({ ...cur, [l.id]: [] }))
      setNewListTitle('')
    } finally {
      setBusy(false)
    }
  }

  async function deleteList(l: Checklist): Promise<void> {
    if (!window.confirm(`Delete checklist "${l.title}"?`)) return
    setLists((cur) => cur.filter((x) => x.id !== l.id))
    setItems((cur) => {
      const next = { ...cur }
      delete next[l.id]
      return next
    })
    try {
      await api.deleteChecklist(taskId, l.id)
    } catch {
      // ignore
    }
  }

  return (
    <section className="space-y-3">
      {lists.length === 0 && (
        <p className="text-xs text-slate-400 italic">No checklists.</p>
      )}
      {lists.map((l) => (
        <ChecklistBlock
          key={l.id}
          taskId={taskId}
          list={l}
          items={itemsByList[l.id] ?? []}
          onChange={(next) =>
            setItems((cur) => ({ ...cur, [l.id]: next }))
          }
          onDelete={() => deleteList(l)}
        />
      ))}
      <form onSubmit={addList} className="flex gap-2">
        <input
          value={newListTitle}
          onChange={(e) => setNewListTitle(e.target.value)}
          placeholder="+ Add checklist"
          className="flex-1 px-2 py-1 text-sm rounded border border-slate-200 dark:border-slate-700 bg-transparent"
        />
        <button
          type="submit"
          disabled={busy || !newListTitle.trim()}
          className="px-2 py-1 rounded bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-xs"
        >
          Add
        </button>
      </form>
    </section>
  )
}

function ChecklistBlock({
  taskId,
  list,
  items,
  onChange,
  onDelete,
}: {
  taskId: string
  list: Checklist
  items: ChecklistItem[]
  onChange: (next: ChecklistItem[]) => void
  onDelete: () => void
}) {
  const [title, setTitle] = useState('')
  const done = items.filter((i) => i.done).length

  async function addItem(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!title.trim()) return
    const it = await api.addChecklistItem(taskId, list.id, { title: title.trim() })
    onChange([...items, it])
    setTitle('')
  }

  async function toggle(it: ChecklistItem): Promise<void> {
    onChange(items.map((x) => (x.id === it.id ? { ...x, done: !x.done } : x)))
    try {
      await api.updateChecklistItem(taskId, list.id, it.id, { done: !it.done })
    } catch {
      onChange(items.map((x) => (x.id === it.id ? { ...x, done: it.done } : x)))
    }
  }

  async function del(it: ChecklistItem): Promise<void> {
    onChange(items.filter((x) => x.id !== it.id))
    try {
      await api.deleteChecklistItem(taskId, list.id, it.id)
    } catch {
      onChange(items)
    }
  }

  return (
    <div className="rounded border border-slate-200 dark:border-slate-800 p-2">
      <div className="flex items-center justify-between mb-1">
        <h3 className="text-sm font-medium">
          {list.title} <span className="text-xs text-slate-400">({done}/{items.length})</span>
        </h3>
        <button
          type="button"
          onClick={onDelete}
          className="text-xs text-slate-400 hover:text-red-500"
          title="Delete checklist"
        >
          ×
        </button>
      </div>
      {items.length > 0 && (
        <ul className="space-y-1">
          {items.map((it) => (
            <li key={it.id} className="flex items-center gap-2 group">
              <input
                type="checkbox"
                checked={it.done}
                onChange={() => toggle(it)}
              />
              <span className={`flex-1 text-sm ${it.done ? 'line-through text-slate-400' : ''}`}>
                {it.title}
              </span>
              <button
                type="button"
                onClick={() => del(it)}
                className="opacity-0 group-hover:opacity-100 text-xs text-slate-400 hover:text-red-500"
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={addItem} className="mt-2 flex gap-2">
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="+ Add item"
          className="flex-1 px-2 py-1 text-sm rounded border border-slate-200 dark:border-slate-700 bg-transparent"
        />
        <button
          type="submit"
          disabled={!title.trim()}
          className="px-2 py-1 rounded bg-slate-100 dark:bg-slate-800 text-xs"
        >
          Add
        </button>
      </form>
    </div>
  )
}
