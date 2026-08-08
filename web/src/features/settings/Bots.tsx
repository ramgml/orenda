import { FormEvent, useEffect, useState } from 'react'

import { api } from '@/shared/api/client'

interface Subscription {
  id: string
  bot_type: string
  target_address: string
  events: string[]
  enabled: boolean
}

const BOT_TYPES = ['console', 'webhook', 'email', 'telegram', 'vk']
const EVENT_TYPES = [
  'task.review_needed',
  'task.assigned_to_me',
  'mention.created',
  'task.commented',
  'agent.offline',
  'backup.failed',
]

/**
 * /settings/bots — manage bot subscriptions (channel × events).
 *
 * The bot credentials themselves live in config.yaml; this page manages
 * which user/channel receives which events.
 */
export function BotsSettingsPage(): JSX.Element {
  const [subs, setSubs] = useState<Subscription[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [info, setInfo] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [botType, setBotType] = useState('webhook')
  const [target, setTarget] = useState('')
  const [selectedEvents, setSelectedEvents] = useState<string[]>(['task.review_needed'])

  async function load(): Promise<void> {
    try {
      const res = await api.listSubscriptions()
      setSubs(res.subscriptions)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    load()
  }, [])

  function toggleEvent(name: string): void {
    setSelectedEvents((cur) =>
      cur.includes(name) ? cur.filter((e) => e !== name) : [...cur, name],
    )
  }

  async function onCreate(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    setError(null)
    setInfo(null)
    try {
      await api.createSubscription({
        bot_type: botType,
        target_address: target.trim(),
        events: selectedEvents,
        enabled: true,
      })
      setCreating(false)
      setTarget('')
      setInfo('Subscription added.')
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  async function onDelete(id: string): Promise<void> {
    try {
      await api.deleteSubscription(id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <section className="space-y-4">
      <header className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Bot subscriptions</h1>
          <p className="text-sm text-slate-500">
            Which channel receives which events. Bot credentials live in{' '}
            <code className="px-1 bg-slate-100 dark:bg-slate-800 rounded">data/config.yaml</code>.
          </p>
        </div>
        <button
          type="button"
          onClick={() => setCreating((v) => !v)}
          className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
        >
          {creating ? 'Cancel' : 'Add subscription'}
        </button>
      </header>

      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}
      {info && (
        <div className="rounded border border-green-300 bg-green-50 text-green-800 px-3 py-2 text-sm">
          {info}
        </div>
      )}

      {creating && (
        <form
          onSubmit={onCreate}
          className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-4 grid gap-3"
        >
          <div className="grid sm:grid-cols-2 gap-3">
            <label className="grid gap-1 text-sm">
              <span className="text-slate-500">Bot type</span>
              <select
                value={botType}
                onChange={(e) => setBotType(e.target.value)}
                className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
              >
                {BOT_TYPES.map((t) => (
                  <option key={t} value={t}>{t}</option>
                ))}
              </select>
            </label>
            <label className="grid gap-1 text-sm">
              <span className="text-slate-500">Target address</span>
              <input
                type="text"
                value={target}
                onChange={(e) => setTarget(e.target.value)}
                placeholder={targetPlaceholder(botType)}
                className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
                required
              />
            </label>
          </div>
          <fieldset>
            <legend className="text-sm text-slate-500 mb-1">Events</legend>
            <div className="flex flex-wrap gap-2">
              {EVENT_TYPES.map((ev) => (
                <label
                  key={ev}
                  className={`px-2 py-1 rounded border text-xs cursor-pointer ${
                    selectedEvents.includes(ev)
                      ? 'border-orenda-500 bg-orenda-50 dark:bg-orenda-900/20'
                      : 'border-slate-300 dark:border-slate-700'
                  }`}
                >
                  <input
                    type="checkbox"
                    checked={selectedEvents.includes(ev)}
                    onChange={() => toggleEvent(ev)}
                    className="sr-only"
                  />
                  {ev}
                </label>
              ))}
            </div>
          </fieldset>
          <button
            type="submit"
            className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
          >
            Add subscription
          </button>
        </form>
      )}

      {subs === null ? (
        <p className="text-slate-500">Loading…</p>
      ) : subs.length === 0 ? (
        <p className="text-slate-500">No subscriptions yet.</p>
      ) : (
        <ul className="divide-y divide-slate-100 dark:divide-slate-800 rounded border border-slate-200 dark:border-slate-800">
          {subs.map((s) => (
            <li key={s.id} className="px-4 py-3 flex items-center justify-between text-sm">
              <div>
                <p className="font-medium">
                  {s.bot_type} → <span className="font-mono text-xs">{s.target_address}</span>
                </p>
                <p className="text-xs text-slate-500">{s.events.join(', ')}</p>
              </div>
              <button
                type="button"
                onClick={() => onDelete(s.id)}
                className="text-red-600 text-xs hover:underline"
              >
                Delete
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function targetPlaceholder(botType: string): string {
  switch (botType) {
    case 'webhook':
      return 'https://example.com/hook'
    case 'email':
      return 'you@example.com'
    case 'telegram':
      return '123456789 (chat id)'
    case 'vk':
      return '2000000001 (peer id)'
    default:
      return 'console'
  }
}