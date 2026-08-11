import { ReactNode, useEffect, useState } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'

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
import { TodayPage } from '@/features/today/TodayPage'
import { CoursesPage } from '@/features/courses/CoursesPage'
import { CourseDetailPage } from '@/features/courses/CourseDetailPage'
import { api, type InfoResponse } from '@/shared/api/client'

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

  useEffect(() => {
    api
      .info()
      .then(setInfo)
      .catch(() => {
        /* info stays null; Footer renders '…' for the version */
      })
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
          <Route path="/" element={<TodayPage />} />
          <Route path="/inbox" element={<InboxPage />} />
          <Route path="/review" element={<ReviewPage />} />
          <Route path="/courses" element={<CoursesPage />} />
          <Route path="/courses/:id" element={<CourseDetailPage />} />
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

// Phase 20: Dashboard was the home route; replaced by TodayPage
// (overdue / due-today / scheduled / awaiting in one screen). The
// old Stats component has been removed; the same data is reachable
// through Reports + the project sidebar.

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
