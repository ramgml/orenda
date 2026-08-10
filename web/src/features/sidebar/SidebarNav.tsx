/**
 * Primary navigation rail inside the sidebar.
 *
 * Mirrors the link set that used to live in the top header so we don't
 * lose access to global views like Calendar or Wiki. Active route is
 * highlighted via `useLocation`.
 */
import { NavLink } from 'react-router-dom'

interface NavEntry {
  to: string
  label: string
  /** Single unicode glyph used as an icon (no extra deps). */
  glyph: string
  /** Short matchers so children paths count as active too. */
  matchPrefix?: string
}

const NAV: NavEntry[] = [
  { to: '/', label: 'Dashboard', glyph: '◉' },
  { to: '/calendar', label: 'Calendar', glyph: '▦' },
  { to: '/wiki', label: 'Wiki', glyph: '✎', matchPrefix: '/wiki' },
  { to: '/agents', label: 'Agents', glyph: '◐' },
  { to: '/search', label: 'Search', glyph: '⌕' },
  { to: '/reports', label: 'Reports', glyph: '▤' },
  { to: '/settings', label: 'Settings', glyph: '⚙', matchPrefix: '/settings' },
]

export function SidebarNav({ collapsed }: { collapsed: boolean }): JSX.Element {
  if (collapsed) {
    return (
      <nav aria-label="Primary" className="space-y-0.5 px-1 pt-2">
        {NAV.map((entry) => (
          <NavLink
            key={entry.to}
            to={entry.to}
            end={entry.to === '/'}
            title={entry.label}
            aria-label={entry.label}
            className={({ isActive }) =>
              [
                'flex items-center justify-center h-9 rounded mx-1 text-base',
                isActive
                  ? 'bg-slate-100 dark:bg-slate-800 text-orenda-600 dark:text-orenda-400'
                  : 'text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-slate-200',
              ].join(' ')
            }
          >
            <span aria-hidden>{entry.glyph}</span>
          </NavLink>
        ))}
      </nav>
    )
  }

  return (
    <nav aria-label="Primary" className="space-y-0.5 pt-2">
      {NAV.map((entry) => {
        const prefix = entry.matchPrefix ?? entry.to
        // For paths ending in the root we want exact match; for nested
        // sections like /wiki or /settings we want a prefix match.
        const end = prefix === entry.to && entry.to === '/'
        return (
          <NavLink
            key={entry.to}
            to={entry.to}
            end={end}
            className={({ isActive }) => {
              const active = entry.to === '/' ? isActive : isActive
              return [
                'relative flex items-center gap-3 px-3 py-1.5 text-sm rounded mx-2',
                active
                  ? 'bg-slate-100 dark:bg-slate-800 text-orenda-600 dark:text-orenda-400'
                  : 'text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800',
              ].join(' ')
            }}
          >
            {({ isActive }) => {
              const active = entry.to === '/' ? isActive : isActive
              return (
                <>
                  <span
                    aria-hidden
                    className={`absolute left-0 top-1 bottom-1 w-[3px] rounded-r ${
                      active ? 'bg-orenda-500' : 'bg-transparent'
                    }`}
                  />
                  <span aria-hidden className="text-base leading-none">
                    {entry.glyph}
                  </span>
                  <span>{entry.label}</span>
                </>
              )
            }}
          </NavLink>
        )
      })}
    </nav>
  )
}
