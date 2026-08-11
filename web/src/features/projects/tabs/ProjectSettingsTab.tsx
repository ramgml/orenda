import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'

import { api, type Project } from '@/shared/api/client'

/**
 * /projects/:id/settings — color, description, archive, delete.
 *
 * The project name is intentionally NOT editable here — it is the
 * inline-edited `<h1>` on the project header (Phase 11 UX). That
 * keeps the title discoverable on every tab without forcing the user
 * to remember to come back here.
 *
 * Phase 16: every project here is a real user project. The old system
 * Inbox project is gone (unfiled tasks live at /inbox), so archive
 * and delete are uniform — no special-casing for a "system" project.
 */
export function ProjectSettingsTab(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [project, setProject] = useState<Project | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [color, setColor] = useState('#3b82f6')
  const [description, setDescription] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)

  useEffect(() => {
    if (!id) return
    let cancelled = false
    api
      .getProject(id)
      .then((p) => {
        if (cancelled) return
        setProject(p)
        setColor(p.color || '#3b82f6')
        setDescription(p.description || '')
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [id])

  async function saveBasics(): Promise<void> {
    if (!project) return
    setBusy(true)
    setError(null)
    try {
      const updated = await api.updateProject(project.id, {
        color,
        description,
      })
      setProject(updated)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function toggleArchive(): Promise<void> {
    if (!project) return
    setBusy(true)
    setError(null)
    try {
      const updated = await api.updateProject(project.id, {
        archived: !project.archived,
      })
      setProject(updated)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function deleteProject(): Promise<void> {
    if (!project) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteProject(project.id)
      navigate('/projects', { replace: true })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  if (!project && !error) return <p className="text-slate-500">Loading…</p>
  if (!project) return <p className="text-red-700">{error}</p>

  return (
    <div className="space-y-6 max-w-2xl">
      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      <section className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-4 space-y-3">
        <h2 className="text-base font-semibold">Appearance</h2>
        <div className="flex items-center gap-3">
          <input
            type="color"
            value={color}
            onChange={(e) => setColor(e.target.value)}
            className="h-9 w-12 rounded border border-slate-300 dark:border-slate-700 cursor-pointer"
            aria-label="Project color"
          />
          <input
            type="text"
            value={color}
            onChange={(e) => setColor(e.target.value)}
            className="flex-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent font-mono text-sm"
            placeholder="#3b82f6"
          />
        </div>
      </section>

      <section className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-4 space-y-3">
        <h2 className="text-base font-semibold">Description</h2>
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={4}
          className="w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
          placeholder="What is this project about?"
        />
        <div className="flex justify-end">
          <button
            type="button"
            onClick={saveBasics}
            disabled={busy}
            className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm disabled:opacity-50"
          >
            {busy ? 'Saving…' : 'Save changes'}
          </button>
        </div>
      </section>

      <section className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-4 space-y-3">
        <h2 className="text-base font-semibold">Archive</h2>
        <p className="text-sm text-slate-500">
          Archived projects stay in the list but are hidden from the Kanban view.
          You can restore them later.
        </p>
        <button
          type="button"
          onClick={toggleArchive}
          disabled={busy}
          className="px-3 py-1.5 rounded border border-slate-300 dark:border-slate-700 text-sm hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-50"
        >
          {project.archived ? 'Unarchive' : 'Archive'}
        </button>
      </section>

      <section className="rounded border border-red-300 bg-red-50/40 dark:bg-red-900/10 dark:border-red-800 p-4 space-y-3">
        <h2 className="text-base font-semibold text-red-800 dark:text-red-300">
          Danger zone
        </h2>
        <p className="text-sm text-slate-600 dark:text-slate-300">
          Deleting a project removes its tasks, columns, comments, and attachments
          permanently. This cannot be undone.
        </p>
        {confirmDelete ? (
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={deleteProject}
              disabled={busy}
              className="px-3 py-1.5 rounded bg-red-600 hover:bg-red-700 text-white text-sm disabled:opacity-50"
            >
              {busy ? 'Deleting…' : 'Yes, delete project'}
            </button>
            <button
              type="button"
              onClick={() => setConfirmDelete(false)}
              disabled={busy}
              className="px-3 py-1.5 rounded border border-slate-300 dark:border-slate-700 text-sm"
            >
              Cancel
            </button>
          </div>
        ) : (
          <button
            type="button"
            onClick={() => setConfirmDelete(true)}
            className="px-3 py-1.5 rounded border border-red-300 text-red-700 hover:bg-red-50 text-sm"
          >
            Delete project…
          </button>
        )}
      </section>
    </div>
  )
}
