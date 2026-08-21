/**
 * Weeek-style collapsible project sidebar.
 *
 * Layout (expanded):
 *   ┌─────────────────────────────────────┐
 *   │ Orenda              [user avatar]   │  ← SidebarHeader (logo + user)
 *   │ ─────────────────────────────────── │
 *   │ [+ New project]                      │  ← NewProjectInline
 *   │ ✉  Inbox                7           │  ← static Inbox link (Phase 16)
 *   │ ──── Pinned (2) ──────────────────►  │
 *   │ •• Orenda          4 ★              │  ← SidebarProjectItem
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
 * Phase 16: the Inbox is no longer a project. It's rendered as a
 * static link with its own badge (count of inbox tasks) just below
 * the New Project button. Clicking it opens /inbox — the dedicated
 * page for unfiled cards. The well-known Inbox id no longer exists;
 * tasks in the Inbox are simply tasks with project_id IS NULL.
 *
 * Layout (collapsed): a ~64px column of colour dots + nav glyphs.
 * Hover any dot to see the project name as a tooltip.
 *
 * Mounted inside the authenticated <Shell> only — the `<RequireAuth>`
 * gate handles the unauthenticated case.
 */
import { useMemo } from 'react';
import { Link, useLocation, useParams } from 'react-router-dom';

import { useAuth } from '@/features/auth/AuthContext';
import { api } from '@/shared/api/client';
import { useProjects } from '@/shared/hooks/useProjects';
import { useOpenTaskCounts } from '@/shared/hooks/useOpenTaskCounts';
import { usePinnedProjects } from '@/shared/hooks/usePinnedProjects';
import { Button } from '@/shared/ui/button';

import { SidebarNav } from './SidebarNav';
import { SidebarProjectItem } from './SidebarProjectItem';
import { SidebarSection } from './SidebarSection';
import { NewProjectInline } from './NewProjectInline';
import { useSidebar } from './SidebarContext';
import { partitionProjects } from './partitionProjects';

const SIDEBAR_WIDTH = 'w-60'; // ~240px in expanded mode
const SIDEBAR_COLLAPSED = 'w-16'; // ~64px  in collapsed mode

export function ProjectSidebar(): JSX.Element {
  const { collapsed, toggle } = useSidebar();
  const { user } = useAuth();
  const { data: projects, isLoading } = useProjects();
  const [pinnedIds, { isPinned, toggle: togglePin }] = usePinnedProjects();
  const location = useLocation();
  const { id: routeProjectId } = useParams<{ id?: string }>();

  // Active detection: we're on the project page if path matches /projects/:id
  const activeProjectId = useMemo(() => {
    const m = location.pathname.match(/^\/projects\/([^/]+)/);
    if (m && m[1]) {
      // Prefer the route param over the regex when available — guards
      // against URL-encoding edge cases.
      return routeProjectId ?? m[1];
    }
    return undefined;
  }, [location.pathname, routeProjectId]);

  const allProjects = useMemo(() => projects ?? [], [projects]);
  const projectIds = useMemo(() => allProjects.map((p) => p.id), [allProjects]);
  const openCounts = useOpenTaskCounts(projectIds);

  const partition = useMemo(
    () => partitionProjects(allProjects, pinnedIds),
    [allProjects, pinnedIds],
  );

  // Inbox badge: number of open inbox tasks. The hook is per-project,
  // so we make a separate "all-inbox" count by fetching the inbox
  // list and counting locally. Cheap because the inbox is small in
  // practice and the hook is a tiny wrapper around useQuery.
  const inboxCount = useInboxOpenCount();

  // Tailwind class lookup is dynamic (md: vs base); we build the
  // container class string once per render to keep JSX clean.
  const containerWidth = collapsed ? SIDEBAR_COLLAPSED : SIDEBAR_WIDTH;

  return (
    <aside
      data-collapsed={collapsed || undefined}
      className={`hidden md:flex flex-col shrink-0 ${containerWidth} transition-[width] duration-200 border-r border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900/40 h-full overflow-y-auto`}
      aria-label="Project navigation"
    >
      {/* Header: brand + user identity (hidden when there's no room). */}
      <div
        className={`flex items-center gap-2 px-3 h-12 border-b border-slate-200 dark:border-slate-800 ${collapsed ? 'justify-center' : ''}`}
      >
        <Link to="/" className="flex items-center gap-2 font-semibold text-lg">
          <span className="inline-block h-5 w-5 rounded bg-orenda-500" aria-hidden />
          {!collapsed && <span>Orenda</span>}
        </Link>
      </div>

      {/* Quick-create project button (only visible when expanded). */}
      {!collapsed && <NewProjectInline />}
      {collapsed && <NewProjectInline collapsed />}

      {/* Static Inbox link (Phase 16). The Inbox isn't a project —
          it's a flat list served by /api/v1/inbox/tasks. The link
          uses the same SidebarProjectItem shell so the visual
          treatment matches the project rows below it (icon, count,
          active highlight). */}
      <div className="px-2 pt-2">
        <SidebarProjectItem
          project={{
            id: '__inbox__',
            name: 'Inbox',
            color: '#6b7280',
            owner_id: '',
            archived: false,
            created_at: '',
            updated_at: '',
          }}
          openTaskCount={inboxCount}
          active={location.pathname === '/inbox'}
          pinned={false}
          collapsed={collapsed}
          onTogglePin={togglePin}
          inboxLink
        />
      </div>

      {/* Project sections: Pinned / Active / Archived. */}
      <div className="flex-1 pb-2">
        {isLoading && !projects ? (
          !collapsed && <p className="px-4 py-6 text-xs text-slate-400">Loading projects…</p>
        ) : (
          <>
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
        <Button
          variant="ghost"
          size="icon"
          onClick={toggle}
          title={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
          className="h-7 w-7 text-slate-500 hover:text-slate-800 dark:hover:text-slate-200"
        >
          <span aria-hidden>{collapsed ? '»' : '«'}</span>
        </Button>
        {!collapsed && user && (
          <span className="text-xs text-slate-400 truncate" title={user.email}>
            {user.email}
          </span>
        )}
      </div>
    </aside>
  );
}

// useInboxOpenCount returns the number of inbox tasks whose status is
// not "done". Used by the sidebar badge. Stale-while-revalidate via
// react-query keeps the count snappy without re-fetching on every
// navigation — the inbox rarely changes relative to project views.
function useInboxOpenCount(): number | undefined {
  // We piggyback on a fetch of the inbox list; the page itself does
  // the same fetch when mounted, so when both are visible the second
  // call hits the react-query cache. The hook lives in the sidebar
  // because that's where the badge lives — moving it to InboxPage
  // would mean either duplicating the fetch or extracting a
  // dedicated hook.
  const [count, setCount] = useState<number | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const r = await api.listInboxTasks();
        if (cancelled) return;
        setCount((r.tasks ?? []).filter((t) => t.status !== 'done').length);
      } catch {
        // best-effort; leave the badge empty on error.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);
  return count;
}

// useState/useEffect imports — keep at the bottom so the module flow
// stays top-down.
import { useEffect, useState } from 'react';
