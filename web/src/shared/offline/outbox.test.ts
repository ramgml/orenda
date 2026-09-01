import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// outbox.ts imports `axios` and `db` (IndexedDB). We mock both so the
// smoke test runs in node without a browser, then verify the
// queueCreateTask path end-to-end.
vi.mock('axios', () => ({
  default: {
    post: vi.fn(),
  },
}));

vi.mock('@/shared/offline/db', () => {
  const store: Record<string, unknown[]> = {};
  return {
    outboxAdd: vi.fn(async (item: unknown) => {
      (store['outbox'] ??= []).push(item);
    }),
    outboxAll: vi.fn(async () => store['outbox'] ?? []),
    outboxRemove: vi.fn(async (id: string) => {
      const arr = (store['outbox'] ?? []) as Array<{ id: string }>;
      const next = arr.filter((x) => x.id !== id);
      store['outbox'] = next;
    }),
    cachePut: vi.fn(async () => undefined),
    cacheGet: vi.fn(async () => undefined),
  };
});

// crypto.randomUUID is widely available in node 19+ but we patch
// navigator.onLine too so the test doesn't bail on syncNow().
beforeEach(() => {
  Object.defineProperty(globalThis, 'navigator', {
    value: { onLine: true },
    configurable: true,
  });
});

afterEach(() => {
  vi.clearAllMocks();
});

describe('outbox', () => {
  it('queueCreateTask returns a client id and stores the item', async () => {
    const { queueCreateTask } = await import('@/shared/offline/outbox');
    const { outboxAll } = await import('@/shared/offline/db');

    const id = await queueCreateTask('proj-1', { title: 'offline task', column_id: 'col-1' });
    expect(typeof id).toBe('string');
    expect(id.length).toBeGreaterThan(8);

    const items = (await outboxAll()) as Array<{
      id: string;
      op: string;
      target: string;
      payload: { title: string; column_id: string };
      clientId: string;
    }>;
    expect(items).toHaveLength(1);
    expect(items[0].op).toBe('create_task');
    expect(items[0].target).toBe('proj-1');
    expect(items[0].payload.title).toBe('offline task');
    expect(items[0].payload.column_id).toBe('col-1');
    expect(items[0].clientId).toBe(id);
  });

  it('queueUpdateTask / queueMoveTask / queueCreateComment preserve the payload shape', async () => {
    const { queueUpdateTask, queueMoveTask, queueCreateComment } =
      await import('@/shared/offline/outbox');
    const { outboxAll } = await import('@/shared/offline/db');

    await queueUpdateTask('task-1', { title: 'renamed' });
    await queueMoveTask('task-1', 'col-2');
    await queueCreateComment('task-1', 'see [[other-task]]');

    const items = (await outboxAll()) as Array<{ op: string; target: string; payload: unknown }>;
    const ops = items.map((i) => i.op);
    expect(ops).toContain('update_task');
    expect(ops).toContain('move_task');
    expect(ops).toContain('create_comment');

    const move = items.find((i) => i.op === 'move_task')!;
    expect(move.target).toBe('task-1');
    expect(move.payload).toEqual({ column_id: 'col-2' });
    // T118: same-column reorder carries a position in the offline
    // outbox too — the sync op accepts it just like the REST move.
    await queueMoveTask('task-1', 'col-2', 1536);
    const itemsWithPos = (await outboxAll()) as Array<{
      op: string;
      target: string;
      payload: { column_id?: string; position?: number };
    }>;
    const moveWithPos = itemsWithPos.filter((i) => i.op === 'move_task').pop()!;
    expect(moveWithPos.payload).toEqual({ column_id: 'col-2', position: 1536 });

    const comment = items.find((i) => i.op === 'create_comment')!;
    expect(comment.payload).toEqual({ body_md: 'see [[other-task]]' });
  });

  it('syncNow does nothing when navigator.onLine is false', async () => {
    Object.defineProperty(globalThis, 'navigator', {
      value: { onLine: false },
      configurable: true,
    });
    const axios = (await import('axios')).default as unknown as { post: ReturnType<typeof vi.fn> };
    const { syncNow } = await import('@/shared/offline/outbox');

    const res = await syncNow();
    expect(res).toBeNull();
    expect(axios.post).not.toHaveBeenCalled();
  });
});
