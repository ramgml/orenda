import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'

import { api, type ProjectBoard } from '@/shared/api/client'

import { KanbanBoard } from './KanbanBoard'

/**
 * /projects/:id — single project kanban board view.
 *
 * Phase 2 replaces the Phase 1 read-only stub with @dnd-kit/core drag-and-drop.
 */
export function ProjectDetailPage(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const [board, setBoard] = useState<ProjectBoard | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    let cancelled = false
    api
      .getBoard(id)
      .then((b) => {
        if (!cancelled) setBoard(b)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [id])

  if (error) return <p className="text-red-700">{error}</p>
  if (!board) return <p className="text-slate-500">Loading…</p>

  return (
    <section>
      <h1 className="text-2xl font-semibold mb-4 font-mono text-sm text-slate-500">
        Project {board.board.project_id.slice(0, 8)}…
      </h1>
      <KanbanBoard
        projectId={board.board.project_id}
        columns={board.columns}
      />
    </section>
  )
}