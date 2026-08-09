import { ReactNode } from 'react'
import { Link, Navigate, Route, Routes } from 'react-router-dom'

import { AuthProvider, useAuth } from '@/features/auth/AuthContext'
import { LoginPage } from '@/features/auth/LoginPage'
import { ProjectsPage } from '@/features/projects/ProjectsPage'
import { ProjectDetailPage } from '@/features/projects/ProjectDetailPage'
import { AgentsPage } from '@/features/agents/AgentsPage'
import { TaskViewPage } from '@/features/tasks/TaskViewPage'
import { CalendarPage } from '@/features/calendar/CalendarPage'
import { TimerWidget } from '@/features/tasks/TimerWidget'
import { WikiPage } from '@/features/wiki/WikiPage'
import { SearchPage } from '@/features/search/SearchPage'
import { NotificationsBell } from '@/features/notifications/NotificationsBell'
import { BackupsSettingsPage } from '@/features/settings/Backups'
import { BotsSettingsPage } from '@/features/settings/Bots'
import { ReportsPage } from '@/features/reports/ReportsPage'
import { ThemeToggle } from '@/shared/ui/ThemeToggle'
import { api, type InfoResponse } from '@/shared/api/client'
import { HealthBadge } from '@/shared/ui/HealthBadge'
import { useEffect, useState } from 'react'

/**
 * Top-level shell. Phase 1 ships the auth-aware shell with Dashboard,
 * Projects list, Project detail, Agents/Settings placeholders.
 */
export function App(): JSX.Element {
  return (
    <AuthProvider>
      <Shell />
    </AuthProvider>
  )
}

function Shell(): JSX.Element {
  const { status, user, logout } = useAuth()
  const [info, setInfo] = useState<InfoResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api
      .info()
      .then(setInfo)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  if (status === 'loading') {
    return (
      <div className="min-h-full flex items-center justify-center text-slate-500">
        Loading session…
      </div>
    )
  }

  return (
    <div className="min-h-full flex flex-col">
      {status === 'authenticated' && (
        <header className="border-b border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950">
          <div className="w-full px-6 py-3 flex items-center justify-between gap-4">
            <Link to="/" className="flex items-center gap-2 font-semibold text-lg">
              <span className="inline-block h-6 w-6 rounded bg-orenda-500" aria-hidden />
              Orenda
            </Link>
            <nav className="flex items-center gap-4 text-sm text-slate-600 dark:text-slate-300">
              <Link to="/" className="hover:text-orenda-600">Dashboard</Link>
              <Link to="/projects" className="hover:text-orenda-600">Projects</Link>
              <Link to="/agents" className="hover:text-orenda-600">Agents</Link>
              <Link to="/calendar" className="hover:text-orenda-600">Calendar</Link>
              <Link to="/wiki" className="hover:text-orenda-600">Wiki</Link>
              <Link to="/search" className="hover:text-orenda-600">Search</Link>
              <Link to="/reports" className="hover:text-orenda-600">Reports</Link>
              <Link to="/settings" className="hover:text-orenda-600">Settings</Link>
              <NotificationsBell />
              <HealthBadge />
              <ThemeToggle />
              <span className="text-xs text-slate-400">{user?.email}</span>
              <button
                type="button"
                onClick={() => logout()}
                className="px-2 py-1 rounded text-xs border border-slate-300 dark:border-slate-700"
              >
                Sign out
              </button>
            </nav>
          </div>
        </header>
      )}

      <main className="flex-1 w-full px-6 py-6">
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<RequireAuth><Dashboard info={info} error={error} /></RequireAuth>} />
          <Route path="/projects" element={<RequireAuth><ProjectsPage /></RequireAuth>} />
          <Route path="/projects/:id" element={<RequireAuth><ProjectDetailPage /></RequireAuth>} />
          <Route path="/agents" element={<RequireAuth><AgentsPage /></RequireAuth>} />
          <Route path="/tasks/:id" element={<RequireAuth><TaskViewPage /></RequireAuth>} />
          <Route path="/calendar" element={<RequireAuth><CalendarPage /></RequireAuth>} />
          <Route path="/wiki/:slug?" element={<RequireAuth><WikiPage /></RequireAuth>} />
          <Route path="/search" element={<RequireAuth><SearchPage /></RequireAuth>} />
          <Route path="/settings" element={<RequireAuth><Placeholder title="Settings" /></RequireAuth>} />
          <Route path="/settings/backups" element={<RequireAuth><BackupsSettingsPage /></RequireAuth>} />
          <Route path="/settings/bots" element={<RequireAuth><BotsSettingsPage /></RequireAuth>} />
          <Route path="/reports" element={<RequireAuth><ReportsPage /></RequireAuth>} />
          <Route path="*" element={<Placeholder title="Not found" />} />
        </Routes>
      </main>

      <footer className="border-t border-slate-200 dark:border-slate-800 text-xs text-slate-500 text-center py-4">
        Orenda {info?.version ?? '…'} · local-first productivity
      </footer>

      {/* Floating timer widget — renders bottom-right when authenticated. */}
      <TimerWidget />
    </div>
  )
}

/**
 * Gate a route on authenticated status. Redirects to /login on miss,
 * preserving the requested path so the user lands back where they started.
 */
function RequireAuth({ children }: { children: ReactNode }): JSX.Element {
  const { status } = useAuth()
  const location = window.location
  if (status !== 'authenticated') {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }
  return <>{children}</>
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
        Phase 1 ships auth + projects + tasks. The kanban board lands in
        Phase 2; agents and bot subscriptions arrive in Phases 3 & 6.
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