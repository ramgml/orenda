import { useCallback, useEffect, useState } from 'react';

import { TaskLink } from '@/features/tasks/TaskModal';
import { api, type BlockerRow } from '@/shared/api/client';

import { Button } from '@/shared/ui/button';
import { Checkbox } from '@/shared/ui/checkbox';
import { Input } from '@/shared/ui/input';
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
          Блокируется задачами{open.length > 0 ? ` (${open.length} open)` : ''}
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
              <TaskLink taskId={b.blocker_id} className="hover:underline">
                {b.title}
              </TaskLink>
              <span className="ml-2 text-[10px] text-slate-400 font-mono">{b.status}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Editor for the blocker set (Task 115).
 *
 * Single-edge operations: checking a candidate POSTs /blocks right
 * away; unchecking a current blocker DELETEs the edge. A search box
 * filters candidates client-side by T-number ("#12", "T12") and
 * title — the project list can be long, and the постановка asked for
 * search-by-number. The full-replace PUT is kept behind a "Replace
 * all" button for bulk edits.
 *
 * Cycle/self errors surface from the API (422) — no client-side
 * graph walk; the backend is the source of truth.
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
  const [taskChoices, setTaskChoices] = useState<
    Array<{ id: string; number: number; title: string }>
  >([]);
  const [selected, setSelected] = useState<Set<string>>(new Set(initial));
  const [search, setSearch] = useState('');
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
          (items ?? [])
            .filter((t) => t.id !== taskId)
            .map((t) => ({ id: t.id, number: t.number ?? 0, title: t.title })),
        );
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    })();
  }, [projectId, taskId]);

  // Search filter: match the T-number ("12", "#12", "T12") and the
  // title, case-insensitive.
  const q = search.trim().toLowerCase();
  const visible = q
    ? taskChoices.filter((t) => {
        const num = t.number > 0 ? String(t.number) : '';
        const numHit = num !== '' && (q === num || q === `#${num}` || q === `t${num}`);
        return numHit || t.title.toLowerCase().includes(q);
      })
    : taskChoices;

  async function toggle(id: string, checked: boolean): Promise<void> {
    setBusy(true);
    setError(null);
    try {
      if (checked) {
        await api.addTaskBlocker(taskId, id);
        setSelected((cur) => new Set(cur).add(id));
      } else {
        await api.removeTaskBlocker(taskId, id);
        setSelected((cur) => {
          const next = new Set(cur);
          next.delete(id);
          return next;
        });
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function onReplaceAll(): Promise<void> {
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
      {projectId && taskChoices.length > 0 && (
        <Input
          type="search"
          placeholder="Search by #number or title…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="h-7 text-xs"
          data-testid="deps-search"
        />
      )}
      {!projectId ? (
        <p className="text-xs text-slate-500 italic">
          File the task under a project to add blockers.
        </p>
      ) : taskChoices.length === 0 ? (
        <p className="text-xs text-slate-500 italic">No other tasks in this project yet.</p>
      ) : visible.length === 0 ? (
        <p className="text-xs text-slate-500 italic">No tasks match “{search}”.</p>
      ) : (
        <ul className="max-h-48 overflow-y-auto space-y-1">
          {visible.map((t) => (
            <li key={t.id} className="flex items-center gap-2 text-xs">
              <Checkbox
                id={`dep-${t.id}`}
                checked={selected.has(t.id)}
                disabled={busy}
                onCheckedChange={(v) => void toggle(t.id, v === true)}
              />
              <label htmlFor={`dep-${t.id}`} className="text-foreground truncate">
                {t.number > 0 && <span className="font-mono text-slate-400 mr-1">#{t.number}</span>}
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
          onClick={() => void onReplaceAll()}
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
