import { openDB, type DBSchema, type IDBPDatabase } from 'idb';

/**
 * IndexedDB layer for the PWA.
 *
 * Stores:
 *   - outbox: pending offline mutations, flushed on reconnect
 *   - cache:  last successful GET responses for offline reads
 */
interface OrendaDB extends DBSchema {
  outbox: {
    key: string;
    value: OutboxItem;
  };
  cache: {
    key: string;
    value: CachedResponse;
  };
}

export interface OutboxItem {
  id: string;
  op:
    | 'create_task'
    | 'update_task'
    | 'move_task'
    | 'create_comment'
    | 'create_event'
    | 'create_page';
  target: string;
  payload: unknown;
  clientId: string; // idempotency key
  createdAt: string;
}

export interface CachedResponse {
  url: string;
  body: unknown;
  fetchedAt: string;
}

let dbPromise: Promise<IDBPDatabase<OrendaDB>> | null = null;

function db(): Promise<IDBPDatabase<OrendaDB>> {
  if (!dbPromise) {
    dbPromise = openDB<OrendaDB>('orenda', 1, {
      upgrade(d) {
        d.createObjectStore('outbox', { keyPath: 'id' });
        d.createObjectStore('cache', { keyPath: 'url' });
      },
    });
  }
  return dbPromise;
}

// ---------- outbox ----------

/** Enqueue a mutation for later sync. */
export async function outboxAdd(item: OutboxItem): Promise<void> {
  await (await db()).put('outbox', item);
}

/** Read all pending mutations (oldest first). */
export async function outboxAll(): Promise<OutboxItem[]> {
  const items = await (await db()).getAll('outbox');
  return items.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}

/** Remove an item after a successful sync. */
export async function outboxRemove(id: string): Promise<void> {
  await (await db()).delete('outbox', id);
}

/** Clear the entire outbox (e.g. after a forced reset). */
export async function outboxClear(): Promise<void> {
  await (await db()).clear('outbox');
}

/** Count pending items — for the badge. */
export async function outboxCount(): Promise<number> {
  return (await db()).count('outbox');
}

// ---------- cache ----------

/** Cache a GET response. */
export async function cachePut(url: string, body: unknown): Promise<void> {
  await (await db()).put('cache', { url, body, fetchedAt: new Date().toISOString() });
}

/** Read a cached response, or undefined if absent. */
export async function cacheGet(url: string): Promise<unknown | undefined> {
  const row = await (await db()).get('cache', url);
  return row?.body;
}
