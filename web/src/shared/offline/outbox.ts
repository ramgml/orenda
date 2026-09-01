import axios, { AxiosError } from 'axios';

import { outboxAdd, outboxAll, outboxRemove, cachePut, cacheGet, type OutboxItem } from './db';

/**
 * Outbox manager: queues mutations while offline, flushes them via
 * POST /api/v1/sync when the connection returns.
 *
 * Usage:
 *   - `syncNow()` is called on window 'online' and on app boot.
 *   - `queueCreateTask(input)` etc. queue an item and return a client-
 *     generated id the UI can render optimistically.
 */

export interface SyncResultItem {
  client_id: string;
  ok: boolean;
  error?: string;
}

export interface SyncResponse {
  results: SyncResultItem[];
}

let syncing = false;
const listeners = new Set<() => void>();

export function onSyncStateChange(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

function emit(): void {
  for (const fn of listeners) fn();
}

function clientId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return 'c-' + Math.random().toString(36).slice(2);
}

/** Queue a task creation for offline sync. Returns the client id. */
export async function queueCreateTask(
  projectId: string,
  input: { title: string; column_id?: string; description?: string },
): Promise<string> {
  const id = clientId();
  await outboxAdd({
    id,
    op: 'create_task',
    target: projectId,
    payload: input,
    clientId: id,
    createdAt: new Date().toISOString(),
  });
  emit();
  return id;
}

/** Queue a task update. */
export async function queueUpdateTask(taskId: string, input: unknown): Promise<string> {
  const id = clientId();
  await outboxAdd({
    id,
    op: 'update_task',
    target: taskId,
    payload: input,
    clientId: id,
    createdAt: new Date().toISOString(),
  });
  emit();
  return id;
}

/** Queue a kanban move. */
export async function queueMoveTask(
  taskId: string,
  columnId: string,
  position?: number,
): Promise<string> {
  const id = clientId();
  await outboxAdd({
    id,
    op: 'move_task',
    target: taskId,
    payload:
      typeof position === 'number' ? { column_id: columnId, position } : { column_id: columnId },
    clientId: id,
    createdAt: new Date().toISOString(),
  });
  emit();
  return id;
}

/** Queue a comment. */
export async function queueCreateComment(taskId: string, bodyMd: string): Promise<string> {
  const id = clientId();
  await outboxAdd({
    id,
    op: 'create_comment',
    target: taskId,
    payload: { body_md: bodyMd },
    clientId: id,
    createdAt: new Date().toISOString(),
  });
  emit();
  return id;
}

/** Flush the outbox. Idempotent; safe to call from 'online' handlers. */
export async function syncNow(): Promise<SyncResponse | null> {
  if (syncing) return null;
  if (!navigator.onLine) return null;
  const items = await outboxAll();
  if (items.length === 0) return null;

  syncing = true;
  emit();
  try {
    const resp = await axios.post<SyncResponse>(
      '/api/v1/sync',
      { ops: items.map(wire) },
      { withCredentials: true },
    );
    const byId = new Map(resp.data.results.map((r) => [r.client_id, r]));
    for (const item of items) {
      const r = byId.get(item.clientId);
      if (r?.ok) {
        await outboxRemove(item.id);
      }
    }
    return resp.data;
  } catch (e) {
    // Network or 5xx: keep the items, try again later.
    if (e instanceof AxiosError && e.response && e.response.status < 500) {
      // 4xx = permanent failure; drop the bad ops so the queue isn't stuck.
      for (const item of items) await outboxRemove(item.id);
    }
    return null;
  } finally {
    syncing = false;
    emit();
  }
}

function wire(item: OutboxItem): Record<string, unknown> {
  return {
    op: item.op,
    target: item.target,
    payload: item.payload,
    client_id: item.clientId,
    created_at: item.createdAt,
  };
}

/** Read-through helper for offline GETs: returns cached body if offline. */
export async function readThrough<T>(url: string, fetcher: () => Promise<T>): Promise<T> {
  if (navigator.onLine) {
    try {
      const data = await fetcher();
      await cachePut(url, data);
      return data;
    } catch (e) {
      const cached = await cacheGet(url);
      if (cached !== undefined) return cached as T;
      throw e;
    }
  }
  const cached = await cacheGet(url);
  if (cached !== undefined) return cached as T;
  throw new Error('offline and no cache for ' + url);
}

/** Wire the window 'online' listener once. Call from main.tsx. */
export function registerOfflineHandlers(): void {
  window.addEventListener('online', () => {
    void syncNow();
  });
}
