import { ReactNode, useEffect, useState } from 'react'
import { Link, Navigate, Route, Routes } from 'react-router-dom'

import { AuthProvider, useAuth } from '@/features/auth/AuthContext'
import { LoginPage } from '@/features/auth/LoginPage'
import { ProjectsPage } from '@/features/projects/ProjectsPage'
import { ProjectDetailPage } from '@/features/projects/ProjectDetailPage'
import { ProjectKanbanTab } from '@/features/projects/tabs/ProjectKanbanTab'
import { ProjectActivityTab } from '@/features/projects/tabs/ProjectActivityTab'
import { ProjectAttachmentsTab } from '@/features/projects/tabs/ProjectAttachmentsTab'
import { ProjectSettingsTab } from '@/features/projects/tabs/ProjectSettingsTab'
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
import { api, type InfoResponse, type Task } from '@/shared/api/client'
import { HealthBadge } from '@/shared/ui/HealthBadge'

/**
 * Top-level shell. Auth-aware layout with a Dashboard that surfaces
 * live counts (projects, open tasks, agents, upcoming events) and a
 * top nav linking to every major section.
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
          <Route path="/projects/:id" element={<RequireAuth><ProjectDetailPage /></RequireAuth>}>
            <Route index element={<ProjectKanbanTab />} />
            <Route path="activity" element={<ProjectActivityTab />} />
            <Route path="attachments" element={<ProjectAttachmentsTab />} />
            <Route path="settings" element={<ProjectSettingsTab />} />
          </Route>
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
    <section className="space-y-4">
      <h1 className="text-2xl font-semibold">Dashboard</h1>

      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 p-3 text-sm">
          Failed to reach backend: {error}
        </div>
      )}

      <Stats />

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
    </section>
  )
}

// Stats — four live cards with project/task/agent/event counts. Each
// card links to its section so the dashboard is a navigation hub as
// much as a status screen.
function Stats(): JSX.Element {
  const [projects, setProjects] = useState<number | null>(null)
  const [openTasks, setOpenTasks] = useState<number | null>(null)
  const [agents, setAgents] = useState<number | null>(null)
  const [events, setEvents] = useState<number | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    async function load(): Promise<void> {
      try {
        const [proj, ag] = await Promise.all([api.listProjects(), api.listAgents()])
        if (cancelled) return
        setProjects(proj.length)
        setAgents(ag.length)
        // Open tasks: sum of tasks where status !== 'done' across projects.
        // We piggyback on listProjectTasks; small projects make this fine.
        const allTasks = await Promise.all(
          proj.map((p) => api.listProjectTasks(p.id).catch(() => [] as Task[])),
        )
        if (cancelled) return
        const open = allTasks
          .flat()
          .filter((t) => t.status !== 'done')
          .length
        setOpenTasks(open)
        // Upcoming events in the next 7 days.
        const now = new Date()
        const week = new Date(now)
        week.setDate(week.getDate() + 7)
        const evs = await api
          .listEvents({
            from: now.toISOString(),
            to: week.toISOString(),
          })
          .catch(() => [])
        if (cancelled) return
        setEvents(evs.length)
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e))
      }
    }
    load()
    return () => {
      cancelled = true
    }
  }, [])

  return (
    <div className="space-y-2">
      {err && (
        <div className="rounded border border-amber-300 bg-amber-50 text-amber-800 p-2 text-xs">
          Could not load stats: {err}
        </div>
      )}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <StatCard
          to="/projects"
          label="Projects"
          value={projects}
          accent="bg-orenda-100 dark:bg-orenda-900/30 text-orenda-700 dark:text-orenda-300"
        />
        <StatCard
          to="/"
          label="Open tasks"
          value={openTasks}
          accent="bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-300"
        />
        <StatCard
          to="/agents"
          label="Agents"
          value={agents}
          accent="bg-emerald-100 dark:bg-emerald-900/30 text-emerald-700 dark:text-emerald-300"
        />
        <StatCard
          to="/calendar"
          label="Events (7d)"
          value={events}
          accent="bg-sky-100 dark:bg-sky-900/30 text-sky-700 dark:text-sky-300"
        />
      </div>
    </div>
  )
}

function StatCard({
  to,
  label,
  value,
  accent,
}: {
  to: string
  label: string
  value: number | null
  accent: string
}): JSX.Element {
  return (
    <Link
      to={to}
      className="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-4 hover:border-orenda-500 transition"
    >
      <div className="text-xs text-slate-500 uppercase tracking-wide">{label}</div>
      <div className={`mt-2 inline-block px-3 py-1 rounded font-mono text-2xl ${accent}`}>
        {value === null ? '…' : value}
      </div>
    </Link>
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