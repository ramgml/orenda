/**
 * Weeek-style collapsible project sidebar.
 *
 * Layout (expanded):
 *   ┌─────────────────────────────────────┐
 *   │ Orenda              [user avatar]   │  ← SidebarHeader (logo + user)
 *   │ ─────────────────────────────────── │
 *   │ [+ New project]                      │  ← NewProjectInline
 *   │ ✉  Inbox [System]    0        (no ★) │  ← SidebarProjectItem (pinned, system)
 *   │ ──── Pinned (2) ──────────────────►  │
 *   │ •• Orenda          7 ★              │  ← SidebarProjectItem
 *   │ •• Site            0 ★              │
 *   │ ──── Active (4) ──────────────────►  │
 *   │ •• Blog            3                │
 *   │ •• Side-project    1                │
 *   │ ──── Archived (1) ────────────────►  │  (default-collapsed)
 *   │ ────────────────────────────────── │
 *   │ ⌂ Dashboard                         │  ← SidebarNav
 *   │ ▦ Calendar                           │
 *   │ ...                                  │
 *   │ ────────────────────────────────── │
 *   │ ⤢  Collapse sidebar                  │  ← SidebarToggle
 *   └─────────────────────────────────────┘
 *
 * The Inbox is treated as a system project: it always sits at the top
 * of the rail (regardless of alphabet order or pin state), cannot be
 * pinned/unpinned, and is hidden from the archive section if it ever
 * slipped through. See `INBOX_PROJECT_ID` in shared/constants.ts.
 *
 * Layout (collapsed): a ~64px column of colour dots + nav glyphs.
 * Hover any dot to see the project name as a tooltip.
 *
 * Mounted inside the authenticated <Shell> only — the `<RequireAuth>`
 * gate handles the unauthenticated case.
 */
import { useMemo } from 'react'
import { Link, useLocation, useParams } from 'react-router-dom'

import { useAuth } from '@/features/auth/AuthContext'
import { useProjects } from '@/shared/hooks/useProjects'
import { useOpenTaskCounts } from '@/shared/hooks/useOpenTaskCounts'
import { usePinnedProjects } from '@/shared/hooks/usePinnedProjects'

import { SidebarNav } from './SidebarNav'
import { SidebarProjectItem } from './SidebarProjectItem'
import { SidebarSection } from './SidebarSection'
import { NewProjectInline } from './NewProjectInline'
import { useSidebar } from './SidebarContext'
import { partitionProjects } from './partitionProjects'

const SIDEBAR_WIDTH = 'w-60'        // ~240px in expanded mode
const SIDEBAR_COLLAPSED = 'w-16'    // ~64px  in collapsed mode

export function ProjectSidebar(): JSX.Element {
  const { collapsed, toggle } = useSidebar()
  const { user } = useAuth()
  const { data: projects, isLoading } = useProjects()
  const [pinnedIds, { isPinned, toggle: togglePin }] = usePinnedProjects()
  const location = useLocation()
  const { id: routeProjectId } = useParams<{ id?: string }>()

  // Active detection: we're on the project page if path matches /projects/:id
  const activeProjectId = useMemo(() => {
    const m = location.pathname.match(/^\/projects\/([^/]+)/)
    if (m && m[1]) {
      // Prefer the route param over the regex when available — guards
      // against URL-encoding edge cases.
      return routeProjectId ?? m[1]
    }
    return undefined
  }, [location.pathname, routeProjectId])

  const allProjects = useMemo(() => projects ?? [], [projects])
  const projectIds = useMemo(() => allProjects.map((p) => p.id), [allProjects])
  const openCounts = useOpenTaskCounts(projectIds)

  const partition = useMemo(
    () => partitionProjects(allProjects, pinnedIds),
    [allProjects, pinnedIds],
  )

  // Tailwind class lookup is dynamic (md: vs base); we build the
  // container class string once per render to keep JSX clean.
  const containerWidth = collapsed ? SIDEBAR_COLLAPSED : SIDEBAR_WIDTH

  return (
    <aside
      data-collapsed={collapsed || undefined}
      className={`hidden md:flex flex-col shrink-0 ${containerWidth} transition-[width] duration-200 border-r border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900/40 h-full overflow-y-auto`}
      aria-label="Project navigation"
    >
      {/* Header: brand + user identity (hidden when there's no room). */}
      <div className={`flex items-center gap-2 px-3 h-12 border-b border-slate-200 dark:border-slate-800 ${collapsed ? 'justify-center' : ''}`}>
        <Link to="/" className="flex items-center gap-2 font-semibold text-lg">
          <span className="inline-block h-5 w-5 rounded bg-orenda-500" aria-hidden />
          {!collapsed && <span>Orenda</span>}
        </Link>
      </div>

      {/* Quick-create project button (only visible when expanded). */}
      {!collapsed && <NewProjectInline />}
      {collapsed && <NewProjectInline collapsed />}

      {/* Project sections: Inbox (system) / Pinned / Active / Archived. */}
      <div className="flex-1 pb-2">
        {isLoading && !projects ? (
          !collapsed && (
            <p className="px-4 py-6 text-xs text-slate-400">Loading projects…</p>
          )
        ) : (
          <>
            {partition.inbox && (
              // Inbox renders without a section header: it's a single
              // first-class project, not a "category". We give it a
              // fixed slot at the top so the user always knows where
              // calendar events land.
              <div className="px-2 pt-2">
                {!collapsed && (
                  <div className="px-2 pt-1 pb-1 text-[11px] uppercase tracking-wide text-slate-500 font-semibold">
                    Workspace
                  </div>
                )}
                <SidebarProjectItem
                  project={partition.inbox}
                  openTaskCount={openCounts.get(partition.inbox.id)}
                  active={partition.inbox.id === activeProjectId}
                  pinned={false}
                  isSystem
                  collapsed={collapsed}
                  onTogglePin={togglePin}
                />
              </div>
            )}

            {partition.pinned.length > 0 && (
              <SidebarSection label="Pinned" count={partition.pinned.length}>
                {partition.pinned.map((p) => (
                  <SidebarProjectItem
                    key={p.id}
                    project={p}
                    openTaskCount={openCounts.get(p.id)}
                    active={p.id === activeProjectId}
                    pinned={isPinned(p.id)}
                    collapsed={collapsed}
                    onTogglePin={togglePin}
                  />
                ))}
              </SidebarSection>
            )}

            {partition.active.length > 0 && (
              <SidebarSection label="Active" count={partition.active.length}>
                {partition.active.map((p) => (
                  <SidebarProjectItem
                    key={p.id}
                    project={p}
                    openTaskCount={openCounts.get(p.id)}
                    active={p.id === activeProjectId}
                    pinned={isPinned(p.id)}
                    collapsed={collapsed}
                    onTogglePin={togglePin}
                  />
                ))}
              </SidebarSection>
            )}

            {partition.archived.length > 0 && (
              <SidebarSection label="Archived" count={partition.archived.length} defaultCollapsed>
                {partition.archived.map((p) => (
                  <SidebarProjectItem
                    key={p.id}
                    project={p}
                    openTaskCount={openCounts.get(p.id)}
                    active={p.id === activeProjectId}
                    pinned={isPinned(p.id)}
                    collapsed={collapsed}
                    onTogglePin={togglePin}
                  />
                ))}
              </SidebarSection>
            )}

            {!collapsed && allProjects.length === 0 && (
              <p className="px-4 py-6 text-xs text-slate-400">
                No projects yet. Click <em>New project</em> above to create one.
              </p>
            )}
          </>
        )}

        <div className="my-2 mx-2 border-t border-slate-200 dark:border-slate-800" />

        <SidebarNav collapsed={collapsed} />
      </div>

      {/* Footer: collapse toggle + user email. */}
      <div className="border-t border-slate-200 dark:border-slate-800 px-2 py-2 flex items-center gap-2">
        <button
          type="button"
          onClick={toggle}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          className="h-7 w-7 rounded flex items-center justify-center text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-slate-200"
        >
          <span aria-hidden>{collapsed ? '»' : '«'}</span>
        </button>
        {!collapsed && user && (
          <span className="text-xs text-slate-400 truncate" title={user.email}>
            {user.email}
          </span>
        )}
      </div>
    </aside>
  )
}
