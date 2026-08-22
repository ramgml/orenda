import { useEffect, useState } from 'react';

import {
  api,
  type BackupLogEntry,
  type BackupSettings,
  type BackupSnapshot,
} from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Checkbox } from '@/shared/ui/checkbox';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/shared/ui/dialog';
import { Input } from '@/shared/ui/input';

/**
 * /settings/backups — backup configuration + manual actions + history.
 *
 * Phase 7 shows the settings read-only (config.yaml is the source of
 * truth); Phase 9 adds a real "edit remote" form; Phase 22 closes
 * the restore loop with an in-process button (maintenance mode +
 * force=true) so the operator doesn't have to ssh to the host and
 * run the CLI command by hand. Phase 32.7 adds the cron schedule +
 * rotation days fields to the same Save form, hot-reloaded by the
 * server without a restart.
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
  // Phase 32.7: schedule + rotation form fields. The server
  // validates the cron expr at PUT time; the UI doesn't try to
  // parse client-side — surfacing the server's 400 is enough.
  const [formSnapshotCron, setFormSnapshotCron] = useState('');
  const [formRotationDays, setFormRotationDays] = useState(30);
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
        // Phase 32.7: pre-fill schedule + rotation from the
        // server's merge (DB > in-memory default). Empty cron
        // means "operator never set one" — pre-fill with the
        // hard-coded default so the form has something visible.
        setFormSnapshotCron(s.snapshot_cron || '0 3 * * *');
        setFormRotationDays(s.snapshot_rotation_days);
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

  // Editable settings form (Phase 28.1 polish.1 + Phase 32.7).
  // Pre-28.1 the settings panel was a `<dl>` — operators had to
  // ssh to the host and edit config.yaml to add a remote. Now the
  // same panel doubles as a Save form. Phase 28.9 made the
  // remote/url/auth trio hot-reload; Phase 32.7 extends the same
  // hot-reload contract to the cron schedule and rotation days.
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
        // Phase 32.7: pass the new fields through. The server
        // validates the cron expr (5-field UTC, no @macros) and
        // rejects negative rotation days with 400 — the catch
        // below surfaces those messages verbatim.
        snapshot_cron: formSnapshotCron.trim(),
        snapshot_rotation_days: formRotationDays,
      })
      .then((fresh) => {
        setSettings(fresh);
        // Phase 32.7: the snapshot loop reads cfg.SnapshotCron
        // each iteration, so the new schedule is in effect within
        // at most one fire interval. We don't promise "instant"
        // — a schedule change from "every minute" to "daily
        // 03:00" can land the next fire at tomorrow's 03:00.
        setInfo('Settings saved. The next snapshot will use the new schedule.');
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

      <div className="rounded border border-border p-4 bg-background space-y-3">
        <h2 className="font-semibold">Settings</h2>
        {settings ? (
          <div className="space-y-3 text-sm">
            <label className="flex items-center gap-2">
              <Checkbox
                data-testid="settings-enabled"
                checked={formEnabled}
                onCheckedChange={(v) => setFormEnabled(v === true)}
              />
              <span>Backup enabled</span>
            </label>
            <label className="block">
              <span className="text-xs text-slate-500">Remote URL</span>
              <Input
                type="url"
                data-testid="settings-remote-url"
                value={formRemoteUrl}
                onChange={(e) => setFormRemoteUrl(e.target.value)}
                placeholder="https://github.com/me/orenda.git"
                className="mt-1 text-sm font-mono"
              />
            </label>
            <label className="block">
              <span className="text-xs text-slate-500">Remote auth (token)</span>
              <Input
                type="password"
                data-testid="settings-remote-auth"
                value={formRemoteAuth}
                onChange={(e) => setFormRemoteAuth(e.target.value)}
                placeholder={
                  settings.has_auth ? '•••• (configured — leave blank to keep)' : 'optional'
                }
                autoComplete="off"
                className="mt-1 text-sm font-mono"
              />
            </label>
            {/* Phase 32.7: snapshot schedule + rotation. The cron
                expression is the same 5-field format the rest of
                the Unix world uses (minute, hour, day-of-month,
                month, day-of-week); the server validates it at
                PUT time and rejects bad input with 400. UTC by
                convention — the form's help text spells this out
                so an operator in a non-UTC TZ doesn't get
                surprised when their "daily 03:00" lands on the
                wrong wall-clock hour. */}
            <label className="block">
              <span className="text-xs text-slate-500">
                Snapshot schedule (cron, UTC) — e.g. <code className="font-mono">0 3 * * *</code>{' '}
                for daily at 03:00, <code className="font-mono">*/15 * * * *</code> for every 15
                minutes
              </span>
              <Input
                type="text"
                data-testid="settings-snapshot-cron"
                value={formSnapshotCron}
                onChange={(e) => setFormSnapshotCron(e.target.value)}
                placeholder="0 3 * * *"
                spellCheck={false}
                className="mt-1 text-sm font-mono"
              />
            </label>
            <label className="block">
              <span className="text-xs text-slate-500">
                Snapshot rotation (days) — 0 = keep forever
              </span>
              <Input
                type="number"
                min={0}
                step={1}
                data-testid="settings-rotation-days"
                value={formRotationDays}
                onChange={(e) => setFormRotationDays(parseInt(e.target.value, 10) || 0)}
                className="mt-1 text-sm font-mono"
              />
            </label>
            <div className="flex gap-2 items-center">
              <Button
                type="button"
                data-testid="settings-save"
                onClick={onSaveSettings}
                disabled={savingSettings}
                variant="default"
                size="sm"
              >
                {savingSettings ? 'Saving…' : 'Save settings'}
              </Button>
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
        <Button
          type="button"
          onClick={onTestPush}
          disabled={busy !== null}
          variant="default"
          size="sm"
        >
          {busy === 'push' ? 'Pushing…' : 'Test push now'}
        </Button>
        <Button
          type="button"
          onClick={onSnapshot}
          disabled={busy !== null}
          variant="outline"
          size="sm"
        >
          {busy === 'snapshot' ? 'Snapshotting…' : 'Snapshot now'}
        </Button>
      </div>

      <div className="rounded border border-border p-4 bg-background">
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
                {' '}
                · latest{' '}
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
                <Button
                  type="button"
                  onClick={() => onRestore(s)}
                  variant="outline"
                  size="sm"
                  className="ml-2 text-xs"
                >
                  Restore…
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="rounded border border-border p-4 bg-background">
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
        <Dialog open onOpenChange={(open) => !open && closeRestore()}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Restore from snapshot</DialogTitle>
              <DialogDescription>
                Snapshot:{' '}
                <code className="px-1 bg-muted rounded text-xs break-all">
                  {restoreTarget.path}
                </code>
              </DialogDescription>
            </DialogHeader>
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
                className="bg-muted rounded p-3 text-xs overflow-x-auto whitespace-pre-wrap break-all"
              >
                {restoreHint ?? '…'}
              </pre>
              <Button
                type="button"
                onClick={copyHint}
                variant="default"
                size="sm"
                className="absolute top-2 right-2 text-xs"
              >
                {copied ? 'Copied' : 'Copy'}
              </Button>
            </div>
            <DialogFooter>
              <Button
                type="button"
                onClick={closeRestore}
                disabled={busy === 'restore'}
                variant="outline"
                size="sm"
              >
                Close
              </Button>
              <Button
                type="button"
                data-testid="restore-inline"
                onClick={() => void onRestoreInline(restoreTarget)}
                disabled={busy === 'restore'}
                variant="default"
                size="sm"
                className="bg-amber-600 hover:bg-amber-700"
              >
                {busy === 'restore' ? 'Restoring…' : 'Restore in this window'}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </section>
  );
}
