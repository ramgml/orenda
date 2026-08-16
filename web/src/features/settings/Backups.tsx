import { useEffect, useState } from 'react';

import {
  api,
  type BackupLogEntry,
  type BackupSettings,
  type BackupSnapshot,
} from '@/shared/api/client';

/**
 * /settings/backups — backup configuration + manual actions + history.
 *
 * Phase 7 shows the settings read-only (config.yaml is the source of
 * truth); Phase 9 adds a real "edit remote" form; Phase 22 closes
 * the restore loop with an in-process button (maintenance mode +
 * force=true) so the operator doesn't have to ssh to the host and
 * run the CLI command by hand.
 */
export function BackupsSettingsPage(): JSX.Element {
  const [settings, setSettings] = useState<BackupSettings | null>(null);
  const [snapshots, setSnapshots] = useState<BackupSnapshot[]>([]);
  const [log, setLog] = useState<BackupLogEntry[]>([]);
  // Phase 30.9: read-only status from GET /api/v1/backups/status —
  // snapshot count + latest snapshot path/size. Safe to poll.
  const [status, setStatus] = useState<{
    scheduler_disabled: boolean;
    snapshot_count?: number;
    latest_snapshot?: string;
    latest_snapshot_size?: number;
    latest_snapshot_unix?: number;
    snapshot_error?: string;
  } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'push' | 'snapshot' | 'restore' | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [restoreTarget, setRestoreTarget] = useState<BackupSnapshot | null>(null);
  const [restoreHint, setRestoreHint] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  // Phase 28.1 polish.1: editable settings form state. The form
  // fields are initialized from `settings` once it loads; the user
  // types into `formEnabled` / `formRemoteUrl` / `formRemoteAuth`
  // and Save posts all three back. Keeping them as separate state
  // (not just `settings`) lets the user edit freely without
  // round-tripping every keystroke.
  const [formEnabled, setFormEnabled] = useState(false);
  const [formRemoteUrl, setFormRemoteUrl] = useState('');
  const [formRemoteAuth, setFormRemoteAuth] = useState('');
  const [savingSettings, setSavingSettings] = useState(false);

  // We only want the form to mirror the server state on the *initial*
  // load — once the operator starts typing, the form state is theirs
  // until they hit Save (or refresh the page). We track this with a
  // `formInitialized` flag.
  const [formInitialized, setFormInitialized] = useState(false);

  async function load(): Promise<void> {
    try {
      const [s, snaps, l, status] = await Promise.all([
        api.getBackupSettings(),
        api.listBackupSnapshots(),
        api.listBackupLog({ limit: 20 }),
        api.getBackupStatus(),
      ]);
      setSettings(s);
      setSnapshots(snaps.snapshots ?? []);
      setLog(l.log ?? []);
      setStatus(status);
      setError(null);
      if (!formInitialized) {
        setFormEnabled(s.enabled);
        setFormRemoteUrl(s.remote_url);
        // Don't pre-fill the auth field — it's a secret, the
        // backend never returns it.
        setFormRemoteAuth('');
        setFormInitialized(true);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function onTestPush(): Promise<void> {
    setBusy('push');
    setInfo(null);
    setError(null);
    try {
      await api.testBackupPush();
      setInfo('Push succeeded.');
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(
        msg.includes('no_remote')
          ? 'No git remote configured — edit data/config.yaml to add one.'
          : msg,
      );
    } finally {
      setBusy(null);
    }
  }

  async function onSnapshot(): Promise<void> {
    setBusy('snapshot');
    setInfo(null);
    setError(null);
    try {
      const r = await api.createSnapshot();
      setInfo(`Snapshot written: ${r.path}`);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  // Initial click on Restore… — opens the modal in CLI-hint mode.
  // The operator can either copy the CLI command or click the
  // "Restore in this window" button to drive the in-process path.
  async function onRestore(s: BackupSnapshot): Promise<void> {
    setError(null);
    setInfo(null);
    setRestoreTarget(s);
    setRestoreHint(null);
    try {
      // Probe the server with `force: false` — most installs return
      // 409 with a CLI hint that we can show as the "manual path".
      // If the operator has already turned maintenance on (or the
      // server happens to be off for some reason) the call may
      // succeed and we still surface the CLI hint as a fallback.
      const r = await api.restoreBackup(s.path, { force: false });
      if (r.hint) setRestoreHint(r.hint);
    } catch (e) {
      // axios throws for non-2xx. The server's structured body
      // carries the CLI hint; recover it from the error message.
      const msg = e instanceof Error ? e.message : String(e);
      const hintMatch = /hint[":\s]+([^\n}]+)/i.exec(msg);
      setRestoreHint(
        hintMatch
          ? hintMatch[1].replace(/^[":\s]+/, '').replace(/[",]+$/, '')
          : `Run on the server host: orenda backup restore --from ${s.path} --yes`,
      );
    }
  }

  // The in-process restore path (Phase 22.3). The UI drives the
  // three-step sequence — maintenance on, restore with force, reload
  // — without the operator touching the CLI. The server stays in
  // maintenance after success so the operator can verify the
  // restored data before exiting.
  async function onRestoreInline(s: BackupSnapshot): Promise<void> {
    setBusy('restore');
    setError(null);
    setInfo(null);
    try {
      await api.maintenanceOn();
      // The restore call is the only one that returns 200 on the
      // happy path; any error here means the restore failed (the
      // server exited maintenance for us, so the next reload will
      // see the live DB).
      await api.restoreBackup(s.path, { force: true });
      // Maintenance stays on after a successful restore. Show the
      // operator the next-step hint and let them decide when to
      // reload + exit.
      setInfo('Restore complete. Reloading in 1.5s — maintenance stays on until you exit it.');
      setRestoreTarget(null);
      // Reload the SPA so all in-flight fetches pick up the
      // restored data (and the maintenance banner shows).
      window.setTimeout(() => {
        window.location.reload();
      }, 1500);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setError(`Restore failed: ${msg}`);
      // Best-effort: ensure we don't leave maintenance stuck on.
      try {
        await api.maintenanceOff();
      } catch {
        // ignore — the operator can flip it from the CLI
      }
    } finally {
      setBusy(null);
    }
  }

  function closeRestore(): void {
    setRestoreTarget(null);
    setRestoreHint(null);
    setCopied(false);
  }

  async function copyHint(): Promise<void> {
    if (!restoreHint) return;
    try {
      await navigator.clipboard.writeText(restoreHint);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
    }
  }

  // Editable settings form (Phase 28.1 polish.1). Pre-28.1 the
  // settings panel was a `<dl>` — operators had to ssh to the host
  // and edit config.yaml to add a remote. Now the same panel
  // doubles as a Save form; the running `*backup.Service` doesn't
  // pick up the change (it was wired at startup), but the next
  // process reload does. The source_hint banner makes that visible.
  function onSaveSettings(): void {
    if (!settings) return;
    setSavingSettings(true);
    setError(null);
    setInfo(null);
    api
      .setBackupSettings({
        enabled: formEnabled,
        remote_url: formRemoteUrl,
        remote_auth: formRemoteAuth,
      })
      .then((fresh) => {
        setSettings(fresh);
        // Phase 28.9: the live service mirrors the new settings
        // via atomic.Pointer[Config]; the next push tick (or the
        // manual "Test push now" button below) hits the new URL
        // without restarting the process. Update the banner to
        // reflect that.
        setInfo('Settings saved. The next push will use the new remote.');
        // Clear the auth field — we don't want to keep the secret
        // in component state after a save.
        setFormRemoteAuth('');
      })
      .catch((e) => {
        const msg = e instanceof Error ? e.message : String(e);
        setError(`Save failed: ${msg}`);
      })
      .finally(() => setSavingSettings(false));
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

      <div className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950 space-y-3">
        <h2 className="font-semibold">Settings</h2>
        {settings ? (
          <div className="space-y-3 text-sm">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                data-testid="settings-enabled"
                checked={formEnabled}
                onChange={(e) => setFormEnabled(e.target.checked)}
              />
              <span>Backup enabled</span>
            </label>
            <label className="block">
              <span className="text-xs text-slate-500">Remote URL</span>
              <input
                type="url"
                data-testid="settings-remote-url"
                value={formRemoteUrl}
                onChange={(e) => setFormRemoteUrl(e.target.value)}
                placeholder="https://github.com/me/orenda.git"
                className="w-full mt-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-sm font-mono"
              />
            </label>
            <label className="block">
              <span className="text-xs text-slate-500">Remote auth (token)</span>
              <input
                type="password"
                data-testid="settings-remote-auth"
                value={formRemoteAuth}
                onChange={(e) => setFormRemoteAuth(e.target.value)}
                placeholder={
                  settings.has_auth ? '•••• (configured — leave blank to keep)' : 'optional'
                }
                autoComplete="off"
                className="w-full mt-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-sm font-mono"
              />
            </label>
            <div className="flex gap-2 items-center">
              <button
                type="button"
                data-testid="settings-save"
                onClick={onSaveSettings}
                disabled={savingSettings}
                className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
              >
                {savingSettings ? 'Saving…' : 'Save settings'}
              </button>
              {/* Phase 28.9: removed the historic restart banner —
                  the live service mirrors the new settings via
                  atomic.Pointer[Config] without a process restart.
                  The source_hint field stays in the response shape
                  for backward compat but is empty after this phase,
                  so no banner is rendered. The E2E spec
                  (`backups-settings.spec.ts`) asserts the
                  data-testid is gone to pin the contract. */}
            </div>
          </div>
        ) : (
          <p className="text-slate-500 text-sm">Loading…</p>
        )}
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
        {/* Phase 30.9: status line — count + latest snapshot timestamp
            without having to scroll the snapshot list. Pulled from
            GET /api/v1/backups/status (read-only). */}
        {status && !status.scheduler_disabled && (
          <p className="text-xs text-slate-500 mb-2">
            {status.snapshot_count ?? 0} snapshot
            {(status.snapshot_count ?? 0) === 1 ? '' : 's'} on disk
            {status.latest_snapshot && (
              <>
                {' '}· latest{' '}
                <span className="font-mono">
                  {status.latest_snapshot_unix
                    ? new Date(status.latest_snapshot_unix * 1000).toLocaleString()
                    : status.latest_snapshot}
                </span>
              </>
            )}
          </p>
        )}
        {snapshots.length === 0 ? (
          <p className="text-slate-500 text-sm">No snapshots yet.</p>
        ) : (
          <ul className="text-sm space-y-1 font-mono">
            {snapshots.map((s) => (
              <li key={s.path} className="flex items-center gap-2">
                <span className="text-slate-500">{new Date(s.mod_time).toLocaleString()}</span>
                <span className="text-slate-400">·</span>
                <span>{(s.size / 1024).toFixed(1)} KiB</span>
                <span className="text-slate-400">·</span>
                <span className="break-all flex-1">{s.path}</span>
                <button
                  type="button"
                  onClick={() => onRestore(s)}
                  className="ml-2 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 text-xs hover:bg-slate-100 dark:hover:bg-slate-800"
                >
                  Restore…
                </button>
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
                <span
                  className={`font-mono ${l.status === 'success' ? 'text-green-600' : 'text-red-600'}`}
                >
                  {l.status}
                </span>
                <span className="text-slate-500">{l.type}</span>
                <span className="truncate">{l.message}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      {restoreTarget && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-slate-900 rounded-lg shadow-xl max-w-lg w-full p-5 space-y-3">
            <h3 className="font-semibold text-lg">Restore from snapshot</h3>
            <p className="text-sm text-slate-600 dark:text-slate-300">
              Snapshot:{' '}
              <code className="px-1 bg-slate-100 dark:bg-slate-800 rounded text-xs break-all">
                {restoreTarget.path}
              </code>
            </p>
            <p className="text-sm">Orenda holds the live database open. Two ways to restore:</p>
            <ul className="text-sm space-y-1 list-disc pl-5">
              <li>
                <strong>In this window</strong> — flips the server into maintenance mode, runs the
                atomic swap, reloads. Maintenance stays on after success so you can verify the data
                before exiting.
              </li>
              <li>
                <strong>From the host CLI</strong> — stop the server, run the command below.
              </li>
            </ul>
            <div className="relative">
              <pre
                data-testid="restore-cli-hint"
                className="bg-slate-100 dark:bg-slate-800 rounded p-3 text-xs overflow-x-auto whitespace-pre-wrap break-all"
              >
                {restoreHint ?? '…'}
              </pre>
              <button
                type="button"
                onClick={copyHint}
                className="absolute top-2 right-2 px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-xs"
              >
                {copied ? 'Copied' : 'Copy'}
              </button>
            </div>
            <div className="flex justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={closeRestore}
                disabled={busy === 'restore'}
                className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 text-sm disabled:opacity-50"
              >
                Close
              </button>
              <button
                type="button"
                data-testid="restore-inline"
                onClick={() => void onRestoreInline(restoreTarget)}
                disabled={busy === 'restore'}
                className="px-3 py-2 rounded bg-amber-600 hover:bg-amber-700 disabled:opacity-50 text-white text-sm"
              >
                {busy === 'restore' ? 'Restoring…' : 'Restore in this window'}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
