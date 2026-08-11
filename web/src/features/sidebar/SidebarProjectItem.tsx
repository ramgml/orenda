/**
 * One row in the sidebar's project list.
 *
 * Visuals:
 *  - 3px-wide colour strip on the left mirrors the project `color`
 *    property, doubling as an "active page" indicator when it lines
 *    up with the page's `border-l`.
 *  - 8px dot before the name for at-a-glance ID when collapsed.
 *  - Open-task count badge to the right; hidden while loading (renders
 *    a dimmed "…" placeholder).
 *  - Pin star on hover (always visible when actively pinned) — uses
 *    a unicode glyph to avoid pulling in an icon library.
 *  - `isSystem` projects (currently only the Inbox) get a small
 *    "System" badge next to their name and skip the pin button.
 *
 * Accessibility: link element gets the project name as its accessible
 * label, and the pin button announces via title attribute.
 */
import { Link } from 'react-router-dom'

import type { Project } from '@/shared/api/client'

interface SidebarProjectItemProps {
  project: Project
  /** Open task count; undefined while still loading. */
  openTaskCount: number | undefined
  active: boolean
  pinned: boolean
  collapsed: boolean
  /**
   * Phase 16: the item is a synthetic "Inbox" row that links to
   * /inbox (not /projects/:id). When true we swap the link target
   * and skip the pin button (the Inbox is always first; pinning it
   * would be redundant).
   */
  inboxLink?: boolean
  /**
   * Kept for backward compat with the old "system project" badge;
   * no longer used because Phase 16 dropped the system Inbox project.
   * Ignored — the item still works without it.
   */
  isSystem?: boolean
  onTogglePin: (projectId: string) => void
}

export function SidebarProjectItem({
  project,
  openTaskCount,
  active,
  pinned,
  collapsed,
  inboxLink = false,
  isSystem = false,
  onTogglePin,
}: SidebarProjectItemProps): JSX.Element {
  // Where does the row link to? Inbox rows go to /inbox, everything
  // else goes to /projects/:id.
  const linkTarget = inboxLink ? '/inbox' : `/projects/${project.id}`
  // Inbox rows can't be pinned — they're a single first-class rail
  // item, not a project you might or might not want to surface.
  const showPin = !inboxLink && !isSystem

  // When the sidebar is collapsed we render a tiny icon-only tile that
  // links straight to the project. The dot carries the project's
  // colour, and the project title becomes a native tooltip.
  if (collapsed) {
    return (
      <Link
        to={linkTarget}
        title={project.name}
        aria-label={project.name}
        className={`flex items-center justify-center h-9 rounded mx-1 ${
          active
            ? 'bg-orenda-100 dark:bg-orenda-900/40'
            : 'hover:bg-slate-100 dark:hover:bg-slate-800'
        }`}
      >
        <span
          aria-hidden
          className="inline-block h-4 w-4 rounded"
          style={{ backgroundColor: project.color }}
        />
      </Link>
    )
  }

  return (
    <div
      className={`group relative flex items-center gap-2 rounded px-2 py-1.5 text-sm ${
        active
          ? 'bg-slate-100 dark:bg-slate-800 text-slate-900 dark:text-slate-50'
          : 'text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800'
      }`}
    >
      {/* Left accent strip — projects feel like a navigation rail. */}
      <span
        aria-hidden
        className={`absolute left-0 top-1 bottom-1 w-[3px] rounded-r ${
          active ? 'bg-orenda-500' : 'bg-transparent'
        }`}
      />
      <Link
        to={linkTarget}
        className="flex items-center gap-2 flex-1 min-w-0"
        aria-current={active ? 'page' : undefined}
      >
        <span
          aria-hidden
          className="inline-block h-2.5 w-2.5 rounded shrink-0"
          style={{ backgroundColor: project.color }}
        />
        <span className="truncate">{project.name}</span>
        {isSystem && (
          <span
            className="text-[9px] uppercase tracking-wide px-1 py-0.5 rounded bg-slate-200 dark:bg-slate-700 text-slate-600 dark:text-slate-300 shrink-0"
            title="System project — always present"
          >
            System
          </span>
        )}
      </Link>

      <span
        className="text-[11px] tabular-nums text-slate-400 dark:text-slate-500 min-w-[1.25rem] text-right shrink-0"
        title={`${openTaskCount ?? 0} open ${inboxLink ? 'inbox tasks' : 'tasks'}`}
        aria-label={`${openTaskCount ?? 0} open ${inboxLink ? 'inbox tasks' : 'tasks'}`}
      >
        {openTaskCount === undefined ? '…' : openTaskCount > 99 ? '99+' : openTaskCount}
      </span>

      {showPin && (
        <button
          type="button"
          onClick={(e) => {
            // The link above is the more likely click target; suppress
            // navigation when the user explicitly pins/unpins.
            e.preventDefault()
            e.stopPropagation()
            onTogglePin(project.id)
          }}
          title={pinned ? 'Unpin from sidebar' : 'Pin to top of sidebar'}
          aria-label={pinned ? 'Unpin project' : 'Pin project'}
          aria-pressed={pinned}
          className={`shrink-0 h-5 w-5 flex items-center justify-center rounded text-xs transition ${
            pinned
              ? 'text-orenda-600 dark:text-orenda-400 opacity-100'
              : 'text-slate-300 dark:text-slate-600 opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-slate-500'
          }`}
        >
          ★
        </button>
      )}
    </div>
  )
}
