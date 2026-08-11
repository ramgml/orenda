import { ReactNode, useEffect, useState } from 'react'
import { Link, Navigate, Route, Routes, useLocation } from 'react-router-dom'

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
import { TaskModal } from '@/features/tasks/TaskModal'
import { CalendarPage } from '@/features/calendar/CalendarPage'
import { TimerWidget } from '@/features/tasks/TimerWidget'
import { WikiPage } from '@/features/wiki/WikiPage'
import { SearchPage } from '@/features/search/SearchPage'
import { BackupsSettingsPage } from '@/features/settings/Backups'
import { BotsSettingsPage } from '@/features/settings/Bots'
import { ReportsPage } from '@/features/reports/ReportsPage'
import { InboxPage } from '@/features/inbox/InboxPage'
import { QuickCapture } from '@/features/inbox/QuickCapture'
import { ReviewPage } from '@/features/review/ReviewPage'
import { api, type InfoResponse, type Task } from '@/shared/api/client'

import { AppLayout } from '@/features/layout/AppLayout'

// State carried in navigation when opening a task as a modal.
// See features/tasks/TaskModal.tsx for the open/close contract.
type BackgroundLocationState = { backgroundLocation?: { pathname: string } }

/**
 * Top-level shell. The authenticated surface is now wrapped in
 * <AppLayout> which provides the collapsible sidebar with project
 * navigation. The Dashboard + login screens sit outside this layout
 * because they have a different chrome (no project rail).
 */
export function App(): JSX.Element {
  return (
    <AuthProvider>
      <Shell />
    </AuthProvider>
  )
}

function Shell(): JSX.Element {
  const [info, setInfo] = useState<InfoResponse | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    api
      .info()
      .then(setInfo)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  // Modal-on-top trick: when the current location was reached with a
  // `backgroundLocation` in its state (i.e. the user clicked a card /
  // link via `openTaskModal` / `TaskLink`), we render the *previous*
  // location inside the main <Routes> (so the kanban stays mounted
  // and scroll/dnd state survive), and additionally render the modal
  // inside a second <Routes> that uses the live location.
  //
  // Direct deep-links to /tasks/:id don't carry `backgroundLocation`,
  // so the main <Routes> falls back to TaskViewPage (full-page view)
  // and the modal block is skipped.
  const location = useLocation()
  const state = (location.state ?? null) as BackgroundLocationState | null
  const backgroundLocation = state?.backgroundLocation
  const backgroundPathname = backgroundLocation?.pathname ?? null

  return (
    <>
      <Routes location={backgroundPathname ? { ...location, pathname: backgroundPathname } : location}>
        {/* Login has its own chrome (no sidebar / top bar). */}
        <Route path="/login" element={<LoginPage />} />

        {/* Authenticated app: sidebar + page content (Phase pre-11),
            with the Phase 11 nested project tabs. */}
        <Route element={<RequireAuth><AppLayout /></RequireAuth>}>
          <Route path="/" element={<Dashboard info={info} error={error} />} />
          <Route path="/inbox" element={<InboxPage />} />
          <Route path="/review" element={<ReviewPage />} />
          <Route path="/projects" element={<ProjectsPage />} />
          <Route path="/projects/:id" element={<ProjectDetailPage />}>
            <Route index element={<ProjectKanbanTab />} />
            <Route path="activity" element={<ProjectActivityTab />} />
            <Route path="attachments" element={<ProjectAttachmentsTab />} />
            <Route path="settings" element={<ProjectSettingsTab />} />
          </Route>
          <Route path="/agents" element={<AgentsPage />} />
          <Route path="/tasks/:id" element={<TaskViewPage />} />
          <Route path="/calendar" element={<CalendarPage />} />
          <Route path="/wiki/:slug?" element={<WikiPage />} />
          <Route path="/search" element={<SearchPage />} />
          <Route path="/reports" element={<ReportsPage />} />
          <Route path="/settings" element={<Placeholder title="Settings" />} />
          <Route path="/settings/backups" element={<BackupsSettingsPage />} />
          <Route path="/settings/bots" element={<BotsSettingsPage />} />
          <Route path="*" element={<Placeholder title="Not found" />} />
        </Route>
      </Routes>

      {/* Modal layer: only renders when the current navigation carries a
          `backgroundLocation`, i.e. the user opened the modal from
          another page (not via a direct deep-link). The modal route
          is intentionally NOT wrapped in <RequireAuth> — by the time
          we get here the user has already been authenticated on the
          background page, and TaskViewBody's own API calls surface
          401s gracefully if the session somehow expires mid-flight. */}
      {backgroundPathname && (
        <Routes>
          <Route path="/tasks/:id" element={<TaskModal />} />
        </Routes>
      )}

      {/* Floating timer widget is rendered globally so it persists across navigation. */}
      <TimerWidget />

      {/* Phase 21: global quick-capture modal. Hotkey 'q' or the
          bottom-right "+" button opens it. Always available while
          authenticated (mounted just inside the Shell so it stays
          after login). */}
      <QuickCapture />

      {/* Footer lives outside AppLayout so it shows on login too. */}
      <Footer info={info} />
    </>
  )
}

/**
 * Gate a route on authenticated status. Redirects to /login on miss,
 * preserving the requested path so the user lands back where they started.
 */
function RequireAuth({ children }: { children: ReactNode }): JSX.Element {
  const { status } = useAuth()
  // While /me is in flight, render a placeholder so react-router doesn't
  // bounce the user to /login (and then back to / from LoginPage) on every
  // hard reload. Only redirect once we *know* the session is anonymous.
  if (status === 'loading') {
    return (
      <div className="p-6 text-sm text-slate-500">Loading…</div>
    )
  }
  if (status !== 'authenticated') {
    return <Navigate to="/login" replace />
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
                  v ? 'bg-green-100 text-green-800' : 'bg-slate-100 text-slate-500'
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

function Footer({ info }: { info: InfoResponse | null }): JSX.Element {
  return (
    <footer className="border-t border-slate-200 dark:border-slate-800 text-xs text-slate-500 text-center py-3">
      Orenda {info?.version ?? '…'} · local-first productivity
    </footer>
  )
}
