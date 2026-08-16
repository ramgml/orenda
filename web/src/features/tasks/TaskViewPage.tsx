import { useParams } from 'react-router-dom';

import { TaskViewBody } from './TaskViewBody';

/**
 * /tasks/:id — full task view (deep-link fallback).
 *
 * Most of the time tasks open as a Trello-style modal via `TaskModal`
 * (see App.tsx + features/tasks/TaskModal.tsx). This route is the
 * standalone fallback: it renders for direct URL hits, browser refresh
 * while the modal is open, and any case where no `backgroundLocation`
 * was passed in navigation state.
 */
export function TaskViewPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  if (!id) return <p className="text-slate-500">Loading…</p>;
  return <TaskViewBody taskId={id} />;
}
