import { FormEvent, useState } from 'react'

import { useAuth } from '@/features/auth/AuthContext'
import { api, type Agent } from '@/shared/api/client'

/**
 * /agents — list registered agents + create new ones.
 *
 * Phase 3 ships the minimum: a list with online/offline status, a form
 * to create a new agent (the plaintext token is shown exactly once),
 * and a delete button. Agent-token namespace endpoints (claim/heartbeat)
 * live under /api/v1/agent/* and are documented in the API client.
 */
export function AgentsPage(): JSX.Element {
  const { user } = useAuth()
  const [agents, setAgents] = useState<Agent[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [type, setType] = useState('qwen')
  const [description, setDescription] = useState('')
  const [createdToken, setCreatedToken] = useState<string | null>(null)

  async function load(): Promise<void> {
    try {
      setAgents(await api.listAgents())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  if (agents === null && !error) load()

  async function onCreate(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!name.trim()) return
    try {
      const { agent, plain_token } = await api.createAgent({
        name: name.trim(),
        type,
        description: description.trim() || undefined,
      })
      setCreatedToken(plain_token)
      setAgents((prev) => (prev ? [agent, ...prev] : [agent]))
      setName('')
      setDescription('')
      setCreating(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function onDelete(id: string): Promise<void> {
    if (!confirm('Delete this agent? Its API token stops working immediately.')) return
    try {
      await api.deleteAgent(id)
      setAgents((prev) => (prev ?? []).filter((a) => a.id !== id))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <section>
      <header className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-semibold">Agents</h1>
          <p className="text-sm text-slate-500">
            AI clients that work on tasks via the API.
            {user ? ` Signed in as ${user.email}.` : ''}
          </p>
        </div>
        <button
          type="button"
          onClick={() => setCreating((v) => !v)}
          className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
        >
          {creating ? 'Cancel' : 'New agent'}
        </button>
      </header>

      {error && (
        <div className="mb-4 rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      {createdToken && (
        <div className="mb-4 rounded border border-amber-300 bg-amber-50 text-amber-900 px-4 py-3 text-sm">
          <p className="font-semibold mb-1">API token — copy now, it won't be shown again.</p>
          <code className="block px-2 py-1 bg-white rounded text-xs font-mono break-all">
            {createdToken}
          </code>
          <button
            type="button"
            onClick={() => setCreatedToken(null)}
            className="mt-2 text-xs underline"
          >
            Dismiss
          </button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={onCreate}
          className="mb-4 p-4 rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 grid gap-2"
        >
          <input
            type="text"
            placeholder="Agent name (unique)"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            autoFocus
            required
          />
          <select
            value={type}
            onChange={(e) => setType(e.target.value)}
            className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
          >
            <option value="qwen">qwen</option>
            <option value="claude">claude</option>
            <option value="custom">custom</option>
          </select>
          <input
            type="text"
            placeholder="Description (optional)"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
          />
          <button
            type="submit"
            className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
          >
            Create
          </button>
        </form>
      )}

      {agents === null ? (
        <p className="text-slate-500">Loading…</p>
      ) : agents.length === 0 ? (
        <p className="text-slate-500">No agents yet. Create one to issue an API token.</p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-slate-500 border-b border-slate-200 dark:border-slate-800">
              <th className="py-2">Name</th>
              <th>Type</th>
              <th>Status</th>
              <th>Last seen</th>
              <th>Created</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {agents.map((a) => (
              <tr key={a.id} className="border-b border-slate-100 dark:border-slate-800">
                <td className="py-2 font-mono">{a.name}</td>
                <td>{a.type}</td>
                <td>
                  <span
                    className={`inline-block px-2 py-0.5 rounded text-xs ${
                      a.status === 'online'
                        ? 'bg-green-100 text-green-800'
                        : a.status === 'disabled'
                          ? 'bg-slate-200 text-slate-600'
                          : 'bg-amber-100 text-amber-800'
                    }`}
                  >
                    {a.status}
                  </span>
                </td>
                <td>{a.last_seen_at ?? '—'}</td>
                <td className="text-slate-500 text-xs">{a.created_at}</td>
                <td>
                  <button
                    type="button"
                    onClick={() => onDelete(a.id)}
                    className="text-red-600 text-xs hover:underline"
                  >
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}