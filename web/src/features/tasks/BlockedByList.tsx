import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { api, type BlockerRow } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Checkbox } from '@/shared/ui/checkbox';
import { useWebSocketTopic } from '@/shared/ws';

/**
 * Phase 15: Blocked-by list on the task page.
 *
 * Two states:
 *   - "View" mode: just shows each blocker (open: red text, struck through
 *     if done). The owner can read them at a glance.
 *   - "Edit" mode: a multi-select of every task in the project, with the
 *     current blockers pre-checked. Submit replaces the full set in one
 *     PUT.
 *
 * WS auto-refresh: any change to the deps emits "task.deps_changed" so
 * other tabs stay in sync without polling.
 */
export function BlockedByList({
  taskId,
  projectId,
}: {
  taskId: string;
  projectId: string;
}): JSX.Element {
  const [blockers, setBlockers] = useState<BlockerRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (): Promise<void> => {
    try {
      const r = await api.listTaskBlockers(taskId);
      setBlockers(r.blockers ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [taskId]);

  useEffect(() => {
    void load();
  }, [load]);

  // Refresh on any task event (covers dep changes, blocker status
  // flips, etc.). The event body has a `task_id` for filter; we keep
  // it simple here and refetch on every event for our task.
  useWebSocketTopic('tasks', () => {
    void load();
  });

  if (loading) return <p className="text-xs text-slate-400 italic">Loading…</p>;

  const open = blockers.filter((b) => !b.done);

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <h2 className="text-sm font-semibold text-slate-500">
          Blocked by{open.length > 0 ? ` (${open.length} open)` : ''}
        </h2>
        {!editing && (
          <Button
            type="button"
            variant="link"
            onClick={() => setEditing(true)}
            className="h-auto p-0 text-[10px] text-slate-500 hover:text-orenda-600"
            data-testid="deps-edit-toggle"
          >
            edit
          </Button>
        )}
      </div>

      {error && <p className="text-xs text-red-600">{error}</p>}

      {editing ? (
        <DependencyEditor
          taskId={taskId}
          projectId={projectId}
          initial={blockers.map((b) => b.blocker_id)}
          onDone={() => {
            setEditing(false);
            void load();
          }}
          onCancel={() => setEditing(false)}
        />
      ) : blockers.length === 0 ? (
        <p className="text-xs text-slate-400 italic">None.</p>
      ) : (
        <ul className="space-y-1">
          {blockers.map((b) => (
            <li
              key={b.blocker_id}
              data-testid="blocker-row"
              className={`text-xs ${b.done ? 'line-through text-slate-400' : 'text-foreground'}`}
            >
              <Link to={`/tasks/${b.blocker_id}`} className="hover:underline">
                {b.title}
              </Link>
              <span className="ml-2 text-[10px] text-slate-400 font-mono">{b.status}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Multi-select editor for the full blocker set.
 *
 * Loads the project's tasks via the project list endpoint and lets
 * the user check the ones that should block this task. Submit
 * replaces the full set; cancel discards.
 *
 * Self-cycles are detected client-side as a sanity check (the API
 * returns 422 otherwise). We don't try to render a graph view.
 */
function DependencyEditor({
  taskId,
  projectId,
  initial,
  onDone,
  onCancel,
}: {
  taskId: string;
  projectId: string;
  initial: string[];
  onDone: () => void;
  onCancel: () => void;
}): JSX.Element {
  const [taskChoices, setTaskChoices] = useState<Array<{ id: string; title: string }>>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set(initial));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!projectId) {
      // Inbox task: skip the editor (no project-scoped tasks to pick).
      setTaskChoices([]);
      return;
    }
    void (async () => {
      try {
        // listProjectTasks returns everything; we exclude self.
        const items = await api.listProjectTasks(projectId);
        setTaskChoices(
          (items ?? []).filter((t) => t.id !== taskId).map((t) => ({ id: t.id, title: t.title })),
        );
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    })();
  }, [projectId, taskId]);

  function toggle(id: string): void {
    setSelected((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function onSubmit(): Promise<void> {
    setBusy(true);
    setError(null);
    try {
      await api.setTaskDependencies(taskId, Array.from(selected));
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="rounded border border-border p-2 bg-muted/40 space-y-2">
      {!projectId ? (
        <p className="text-xs text-slate-500 italic">
          File the task under a project to add blockers.
        </p>
      ) : taskChoices.length === 0 ? (
        <p className="text-xs text-slate-500 italic">No other tasks in this project yet.</p>
      ) : (
        <ul className="max-h-48 overflow-y-auto space-y-1">
          {taskChoices.map((t) => (
            <li key={t.id} className="flex items-center gap-2 text-xs">
              <Checkbox
                id={`dep-${t.id}`}
                checked={selected.has(t.id)}
                onCheckedChange={() => toggle(t.id)}
              />
              <label htmlFor={`dep-${t.id}`} className="text-foreground truncate">
                {t.title}
              </label>
            </li>
          ))}
        </ul>
      )}
      {error && <p className="text-xs text-red-600">{error}</p>}
      <div className="flex gap-2">
        <Button
          type="button"
          size="sm"
          onClick={() => void onSubmit()}
          disabled={busy || !projectId}
          className="text-xs"
        >
          Save
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onCancel}
          disabled={busy}
          className="text-xs"
        >
          Cancel
        </Button>
      </div>
    </div>
  );
}
