import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { api, type StatsResponse } from '@/shared/api/client'

/**
 * /settings — hub page for everything in the Settings tree.
 *
 * Phase 28.2 (polish): before this page existed, /settings rendered
 * a `<Placeholder title="Settings" />` so the only way to reach
 * Backups / Bots was to know the URL. This hub lists every reachable
 * settings surface as a clickable card; the footer "About" block
 * pulls version + uptime from the existing /api/v1/info and
 * /api/v1/stats endpoints.
 *
 * Design notes:
 *  - No navigation owns this page — cards are <Link>s, not buttons
 *    with imperative nav. The sidebar ⚙ Settings entry already
 *    routes here.
 *  - Sub-pages (Backups, Bots) live at /settings/backups,
 *    /settings/bots. The sidebar `matchPrefix: '/settings'` keeps
 *    the ⚙ glyph highlighted while the user is anywhere under
 *    /settings/*.
 *  - Agents and Reports aren't strictly under /settings/* but are
 *    "operator surfaces" — exposing them here is a one-click
 *    shortcut from the index. Listing them at the top keeps the
 *    hub honest about what the owner can tweak.
 *  - Theme is in the topbar (not a page) — see the note below the
 *    cards.
 *  - Capabilities (websocket, bots, fts, …) are surfaced in About
 *    so the owner can sanity-check what the server actually has
 *    enabled without going to the CLI.
 */

interface CardSpec {
  to: string
  title: string
  description: string
  // Single unicode glyph (no icon library — convention in the
  // sidebar: ◉ ▦ ✎ ◐ ⌕ ✓ 🎓 ▤ ⚙).
  glyph: string
  /** data-testid hook for the E2E spec. */
  testId: string
}

const CARDS: CardSpec[] = [
  {
    to: '/settings/backups',
    title: 'Backups',
    description: 'Git remote, push schedule, snapshots, restore.',
    glyph: '⤓',
    testId: 'settings-card-backups',
  },
  {
    to: '/settings/bots',
    title: 'Bots & notifications',
    description: 'Telegram / VK / Email / Webhook subscriptions and the bind flow.',
    glyph: '✉',
    testId: 'settings-card-bots',
  },
  {
    to: '/agents',
    title: 'Agents',
    description: 'API tokens, online status, activity history.',
    glyph: '◐',
    testId: 'settings-card-agents',
  },
  {
    to: '/reports',
    title: 'Reports',
    description: 'Time aggregation per task over a window.',
    glyph: '▤',
    testId: 'settings-card-reports',
  },
]

/**
 * Format seconds as "Xd Yh" / "Xh Ym" / "Xm Ys" — the in-process
 * uptime is what /api/v1/stats returns, nothing fancy.
 */
function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

/** Format bytes as "1.2 MiB" / "456 KiB" / "789 B". The Settings
 *  About block uses Kibibytes to match the operator's mental model
 *  (Glances / `du -h` style). */
function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GiB`
}

/** Group thousands with a comma. We deliberately avoid
 *  `Number.prototype.toLocaleString` here because the host ICU
 *  data varies between environments (a CI runner with a C-locale
 *  produces a non-breaking space, which is invisible in test
 *  output and breaks the snapshot). Locale-aware rendering is
 *  plausible later but isn't worth the test flake today. */
function formatCount(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '—'
  return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

/** "—" until the network call resolves, then a real value. We
 *  mount a placeholder rather than throwing on stats failure —
 *  stats is best-effort. */
export function SettingsHome(): JSX.Element {
  const [stats, setStats] = useState<StatsResponse | null>(null)

  useEffect(() => {
    let cancelled = false
    api
      .getStats()
      .then((s) => {
        if (!cancelled) setStats(s)
      })
      .catch(() => {
        // best-effort: leave stats null and the About block renders
        // "—" for the fields that come from /api/v1/stats.
      })
    return () => {
      cancelled = true
    }
  }, [])

  // The version is already loaded by the Shell (`info` is fetched
  // on mount) and rendered in the footer. We don't need a second
  // fetch here; the Footer duplication is fine for a single binary.
  // The About block intentionally shows *capabilities* + *stats*
  // — the version lives in the footer where it's always visible.

  return (
    <section className="space-y-6" data-testid="settings-home">
      <header>
        <h1 className="text-2xl font-semibold">Settings</h1>
        <p className="text-sm text-slate-500">
          Everything you can tweak as the owner. Theme lives in the top bar.
        </p>
      </header>

      <div
        className="grid gap-3 sm:grid-cols-2"
        data-testid="settings-cards"
      >
        {CARDS.map((card) => (
          <Link
            key={card.to}
            to={card.to}
            data-testid={card.testId}
            className="block rounded-lg border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950 hover:border-orenda-500 hover:shadow-sm transition-colors"
          >
            <div className="flex items-start gap-3">
              <span
                aria-hidden
                className="text-orenda-500 text-xl leading-none mt-0.5"
              >
                {card.glyph}
              </span>
              <div className="flex-1 min-w-0">
                <h2 className="font-semibold text-slate-800 dark:text-slate-100">
                  {card.title}
                </h2>
                <p className="text-sm text-slate-500 mt-1">{card.description}</p>
              </div>
            </div>
          </Link>
        ))}
      </div>

      <div
        className="rounded-lg border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950"
        data-testid="settings-about"
      >
        <h2 className="font-semibold mb-2">About</h2>
        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2 text-sm">
          <div className="flex justify-between gap-2">
            <dt className="text-slate-500">Uptime</dt>
            <dd className="font-mono" data-testid="about-uptime">
              {stats ? formatUptime(stats.uptime_seconds) : '—'}
            </dd>
          </div>
          <div className="flex justify-between gap-2">
            <dt className="text-slate-500">Database</dt>
            <dd className="font-mono truncate" data-testid="about-db">
              {stats ? formatBytes(stats.db_bytes) : '—'}
            </dd>
          </div>
          <div className="flex justify-between gap-2">
            <dt className="text-slate-500">Requests served</dt>
            <dd className="font-mono" data-testid="about-requests">
              {stats ? formatCount(stats.requests_total) : '—'}
            </dd>
          </div>
          <div className="flex justify-between gap-2">
            <dt className="text-slate-500">WS clients</dt>
            <dd className="font-mono" data-testid="about-ws">
              {stats ? stats.ws_connections : '—'}
            </dd>
          </div>
        </dl>
      </div>
    </section>
  )
}
