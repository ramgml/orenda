import { useEffect, useState } from 'react';

import { useAuth } from '@/features/auth/AuthContext';
import { api, type Agent, type BoardColumn } from '@/shared/api/client';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/ui/select';

/**
 * Phase 27.7 + 27.8.4: editable Status / Priority / Assignee
 * controls for the task sidebar.
 *
 * Three selects, three handlers. Each handler PATCHes the task
 * through api.patchTask and writes the fresh Task back through
 * `onChanged` so the parent can re-render its body. Errors
 * surface inline (no toast spam from a dropdown).
 *
 * Phase 27.8.4: Status options come from the project's columns
 * (via api.getBoard) instead of a hardcoded enum. The board
 * is the source of truth — a column rename or a custom column
 * (Phase 12) shows up in the dropdown automatically. The
 * invariant `task.status ≡ column.status` (closed in 27.8
 * backend) means PATCHing `status` is enough: the backend moves
 * the card.
 *
 * Inbox tasks have `project_id === ''` and no column. The Status
 * control renders as a read-only label instead of a select —
 * assigning the task to a project is required to change status
 * (the column is what carries the new status). This matches the
 * 27.8 decision: "Структурное редактирование — только через проект".
 *
 * Assignee resolution is name-based ("Alice" / "QA-bot") instead
 * of `type:id`. The agent names are loaded once on mount and
 * cached for the lifetime of the component.
 */
export function TaskFieldControls(props: {
  status: string;
  priority: string;
  assigneeType: string;
  assigneeID: string;
  taskID: string;
  busy: boolean;
  /**
   * Phase 27.8.4: pass `task.project_id`. Empty string means
   * the task is in the Inbox (no board, no column) — Status
   * renders as a read-only label.
   */
  projectID: string;
  onChanged: (task: Awaited<ReturnType<typeof api.patchTask>>) => void;
  onError: (msg: string) => void;
}): JSX.Element {
  const { user } = useAuth();
  const [agents, setAgents] = useState<Agent[]>([]);
  const [loadingAgents, setLoadingAgents] = useState(true);
  const [statusOptions, setStatusOptions] = useState<{ value: string; label: string }[] | null>(
    null,
  );

  // Load the agent list once so the Assignee dropdown shows names.
  useEffect(() => {
    let cancelled = false;
    api
      .listAgents()
      .then((list) => {
        if (!cancelled) setAgents(list ?? []);
      })
      .catch(() => {
        // Non-fatal: the dropdown falls back to "id" labels.
      })
      .finally(() => {
        if (!cancelled) setLoadingAgents(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Phase 27.8.4: load the project's columns so the Status select
  // matches the kanban board. Sorted by position so the dropdown
  // order reads naturally (left → right). When projectID is empty
  // (Inbox), `statusOptions` stays null and we render a read-only
  // label below.
  useEffect(() => {
    if (!props.projectID) {
      setStatusOptions(null);
      return;
    }
    let cancelled = false;
    api
      .getBoard(props.projectID)
      .then((pb) => {
        if (cancelled) return;
        const cols = (pb.columns ?? [])
          .slice()
          .sort((a, b) => a.position - b.position)
          .filter((c: BoardColumn) => !!c.status)
          .map((c) => ({ value: c.status as string, label: c.name }));
        setStatusOptions(cols);
      })
      .catch(() => {
        // Defensive: a backend hiccup shouldn't brick the sidebar.
        // Fall back to the canonical five options so the user can
        // still edit status. (The backend will still lift the
        // column onto the new status via SyncStatusAndColumn.)
        if (!cancelled) setStatusOptions(FALLBACK_STATUS_OPTIONS);
      });
    return () => {
      cancelled = true;
    };
  }, [props.projectID]);

  async function patch(patch: Record<string, unknown>): Promise<void> {
    try {
      const fresh = await api.patchTask(props.taskID, patch);
      props.onChanged(fresh);
    } catch (e) {
      props.onError(e instanceof Error ? e.message : String(e));
    }
  }

  // Resolve the current assignee label. Empty / null → "Unassigned".
  function assigneeLabel(): string {
    if (!props.assigneeType) return 'Unassigned';
    if (props.assigneeType === 'user' && user && props.assigneeID === user.user_id) {
      return user.display_name || 'Me';
    }
    if (props.assigneeType === 'user') {
      // Without a global user listing (single-owner, but the
      // middleware doesn't expose /users), we still fall back to
      // "owner" — owner_id is the same shape. The listAgents() call
      // doesn't include the owner; this branch is rarely hit.
      return 'Owner';
    }
    const found = agents.find((a) => a.id === props.assigneeID);
    return found?.name ?? props.assigneeID.slice(0, 8);
  }

  return (
    <div className="space-y-3" data-testid="task-field-controls">
      {statusOptions ? (
        <SidebarSelect
          label="Status"
          value={props.status}
          disabled={props.busy}
          onChange={(v) => void patch({ status: v })}
          data-testid="task-status"
          options={statusOptions}
        />
      ) : (
        <SidebarReadOnlyField
          label="Status"
          value={props.status || '—'}
          hint="Inbox task — assign to a project to change status."
        />
      )}
      <SidebarSelect
        label="Priority"
        value={props.priority}
        disabled={props.busy}
        onChange={(v) => void patch({ priority: v })}
        data-testid="task-priority"
        options={PRIORITY_OPTIONS}
      />
      <div className="rounded border border-border p-3" data-testid="task-assignee">
        <label className="block">
          <span className="text-xs text-slate-500">Assignee</span>
          <Select
            value={assigneeKey(props.assigneeType, props.assigneeID, user?.user_id ?? '')}
            disabled={props.busy || loadingAgents}
            onValueChange={(v) => {
              if (v === 'unassigned') {
                void patch({ assignee_type: '', assignee_id: '' });
                return;
              }
              if (v === 'me') {
                if (!user) return;
                void patch({ assignee_type: 'user', assignee_id: user.user_id });
                return;
              }
              if (v.startsWith('agent:')) {
                const id = v.slice('agent:'.length);
                void patch({ assignee_type: 'agent', assignee_id: id });
              }
            }}
          >
            <SelectTrigger
              className="w-full mt-1 h-8 px-2 text-sm"
              data-testid="task-assignee-trigger"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="unassigned">Unassigned</SelectItem>
              {user && <SelectItem value="me">{user.display_name || 'Me'}</SelectItem>}
              {agents.map((a) => (
                <SelectItem key={a.id} value={`agent:${a.id}`}>
                  {a.name} ({a.status})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-[10px] text-slate-400 mt-1 truncate">currently: {assigneeLabel()}</p>
        </label>
      </div>
    </div>
  );
}

// assigneeKey returns the dropdown value for the current assignee.
// Unassigned → "unassigned". Me → "me". Agent → "agent:<id>".
function assigneeKey(type: string, id: string, ownerID: string): string {
  if (!type) return 'unassigned';
  if (type === 'user' && id === ownerID) return 'me';
  if (type === 'agent') return `agent:${id}`;
  // "user" but not the owner — show as "unassigned" since we don't
  // have a stable dropdown entry for "some other user". The
  // current label below the dropdown tells the truth.
  return 'unassigned';
}

// Fallback used only when getBoard fails (network/500/etc). The
// canonical five are always valid statuses; the backend accepts
// them and SyncStatusAndColumn will move the card to the matching
// column.
const FALLBACK_STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: 'backlog', label: 'Backlog' },
  { value: 'todo', label: 'Todo' },
  { value: 'in_progress', label: 'In progress' },
  { value: 'review', label: 'Review' },
  { value: 'done', label: 'Done' },
];

const PRIORITY_OPTIONS: { value: string; label: string }[] = [
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'urgent', label: 'Urgent' },
];

function SidebarSelect(props: {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
  disabled: boolean;
  'data-testid': string;
}): JSX.Element {
  return (
    <div className="rounded border border-border p-3">
      <label className="block">
        <span className="text-xs text-slate-500">{props.label}</span>
        <Select value={props.value} disabled={props.disabled} onValueChange={props.onChange}>
          <SelectTrigger
            className="w-full mt-1 h-8 px-2 text-sm"
            data-testid={props['data-testid']}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {props.options.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </label>
    </div>
  );
}

// Phase 27.8.4: Inbox tasks (project_id === '') have no board, so
// the Status select cannot offer project-derived options. The
// backend's lookupColumnForStatus returns "" for an empty project,
// meaning a PATCH {status} would silently no-op on the column side.
// Rather than ship a misleading select, we render the current value
// as a label with a hint pointing to the project-filing action.
function SidebarReadOnlyField(props: { label: string; value: string; hint: string }): JSX.Element {
  return (
    <div className="rounded border border-border p-3" data-testid="task-status-readonly">
      <span className="block text-xs text-slate-500">{props.label}</span>
      <span className="block mt-1 text-sm font-medium" data-testid="task-status-value">
        {props.value}
      </span>
      <p className="text-[10px] text-slate-400 mt-1">{props.hint}</p>
    </div>
  );
}
