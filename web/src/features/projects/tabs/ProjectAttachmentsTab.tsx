import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'

import { api, type TaskAttachment } from '@/shared/api/client'

/**
 * /projects/:id/attachments — files attached directly to the
 * project (not to any of its tasks).
 *
 * Upload is a drag-and-drop area plus a click-to-pick fallback. The
 * download URLs reuse the global /api/v1/attachments/{id}/download
 * route — the server resolves the target_type from the row, so the
 * same handler serves both task and project attachments.
 */
export function ProjectAttachmentsTab(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const [items, setItems] = useState<TaskAttachment[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  async function reload(): Promise<void> {
    if (!id) return
    try {
      const r = await api.listProjectAttachments(id)
      setItems(r.attachments)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    void reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  async function uploadFiles(files: FileList | File[]): Promise<void> {
    if (!id) return
    setBusy(true)
    setError(null)
    try {
      for (const f of Array.from(files)) {
        await api.uploadProjectAttachment(id, f)
      }
      await reload()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (items === null && !error) {
    return <p className="text-slate-500">Loading attachments…</p>
  }

  return (
    <div className="space-y-3">
      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDragOver(true)
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault()
          setDragOver(false)
          if (e.dataTransfer.files.length) {
            void uploadFiles(e.dataTransfer.files)
          }
        }}
        onClick={() => fileInputRef.current?.click()}
        className={`rounded border-2 border-dashed p-6 text-center text-sm cursor-pointer transition ${
          dragOver
            ? 'border-orenda-500 bg-orenda-50 dark:bg-orenda-900/20'
            : 'border-slate-300 dark:border-slate-700 hover:border-orenda-400'
        }`}
      >
        {busy ? 'Uploading…' : 'Drop files here or click to upload'}
        <input
          ref={fileInputRef}
          type="file"
          multiple
          className="hidden"
          onChange={(e) => {
            if (e.target.files?.length) void uploadFiles(e.target.files)
            e.target.value = ''
          }}
        />
      </div>

      {items && items.length === 0 ? (
        <p className="text-sm text-slate-500">No attachments yet.</p>
      ) : (
        <ul className="divide-y divide-slate-200 dark:divide-slate-800 rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950">
          {(items ?? []).map((a) => (
            <li key={a.id} className="px-3 py-2 flex items-center gap-3 text-sm">
              <span className="font-medium truncate flex-1">{a.filename}</span>
              <span className="text-xs text-slate-400 font-mono">{a.mime}</span>
              <span className="text-xs text-slate-400">{formatBytes(a.size)}</span>
              <a
                href={api.taskAttachmentDownloadUrl(a.id)}
                download
                target="_blank"
                rel="noopener"
                className="text-xs px-2 py-1 rounded border border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800"
              >
                Download
              </a>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB`
}
