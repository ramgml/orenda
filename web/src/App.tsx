import { useEffect, useState } from 'react'
import { Link, Route, Routes } from 'react-router-dom'

import { api, type InfoResponse } from '@/shared/api/client'
import { HealthBadge } from '@/shared/ui/HealthBadge'

/**
 * Top-level shell. Phase 0 ships the dashboard stub; feature modules (kanban,
 * calendar, wiki, agents, settings) wire in over Phases 1-7.
 */
export function App(): JSX.Element {
  const [info, setInfo] = useState<InfoResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api
      .info()
      .then(setInfo)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  return (
    <div className="min-h-full flex flex-col">
      <header className="border-b border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950">
        <div className="max-w-6xl mx-auto px-6 py-4 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2 font-semibold text-lg">
            <span className="inline-block h-6 w-6 rounded bg-orenda-500" aria-hidden />
            Orenda
          </Link>
          <nav className="flex items-center gap-4 text-sm text-slate-600 dark:text-slate-300">
            <Link to="/" className="hover:text-orenda-600">Dashboard</Link>
            <Link to="/projects" className="hover:text-orenda-600">Projects</Link>
            <Link to="/agents" className="hover:text-orenda-600">Agents</Link>
            <Link to="/settings" className="hover:text-orenda-600">Settings</Link>
            <HealthBadge />
          </nav>
        </div>
      </header>

      <main className="flex-1 max-w-6xl mx-auto w-full px-6 py-8">
        <Routes>
          <Route path="/" element={<Dashboard info={info} error={error} />} />
          <Route path="/projects" element={<Placeholder title="Projects" />} />
          <Route path="/agents" element={<Placeholder title="Agents" />} />
          <Route path="/settings" element={<Placeholder title="Settings" />} />
          <Route path="*" element={<Placeholder title="Not found" />} />
        </Routes>
      </main>

      <footer className="border-t border-slate-200 dark:border-slate-800 text-xs text-slate-500 text-center py-4">
        Orenda {info?.version ?? '…'} · local-first productivity
      </footer>
    </div>
  )
}

function Dashboard({ info, error }: { info: InfoResponse | null; error: string | null }): JSX.Element {
  return (
    <section>
      <h1 className="text-2xl font-semibold mb-4">Dashboard</h1>

      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 p-3 text-sm">
          Failed to reach backend: {error}
        </div>
      )}

      {info && (
        <div className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950">
          <div className="text-sm text-slate-500">Server</div>
          <div className="text-lg font-mono">
            {info.name} {info.version}
          </div>
          <div className="mt-3 grid grid-cols-2 sm:grid-cols-4 gap-2 text-xs">
            {Object.entries(info.capabilities).map(([k, v]) => (
              <span
                key={k}
                className={`px-2 py-1 rounded ${
                  v
                    ? 'bg-green-100 text-green-800'
                    : 'bg-slate-100 text-slate-500'
                }`}
              >
                {k}
              </span>
            ))}
          </div>
        </div>
      )}

      <p className="mt-6 text-slate-600 dark:text-slate-300">
        Phase 0 ships the shell. Tasks, kanban, calendar, wiki, agents and
        backups arrive in Phases 1–7.
      </p>
    </section>
  )
}

function Placeholder({ title }: { title: string }): JSX.Element {
  return (
    <section>
      <h1 className="text-2xl font-semibold mb-2">{title}</h1>
      <p className="text-slate-600 dark:text-slate-300">Coming in a later phase.</p>
    </section>
  )
}