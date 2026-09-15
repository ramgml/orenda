import type { ApiClient } from './core';

/**
 * Capabilities advertised by the server (mirrors Go's api.Capabilities).
 */
interface Capabilities {
  auth: boolean;
  rest_tasks: boolean;
  websocket: boolean;
  backup: boolean;
  bots: boolean;
  fts: boolean;
  pwa: boolean;
}

export interface InfoResponse {
  version: string;
  name: string;
  capabilities: Capabilities;
}

export interface HealthResponse {
  status: string;
  version: string;
}

/**
 * Phase 24: GET /api/v1/stats. Mirrors `statsResponse` in
 * `internal/api/handlers_stats.go`. Optional fields (last_backup_unix)
 * are only emitted when the server has a backup timestamp; the UI
 * treats them as "not yet".
 */
export interface StatsResponse {
  uptime_seconds: number;
  requests_total: number;
  requests_2xx: number;
  requests_3xx: number;
  requests_4xx: number;
  requests_5xx: number;
  slow_requests: number;
  ws_connections: number;
  db_bytes: number;
  db_path: string;
  last_backup_unix?: number;
}

/**
 * Auth profile returned by GET /api/v1/me and embedded in LoginResponse.
 */
export interface UserProfile {
  user_id: string;
  email: string;
  display_name?: string;
  role?: string;
  scopes?: string[];
}

type LoginResponse = UserProfile;

export interface CalendarEvent {
  id: string;
  title: string;
  description?: string;
  start_at: string;
  end_at: string;
  all_day: boolean;
  color?: string;
  project_id?: string;
  recurrence?: string;
  created_at: string;
  updated_at: string;
}

interface BotSubscription {
  id: string;
  user_id: string;
  bot_type: string;
  target_address: string;
  events: string[];
  enabled: boolean;
  created_at: string;
}

export interface BackupSettings {
  enabled: boolean;
  remote_url: string;
  has_auth: boolean;
  /**
   * Phase 32.7: cron-driven snapshot fire. Operators can edit
   * from /settings/backups; the server persists and hot-reloads
   * without a restart. The default ("0 3 * * *" = daily 03:00
   * UTC) is shown by the form when the persisted value is empty.
   */
  snapshot_cron: string;
  /** Phase 32.7: 0 = keep forever; otherwise days to retain. */
  snapshot_rotation_days: number;
  updated_at?: string;
  /**
   * Phase 28.1 polish.1: when the UI has overridden the in-memory
   * config, the running `*backup.Service` is still on the old URL
   * — the operator needs to restart for the new remote to apply.
   * The form shows a banner whenever this hint is non-empty.
   */
  source_hint?: string;
}

/**
 * Body shape for `setBackupSettings`. All fields are optional so
 * the operator can change one at a time without round-tripping the
 * current state; missing fields keep the persisted value. The
 * server validates snapshot_cron (5-field cron expression) and
 * rejects negative snapshot_rotation_days.
 */
interface BackupSettingsInput {
  enabled?: boolean;
  remote_url?: string;
  remote_auth?: string;
  snapshot_cron?: string;
  snapshot_rotation_days?: number;
}

export interface BackupSnapshot {
  path: string;
  size: number;
  mod_time: string;
}

export interface BackupLogEntry {
  id: string;
  type: string;
  status: 'success' | 'failed';
  message: string;
  snapshot_path: string;
  created_at: string;
}

/**
 * System domain endpoints: health/info/auth/stats, calendar events,
 * backups, maintenance mode, bot subscriptions.
 * Merged onto the ApiClient prototype in client.ts.
 */
export const systemEndpoints = {
  health(): Promise<HealthResponse> {
    return this.http.get<HealthResponse>('/healthz').then((r) => r.data);
  },

  info(): Promise<InfoResponse> {
    return this.http.get<InfoResponse>('/api/v1/info').then((r) => r.data);
  },

  me(): Promise<UserProfile> {
    return this.http.get<UserProfile>('/api/v1/me').then((r) => r.data);
  },

  /**
   * Phase 24 observable snapshot. The Settings index (Phase 28.2)
   * renders this in the About panel; uptime + DB size are the two
   * fields humans actually look at. No auth required — the endpoint
   * is intentionally public.
   */
  getStats(): Promise<StatsResponse> {
    return this.http.get<StatsResponse>('/api/v1/stats').then((r) => r.data);
  },

  login(email: string, password: string): Promise<LoginResponse> {
    return this.http
      .post<LoginResponse>('/api/v1/auth/login', { email, password })
      .then((r) => r.data);
  },

  logout(): Promise<void> {
    return this.http.post<void>('/api/v1/auth/logout').then(() => undefined);
  },

  // ---- Events (Phase 4) ----

  listEvents(params: { from: string; to: string; project_id?: string }): Promise<CalendarEvent[]> {
    return this.http
      .get<{ events: CalendarEvent[] | null }>('/api/v1/events', { params })
      .then((r) => r.data.events ?? []);
  },

  createEvent(input: {
    title: string;
    description?: string;
    start_at: string;
    end_at: string;
    all_day?: boolean;
    color?: string;
    project_id?: string;
    recurrence?: string;
  }): Promise<CalendarEvent> {
    return this.http.post<CalendarEvent>('/api/v1/events', input).then((r) => r.data);
  },

  patchEvent(
    id: string,
    input: Partial<{
      title: string;
      description: string;
      start_at: string;
      end_at: string;
      all_day: boolean;
      color: string;
      project_id: string;
      recurrence: string;
    }>,
  ): Promise<CalendarEvent> {
    return this.http.patch<CalendarEvent>(`/api/v1/events/${id}`, input).then((r) => r.data);
  },

  deleteEvent(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/events/${id}`).then(() => undefined);
  },

  // ---- Backups (Phase 7) ----

  getBackupSettings(): Promise<BackupSettings> {
    return this.http.get<BackupSettings>('/api/v1/backups/settings').then((r) => r.data);
  },

  /**
   * Phase 28.1 (polish.1): PUT /api/v1/backups/settings. Persists
   * the (enabled, remote_url, remote_auth) tuple to the
   * backup_settings table; the response body mirrors GET so the UI
   * can update from the response without an extra round-trip.
   *
   * Settings take effect on the next process restart (the
   * `*backup.Service` is wired from cfg at startup). The UI
   * surfaces this via the `source_hint` field on GET — when it
   * reads "ui_override_restart_to_apply", the form shows a
   * banner.
   */
  setBackupSettings(body: BackupSettingsInput): Promise<BackupSettings> {
    return this.http.put<BackupSettings>('/api/v1/backups/settings', body).then((r) => r.data);
  },

  testBackupPush(): Promise<{ status: string }> {
    return this.http.post<{ status: string }>('/api/v1/backups/test', {}).then((r) => r.data);
  },

  /**
   * Phase 30.9: read-only backup status (snapshot count + latest
   * path/size). No side-effects; safe to poll.
   */
  getBackupStatus(): Promise<{
    scheduler_disabled: boolean;
    snapshot_count?: number;
    latest_snapshot?: string;
    latest_snapshot_size?: number;
    latest_snapshot_unix?: number;
    snapshot_error?: string;
  }> {
    return this.http.get('/api/v1/backups/status').then((r) => r.data);
  },

  createSnapshot(): Promise<{ path: string }> {
    return this.http.post<{ path: string }>('/api/v1/backups/snapshot', {}).then((r) => r.data);
  },

  listBackupSnapshots(): Promise<{ snapshots: BackupSnapshot[] }> {
    return this.http
      .get<{ snapshots: BackupSnapshot[] }>('/api/v1/backups/snapshots')
      .then((r) => r.data);
  },

  listBackupLog(params?: { limit?: number }): Promise<{ log: BackupLogEntry[] }> {
    return this.http
      .get<{ log: BackupLogEntry[] }>('/api/v1/backups/log', { params })
      .then((r) => r.data);
  },

  /** Trigger a restore from a snapshot. Phase 22.3 added the
   * `force=true` path that, combined with maintenance mode, runs
   * the swap in-process. The default call (no `force`) still
   * returns the CLI hint for backward compat.
   *
   * The UI wraps this with maintenance-mode on/off so the
   * in-process path stays one click away. */
  restoreBackup(
    path: string,
    opts?: { force?: boolean },
  ): Promise<{ snapshot: string; hint?: string; status?: string }> {
    return this.http
      .post<{
        snapshot: string;
        hint?: string;
        status?: string;
      }>('/api/v1/backups/restore', { path, force: !!opts?.force })
      .then((r) => r.data);
  },

  // ---- Maintenance mode (Phase 22.3) ----
  //
  // While maintenance is on the API blocks non-GET methods
  // (except /maintenance/off itself). The UI flips it on before
  // a restore and off once the operator is ready.

  maintenanceOn(): Promise<{ maintenance: true }> {
    return this.http.post<{ maintenance: true }>('/api/v1/maintenance/on', {}).then((r) => r.data);
  },

  maintenanceOff(): Promise<{ maintenance: false }> {
    return this.http
      .post<{ maintenance: false }>('/api/v1/maintenance/off', {})
      .then((r) => r.data);
  },

  maintenanceStatus(): Promise<{ maintenance: boolean }> {
    return this.http.get<{ maintenance: boolean }>('/api/v1/maintenance').then((r) => r.data);
  },

  // ---- Bot subscriptions (Phase 10) ----

  listSubscriptions(): Promise<{ subscriptions: BotSubscription[] }> {
    return this.http
      .get<{ subscriptions: BotSubscription[] }>('/api/v1/notifications/subscriptions')
      .then((r) => r.data);
  },

  createSubscription(input: {
    bot_type: string;
    target_address: string;
    events: string[];
    enabled: boolean;
  }): Promise<BotSubscription> {
    return this.http
      .post<BotSubscription>('/api/v1/notifications/subscriptions', input)
      .then((r) => r.data);
  },

  deleteSubscription(id: string): Promise<void> {
    return this.http
      .delete<void>(`/api/v1/notifications/subscriptions/${id}`)
      .then(() => undefined);
  },

  // Phase 22.3 follow-up: resolve a `/start`-issued Telegram bind
  // code into a chat id and create the default subscription
  // server-side. Returns the resolved chat id + the new
  // subscription id so the UI can show a success banner and
  // refresh the list.
  bindTelegram(input: { code: string }): Promise<{
    chat_id: number;
    username?: string;
    subscription_id: string;
  }> {
    return this.http
      .post<{
        chat_id: number;
        username?: string;
        subscription_id: string;
      }>('/api/v1/bots/telegram/bind', input)
      .then((r) => r.data);
  },

  /**
   * Phase 10 Test send UI: deliver a one-off message through any
   * configured bot. The handler ignores the subscription store —
   * the address is whatever the user types in the form, even if
   * no subscription exists yet. Returns the server's
   * `ok: true` payload on success.
   *
   * On failure the server returns one of:
   *   - 400 `invalid_input`        (missing bot_type / target_address)
   *   - 400 `unknown_bot_type`     (bot_type not in knownTestBotTypes)
   *   - 400 per-bot target pre-check (e.g. webhook must be http(s))
   *   - 503 `bot_not_running`      (bot not registered in the live registry)
   *   - 502 `send_failed`          (transport-level error after registry hit)
   *
   * Errors (.catch) here surface the raw axios message — the UI
   * pattern-matches against `error.response.data.error` to render
   * a friendly hint.
   */
  testBot(input: { bot_type: string; target_address: string }): Promise<{
    ok: boolean;
    bot_type: string;
    target: string;
    sentinel: string;
    sent_at: string;
  }> {
    return this.http
      .post<{
        ok: boolean;
        bot_type: string;
        target: string;
        sentinel: string;
        sent_at: string;
      }>('/api/v1/bots/test', input)
      .then((r) => r.data);
  },
} satisfies ThisType<ApiClient>;

/** Method surface contributed by this domain to the ApiClient type. */
export type SystemApi = typeof systemEndpoints;
