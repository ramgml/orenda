/**
 * Primary navigation rail inside the sidebar.
 *
 * Mirrors the link set that used to live in the top header so we don't
 * lose access to global views like Calendar or Wiki. Active route is
 * highlighted via `useLocation`.
 */
import { useCallback, useEffect, useState } from 'react';
import { NavLink } from 'react-router-dom';

import { api } from '@/shared/api/client';
import { useWebSocketTopic } from '@/shared/ws';

interface NavEntry {
  to: string;
  label: string;
  /** Single unicode glyph used as an icon (no extra deps). */
  glyph: string;
  /** Short matchers so children paths count as active too. */
  matchPrefix?: string;
  /** Phase 19: which entry carries a live badge. */
  badge?: 'review';
}

const NAV: NavEntry[] = [
  { to: '/', label: 'Dashboard', glyph: '◉' },
  { to: '/calendar', label: 'Calendar', glyph: '▦' },
  { to: '/wiki', label: 'Wiki', glyph: '✎', matchPrefix: '/wiki' },
  { to: '/agents', label: 'Agents', glyph: '◐' },
  { to: '/search', label: 'Search', glyph: '⌕' },
  // Phase 19: Review queue lives at the top of the rail because it's
  // the daily-driver action surface — agents submit work, humans
  // resolve it. The badge drives the visibility.
  { to: '/review', label: 'Review', glyph: '✓', badge: 'review' },
  // Phase 18: courses (LMS) — second-life activity after tasks.
  { to: '/courses', label: 'Courses', glyph: '🎓', matchPrefix: '/courses' },
  { to: '/reports', label: 'Reports', glyph: '▤' },
  { to: '/settings', label: 'Settings', glyph: '⚙', matchPrefix: '/settings' },
];

export function SidebarNav({ collapsed }: { collapsed: boolean }): JSX.Element {
  return (
    <nav aria-label="Primary" className="space-y-0.5 pt-2">
      {NAV.map((entry) => (
        <SidebarNavItem key={entry.to} entry={entry} collapsed={collapsed} />
      ))}
    </nav>
  );
}

/**
 * One row inside the primary nav. Phase 19 adds the live review-queue
 * badge: a small numeric chip that updates via a cheap count endpoint
 * + WS subscription (any task event may move the queue size).
 */
function SidebarNavItem({
  entry,
  collapsed,
}: {
  entry: NavEntry;
  collapsed: boolean;
}): JSX.Element {
  // Phase 19: badge for the review queue. Loaded once on mount + on
  // every WS "tasks" event (cheap endpoint, the badge lives at the
  // top of the rail where it has to be current).
  const reviewCount = useReviewBadgeCount();
  const badge = entry.badge === 'review' && reviewCount && reviewCount > 0 ? reviewCount : null;

  if (collapsed) {
    return (
      <NavLink
        to={entry.to}
        end={entry.to === '/'}
        title={entry.label}
        aria-label={entry.label}
        className={({ isActive }) =>
          [
            'relative flex items-center justify-center h-9 rounded mx-1 text-base',
            isActive
              ? 'bg-slate-100 dark:bg-slate-800 text-orenda-600 dark:text-orenda-400'
              : 'text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-800 dark:hover:text-slate-200',
          ].join(' ')
        }
      >
        <span aria-hidden>{entry.glyph}</span>
        {badge !== null && (
          <span
            aria-hidden
            className="absolute top-1 right-1 min-w-[16px] h-4 rounded-full bg-orenda-600 text-white text-[10px] font-medium px-1 flex items-center justify-center"
          >
            {badge}
          </span>
        )}
      </NavLink>
    );
  }

  return (
    <NavLink
      to={entry.to}
      end={entry.to === '/'}
      className={({ isActive }) => {
        const active = entry.to === '/' ? isActive : isActive;
        return [
          'relative flex items-center gap-3 px-3 py-1.5 text-sm rounded mx-2',
          active
            ? 'bg-slate-100 dark:bg-slate-800 text-orenda-600 dark:text-orenda-400'
            : 'text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800',
        ].join(' ');
      }}
    >
      {({ isActive }) => {
        const active = entry.to === '/' ? isActive : isActive;
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
            <span className="flex-1">{entry.label}</span>
            {badge !== null && (
              <span
                aria-hidden
                data-testid="review-badge"
                className="min-w-[18px] h-[18px] rounded-full bg-orenda-600 text-white text-[10px] font-medium px-1.5 flex items-center justify-center"
              >
                {badge}
              </span>
            )}
          </>
        );
      }}
    </NavLink>
  );
}

/**
 * Hook: live count of tasks awaiting human review.
 *
 * Polls /api/v1/review-queue/count on mount + on every "tasks" WS
 * event. Returns undefined until the first fetch resolves so the UI
 * doesn't show "0" pre-load (which would otherwise flash a "no
 * notifications" state).
 */
function useReviewBadgeCount(): number | undefined {
  const [count, setCount] = useState<number | undefined>(undefined);
  const refresh = useCallback(async (): Promise<void> => {
    try {
      const r = await api.getReviewQueueCount();
      setCount(r.count);
    } catch {
      // best-effort: leave the previous value on error.
    }
  }, []);
  useEffect(() => {
    void refresh();
  }, [refresh]);
  useWebSocketTopic('tasks', () => {
    void refresh();
  });
  return count;
}
