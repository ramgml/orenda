import { useCallback, useEffect, useState } from 'react';

import { api, type Project, type Task } from '@/shared/api/client';
import { TaskCard } from '@/features/projects/TaskCard';
import { openTaskModal } from '@/features/tasks/TaskModal';
import { queueUpdateTask } from '@/shared/offline/outbox';

/**
 * Inbox — flat list of unfiled tasks.
 *
 * Phase 16 replaces the old "system Inbox project" with a dedicated
 * page that fetches /api/v1/inbox/tasks (project_id IS NULL). The list
 * is intentionally minimal: cards only, no board. Each card has a
 * dropdown to file the task under a project — that's the inbox→project
 * transition.
 *
 * Sorting: newest first (matches ListInBox ordering).
 * Quick-add: a textarea + button at the top; submits to the inbox
 * endpoint. Empty title = no-op.
 *
 * Phase Wave 4 PR 2: cards now reuse the shared TaskCard component
 * (priority border, due badge, counters, etc.) instead of the old
 * minimal InboxRow. The file-project dropdown and delete button live
 * alongside as sibling actions.
 */
export function InboxPage(): JSX.Element {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (): Promise<void> => {
    try {
      const [inboxR, projR] = await Promise.all([api.listInboxTasks(), api.listProjects()]);
      setTasks(inboxR.tasks ?? []);
      setProjects((projR ?? []).filter((p) => !p.archived));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function onQuickAdd(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    const title = draft.trim();
    if (!title) return;
    setBusy(true);
    setError(null);
    try {
      const t = await api.createInboxTask({ title });
      setTasks((cur) => [t, ...cur]);
      setDraft('');
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function onFile(taskId: string, projectId: string): Promise<void> {
    setError(null);
    try {
      // Phase Wave 4 PR 2: file-under-project goes through the
      // outbox when the client is offline. The dedicated
      // update_task path is better than the bare PATCH because
      // it carries an idempotency key.
      if (typeof navigator !== 'undefined' && !navigator.onLine) {
        await queueUpdateTask(taskId, { project_id: projectId || '' });
        // Optimistic removal from the inbox list — the task is
        // no longer in the inbox on the server.
        if (projectId !== '') {
          setTasks((cur) => cur.filter((t) => t.id !== taskId));
        }
        return;
      }
      await api.patchTask(taskId, { project_id: projectId || '' });
      // If filing under a project, drop the card; if filing back to
      // "" (would happen only via an explicit empty value), keep it
      // (the server treats "" as "Inbox", so the card stays in the
      // current view).
      if (projectId !== '') {
        setTasks((cur) => cur.filter((t) => t.id !== taskId));
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function onDelete(taskId: string): Promise<void> {
    if (!window.confirm('Delete this task? This cannot be undone.')) return;
    try {
      await api.deleteTask(taskId);
      setTasks((cur) => cur.filter((t) => t.id !== taskId));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section className="space-y-6 p-6 max-w-3xl mx-auto">
      <header>
        <h1 className="text-2xl font-semibold">Inbox</h1>
        <p className="text-sm text-slate-500 mt-1">
          Unfiled tasks. File them onto a project's board when you're ready.
        </p>
      </header>

      <form onSubmit={(e) => void onQuickAdd(e)} className="flex gap-2 items-start">
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={2}
          placeholder="What's on your mind?"
          disabled={busy}
          className="flex-1 px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
          onKeyDown={(e) => {
            // Cmd/Ctrl+Enter submits without a trailing newline.
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
              e.preventDefault();
              void onQuickAdd(e as unknown as React.FormEvent<HTMLFormElement>);
            }
          }}
        />
        <button
          type="submit"
          disabled={busy || !draft.trim()}
          className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
        >
          Add
        </button>
      </form>

      {error && <p className="text-sm text-red-600">{error}</p>}

      {tasks.length === 0 ? (
        <p className="text-sm text-slate-400 italic">
          Nothing in the inbox. Quick-add a thought above to capture it here.
        </p>
      ) : (
        <ul className="space-y-2">
          {tasks.map((t) => (
            <InboxRow key={t.id} task={t} projects={projects} onFile={onFile} onDelete={onDelete} />
          ))}
        </ul>
      )}
    </section>
  );
}

function InboxRow({
  task,
  projects,
  onFile,
  onDelete,
}: {
  task: Task;
  projects: Project[];
  onFile: (id: string, projectId: string) => Promise<void>;
  onDelete: (id: string) => Promise<void>;
}): JSX.Element {
  // Phase Wave 4 PR 2: delegate rendering to the shared TaskCard
  // (priority border, due badge, counters, awaiting/blocked/child
  // counts). The file/delete controls live in a sibling column so
  // they don't fight with the card's own click target.
  //
  // We pass `onOpen` so the card routes to the modal overlay (same
  // path as the kanban). Without `onOpen`, the card would use
  // the global modal helper, which is fine too — but the explicit
  // hook here keeps the inbox inline with the rest of the app.
  return (
    <li className="flex gap-3 items-start">
      <div className="flex-1 min-w-0">
        <TaskCard
          task={task}
          onOpen={() =>
            void openTaskModal(
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              ((path: string) => {
                window.location.href = path;
              }) as unknown as never,
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              window.location as unknown as never,
              task.id,
            )
          }
        />
      </div>
      <div className="flex flex-col gap-1 items-end shrink-0 pt-2">
        <label className="text-[10px] text-slate-500">File under</label>
        <select
          value=""
          onChange={(e) => {
            const v = e.target.value;
            if (v) void onFile(task.id, v);
          }}
          className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-xs"
        >
          <option value="">— pick project —</option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
        <button
          type="button"
          onClick={() => void onDelete(task.id)}
          className="text-[10px] text-red-600 hover:text-red-700"
        >
          delete
        </button>
      </div>
    </li>
  );
}
