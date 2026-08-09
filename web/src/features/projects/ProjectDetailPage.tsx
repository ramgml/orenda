import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'

import { api, type Project, type ProjectBoard } from '@/shared/api/client'

import { KanbanBoard } from './KanbanBoard'

/**
 * /projects/:id — single project kanban board view.
 *
 * Phase 2 replaces the Phase 1 read-only stub with @dnd-kit/core drag-and-drop.
 * Phase 6+ adds an archive toggle in the header.
 */
export function ProjectDetailPage(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const [board, setBoard] = useState<ProjectBoard | null>(null)
  const [project, setProject] = useState<Project | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!id) return
    let cancelled = false
    Promise.all([api.getBoard(id), api.listProjects()])
      .then(([b, list]) => {
        if (cancelled) return
        setBoard(b)
        setProject(list.find((p) => p.id === id) ?? null)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [id])

  async function toggleArchive(): Promise<void> {
    if (!project) return
    setBusy(true)
    setError(null)
    try {
      const updated = await api.updateProject(project.id, { archived: !project.archived })
      setProject(updated)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (error) return <p className="text-red-700">{error}</p>
  if (!board) return <p className="text-slate-500">Loading…</p>

  return (
    <section>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-semibold">
          {project ? project.name : (
            <span className="font-mono text-base text-slate-500">
              Project {board.board.project_id.slice(0, 8)}…
            </span>
          )}
          {project?.archived && (
            <span className="ml-2 text-xs uppercase tracking-wide text-slate-500 border border-slate-300 dark:border-slate-700 rounded px-2 py-1 align-middle">
              archived
            </span>
          )}
        </h1>
        {project && (
          <button
            type="button"
            onClick={toggleArchive}
            disabled={busy}
            className="px-3 py-1.5 rounded border border-slate-300 dark:border-slate-700 text-sm hover:bg-slate-100 dark:hover:bg-slate-800 disabled:opacity-50"
          >
            {busy ? 'Saving…' : project.archived ? 'Unarchive' : 'Archive'}
          </button>
        )}
      </div>
      <KanbanBoard projectId={board.board.project_id} columns={board.columns} />
    </section>
  )
}