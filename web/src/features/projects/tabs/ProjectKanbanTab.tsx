import { useEffect, useState } from 'react';
import { useParams } from 'react-router';

import { api, type Project, type ProjectBoard } from '@/shared/api/client';

import { KanbanBoard } from '../KanbanBoard';

/**
 * /projects/:id — Kanban tab.
 *
 * Loads the board independently from the parent so refresh / deep-link
 * to /projects/:id/activity still finds the project + column set in
 * cache. Phase 11 split the project page into tabs; this file owns
 * just the Kanban surface.
 */
export function ProjectKanbanTab(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const [board, setBoard] = useState<ProjectBoard | null>(null);
  const [project, setProject] = useState<Project | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    Promise.all([api.getBoard(id), api.listProjects()])
      .then(([b, list]) => {
        if (cancelled) return;
        setBoard(b);
        setProject(list.find((p) => p.id === id) ?? null);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (error) return <p className="text-red-700">{error}</p>;
  if (!board) return <p className="text-slate-500">Loading…</p>;

  return (
    <>
      {project?.archived && (
        <div className="mb-3 text-xs text-amber-700 dark:text-amber-300 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800 rounded px-3 py-2">
          This project is archived. Unarchive it from Settings to make new changes.
        </div>
      )}
      <KanbanBoard projectId={board.board.project_id} columns={board.columns} />
    </>
  );
}
