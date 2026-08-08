import { useEffect, useState } from 'react'

import { api, type BackupLogEntry, type BackupSettings, type BackupSnapshot } from '@/shared/api/client'

/**
 * /settings/backups — backup configuration + manual actions + history.
 *
 * Phase 7 shows the settings read-only (config.yaml is the source of
 * truth); Phase 9 adds a real "edit remote" form.
 */
export function BackupsSettingsPage(): JSX.Element {
  const [settings, setSettings] = useState<BackupSettings | null>(null)
  const [snapshots, setSnapshots] = useState<BackupSnapshot[]>([])
  const [log, setLog] = useState<BackupLogEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<'push' | 'snapshot' | null>(null)
  const [info, setInfo] = useState<string | null>(null)

  async function load(): Promise<void> {
    try {
      const [s, snaps, l] = await Promise.all([
        api.getBackupSettings(),
        api.listBackupSnapshots(),
        api.listBackupLog({ limit: 20 }),
      ])
      setSettings(s)
      setSnapshots(snaps.snapshots ?? [])
      setLog(l.log ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  useEffect(() => {
    load()
  }, [])

  async function onTestPush(): Promise<void> {
    setBusy('push')
    setInfo(null)
    setError(null)
    try {
      await api.testBackupPush()
      setInfo('Push succeeded.')
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      setError(msg.includes('no_remote') ? 'No git remote configured — edit data/config.yaml to add one.' : msg)
    } finally {
      setBusy(null)
    }
  }

  async function onSnapshot(): Promise<void> {
    setBusy('snapshot')
    setInfo(null)
    setError(null)
    try {
      const r = await api.createSnapshot()
      setInfo(`Snapshot written: ${r.path}`)
      await load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(null)
    }
  }

  return (
    <section className="space-y-4">
      <header>
        <h1 className="text-2xl font-semibold">Backups</h1>
        <p className="text-sm text-slate-500">
          Git push of the markdown mirror + scheduled sqlite snapshots.
        </p>
      </header>

      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}
      {info && (
        <div className="rounded border border-green-300 bg-green-50 text-green-800 px-3 py-2 text-sm">
          {info}
        </div>
      )}

      <div className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950">
        <h2 className="font-semibold mb-2">Settings</h2>
        {settings ? (
          <dl className="grid grid-cols-[140px,1fr] gap-y-1 text-sm">
            <dt className="text-slate-500">Enabled</dt>
            <dd>{settings.enabled ? 'yes' : 'no'}</dd>
            <dt className="text-slate-500">Remote URL</dt>
            <dd className="font-mono text-xs break-all">{settings.remote_url || '—'}</dd>
            <dt className="text-slate-500">Auth configured</dt>
            <dd>{settings.has_auth ? 'yes' : 'no'}</dd>
          </dl>
        ) : (
          <p className="text-slate-500 text-sm">Loading…</p>
        )}
        <p className="text-xs text-slate-500 mt-3">
          Settings live in <code className="px-1 bg-slate-100 dark:bg-slate-800 rounded">data/config.yaml</code>.
          Edit and restart to change the remote.
        </p>
      </div>

      <div className="flex gap-2">
        <button
          type="button"
          onClick={onTestPush}
          disabled={busy !== null}
          className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
        >
          {busy === 'push' ? 'Pushing…' : 'Test push now'}
        </button>
        <button
          type="button"
          onClick={onSnapshot}
          disabled={busy !== null}
          className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 text-sm"
        >
          {busy === 'snapshot' ? 'Snapshotting…' : 'Snapshot now'}
        </button>
      </div>

      <div className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950">
        <h2 className="font-semibold mb-2">Snapshots ({snapshots.length})</h2>
        {snapshots.length === 0 ? (
          <p className="text-slate-500 text-sm">No snapshots yet.</p>
        ) : (
          <ul className="text-sm space-y-1 font-mono">
            {snapshots.map((s) => (
              <li key={s.path}>
                <span className="text-slate-500">{new Date(s.mod_time).toLocaleString()}</span>{' '}
                <span className="text-slate-400">·</span>{' '}
                <span>{(s.size / 1024).toFixed(1)} KiB</span>{' '}
                <span className="text-slate-400">·</span>{' '}
                <span className="break-all">{s.path}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950">
        <h2 className="font-semibold mb-2">Log</h2>
        {log.length === 0 ? (
          <p className="text-slate-500 text-sm">No log entries yet.</p>
        ) : (
          <ul className="text-xs space-y-1">
            {log.map((l) => (
              <li key={l.id} className="flex gap-2">
                <span className="text-slate-400">{new Date(l.created_at).toLocaleString()}</span>
                <span className={`font-mono ${l.status === 'success' ? 'text-green-600' : 'text-red-600'}`}>
                  {l.status}
                </span>
                <span className="text-slate-500">{l.type}</span>
                <span className="truncate">{l.message}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  )
}