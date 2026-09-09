import { openDB, type DBSchema, type IDBPDatabase } from 'idb';

/**
 * IndexedDB layer for the PWA.
 *
 * Stores:
 *   - outbox: pending offline mutations, flushed on reconnect
 */
interface OrendaDB extends DBSchema {
  outbox: {
    key: string;
    value: OutboxItem;
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

let dbPromise: Promise<IDBPDatabase<OrendaDB>> | null = null;

function db(): Promise<IDBPDatabase<OrendaDB>> {
  if (!dbPromise) {
    dbPromise = openDB<OrendaDB>('orenda', 1, {
      upgrade(d) {
        d.createObjectStore('outbox', { keyPath: 'id' });
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
