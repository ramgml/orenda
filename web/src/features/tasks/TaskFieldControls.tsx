import { useEffect, useState } from 'react'

import { useAuth } from '@/features/auth/AuthContext'
import { api, type Agent } from '@/shared/api/client'

/**
 * Phase 27.7: editable Status / Priority / Assignee controls for
 * the task sidebar.
 *
 * Three selects, three handlers. Each handler PATCHes the task
 * through api.patchTask and writes the fresh Task back through
 * `onChanged` so the parent can re-render its body. Errors
 * surface inline (no toast spam from a dropdown).
 *
 * Why three separate selects rather than one form: the sidebar
 * is dense and the fields are independent. A single PATCH per
 * change keeps the audit feed readable (one `status_changed`
 * row per click, not a meta-update that touches everything).
 *
 * Assignee resolution is name-based ("Alice" / "QA-bot") instead
 * of `type:id`. The owner of an active task doesn't need to know
 * the underlying ID; the agent names are loaded once on mount
 * and cached for the lifetime of the component.
 */
export function TaskFieldControls(props: {
  status: string
  priority: string
  assigneeType: string
  assigneeID: string
  taskID: string
  busy: boolean
  onChanged: (task: Awaited<ReturnType<typeof api.patchTask>>) => void
  onError: (msg: string) => void
}): JSX.Element {
  const { user } = useAuth()
  const [agents, setAgents] = useState<Agent[]>([])
  const [loadingAgents, setLoadingAgents] = useState(true)

  // Load the agent list once so the Assignee dropdown shows names.
  useEffect(() => {
    let cancelled = false
    api
      .listAgents()
      .then((list) => {
        if (!cancelled) setAgents(list ?? [])
      })
      .catch(() => {
        // Non-fatal: the dropdown falls back to "id" labels.
      })
      .finally(() => {
        if (!cancelled) setLoadingAgents(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  async function patch(patch: Record<string, unknown>): Promise<void> {
    try {
      const fresh = await api.patchTask(props.taskID, patch)
      props.onChanged(fresh)
    } catch (e) {
      props.onError(e instanceof Error ? e.message : String(e))
    }
  }

  // Resolve the current assignee label. Empty / null → "Unassigned".
  function assigneeLabel(): string {
    if (!props.assigneeType) return 'Unassigned'
    if (props.assigneeType === 'user' && user && props.assigneeID === user.user_id) {
      return user.display_name || 'Me'
    }
    if (props.assigneeType === 'user') {
      // Without a global user listing (single-owner, but the
      // middleware doesn't expose /users), we still fall back to
      // "owner" — owner_id is the same shape. The listAgents() call
      // doesn't include the owner; this branch is rarely hit.
      return 'Owner'
    }
    const found = agents.find((a) => a.id === props.assigneeID)
    return found?.name ?? props.assigneeID.slice(0, 8)
  }

  return (
    <div className="space-y-3" data-testid="task-field-controls">
      <SidebarSelect
        label="Status"
        value={props.status}
        disabled={props.busy}
        onChange={(v) => void patch({ status: v })}
        data-testid="task-status"
        options={STATUS_OPTIONS}
      />
      <SidebarSelect
        label="Priority"
        value={props.priority}
        disabled={props.busy}
        onChange={(v) => void patch({ priority: v })}
        data-testid="task-priority"
        options={PRIORITY_OPTIONS}
      />
      <div
        className="rounded border border-slate-200 dark:border-slate-800 p-3"
        data-testid="task-assignee"
      >
        <label className="block">
          <span className="text-xs text-slate-500">Assignee</span>
          <select
            value={assigneeKey(props.assigneeType, props.assigneeID, user?.user_id ?? '')}
            disabled={props.busy || loadingAgents}
            onChange={(e) => {
              const v = e.target.value
              if (v === 'unassigned') {
                void patch({ assignee_type: '', assignee_id: '' })
                return
              }
              if (v === 'me') {
                if (!user) return
                void patch({ assignee_type: 'user', assignee_id: user.user_id })
                return
              }
              if (v.startsWith('agent:')) {
                const id = v.slice('agent:'.length)
                void patch({ assignee_type: 'agent', assignee_id: id })
              }
            }}
            className="w-full mt-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-sm"
          >
            <option value="unassigned">Unassigned</option>
            {user && <option value="me">{user.display_name || 'Me'}</option>}
            {agents.map((a) => (
              <option key={a.id} value={`agent:${a.id}`}>
                {a.name} ({a.status})
              </option>
            ))}
          </select>
          <p className="text-[10px] text-slate-400 mt-1 truncate">
            currently: {assigneeLabel()}
          </p>
        </label>
      </div>
    </div>
  )
}

// assigneeKey returns the <select> value for the current assignee.
// Unassigned → "unassigned". Me → "me". Agent → "agent:<id>".
function assigneeKey(type: string, id: string, ownerID: string): string {
  if (!type) return 'unassigned'
  if (type === 'user' && id === ownerID) return 'me'
  if (type === 'agent') return `agent:${id}`
  // "user" but not the owner — show as "unassigned" since we don't
  // have a stable dropdown entry for "some other user". The
  // current label below the dropdown tells the truth.
  return 'unassigned'
}

const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: 'backlog', label: 'Backlog' },
  { value: 'todo', label: 'Todo' },
  { value: 'in_progress', label: 'In progress' },
  { value: 'review', label: 'Review' },
  { value: 'done', label: 'Done' },
]

const PRIORITY_OPTIONS: { value: string; label: string }[] = [
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'urgent', label: 'Urgent' },
]

function SidebarSelect(props: {
  label: string
  value: string
  options: { value: string; label: string }[]
  onChange: (v: string) => void
  disabled: boolean
  'data-testid': string
}): JSX.Element {
  return (
    <div className="rounded border border-slate-200 dark:border-slate-800 p-3">
      <label className="block">
        <span className="text-xs text-slate-500">{props.label}</span>
        <select
          value={props.value}
          disabled={props.disabled}
          onChange={(e) => props.onChange(e.target.value)}
          data-testid={props['data-testid']}
          className="w-full mt-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-sm"
        >
          {props.options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </label>
    </div>
  )
}
