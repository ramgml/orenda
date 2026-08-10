import { ChangeEvent, useEffect, useRef, useState } from 'react'

import { api, type TaskAttachment } from '@/shared/api/client'

/**
 * Attachments block on the task view. File picker → multipart upload
 * → row with a download link + delete button.
 */
export function AttachmentsList({
  taskId,
  initial,
}: {
  taskId: string
  initial: TaskAttachment[]
}) {
  const [items, setItems] = useState<TaskAttachment[]>(initial)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    setItems(initial)
  }, [initial])

  async function reload(): Promise<void> {
    const r = await api.listTaskAttachments(taskId)
    setItems(r.attachments ?? [])
  }

  async function onPick(e: ChangeEvent<HTMLInputElement>): Promise<void> {
    const f = e.target.files?.[0]
    if (!f) return
    setBusy(true)
    setErr(null)
    try {
      const a = await api.uploadTaskAttachment(taskId, f)
      setItems((cur) => [...cur, a])
    } catch (ex) {
      setErr(ex instanceof Error ? ex.message : String(ex))
    } finally {
      setBusy(false)
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  async function onDelete(a: TaskAttachment): Promise<void> {
    if (!window.confirm(`Delete "${a.filename}"?`)) return
    setItems((cur) => cur.filter((x) => x.id !== a.id))
    try {
      await api.deleteTaskAttachment(a.id)
    } catch {
      reload()
    }
  }

  function formatSize(n: number): string {
    if (n < 1024) return `${n} B`
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
    return `${(n / 1024 / 1024).toFixed(1)} MB`
  }

  return (
    <section>
      <h2 className="text-sm font-semibold mb-2 text-slate-500 flex items-center justify-between">
        <span>Attachments ({items.length})</span>
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          disabled={busy}
          className="px-2 py-0.5 rounded bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-xs"
        >
          + Upload
        </button>
      </h2>
      <input
        ref={inputRef}
        type="file"
        className="hidden"
        onChange={onPick}
        disabled={busy}
      />
      {err && <p className="text-xs text-red-600 mb-2">{err}</p>}
      {items.length === 0 ? (
        <p className="text-xs text-slate-400 italic">No attachments yet.</p>
      ) : (
        <ul className="space-y-1">
          {items.map((a) => (
            <li
              key={a.id}
              className="flex items-center gap-2 group rounded px-2 py-1 hover:bg-slate-50 dark:hover:bg-slate-900"
            >
              <span aria-hidden className="text-slate-400">📎</span>
              <a
                href={api.taskAttachmentDownloadUrl(a.id)}
                className="flex-1 text-sm truncate hover:underline"
                title={a.mime}
              >
                {a.filename}
              </a>
              <span className="text-[10px] text-slate-400">{formatSize(a.size)}</span>
              <button
                type="button"
                onClick={() => onDelete(a)}
                title="Delete"
                className="opacity-0 group-hover:opacity-100 text-xs text-slate-400 hover:text-red-500"
              >
                ×
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
