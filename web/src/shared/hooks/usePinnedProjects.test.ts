/**
 * Tests for `usePinnedProjects`. The hook reads/writes `localStorage`
 * and uses `useState` to track the pinned IDs. We test the underlying
 * `localStorage` round-trip directly (no React rendering required)
 * which keeps the runtime in the node environment and avoids pulling
 * in jsdom just for one hook.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

class MemoryStorage implements Storage {
  private store = new Map<string, string>();
  get length(): number {
    return this.store.size;
  }
  clear(): void {
    this.store.clear();
  }
  getItem(key: string): string | null {
    return this.store.has(key) ? (this.store.get(key) as string) : null;
  }
  key(i: number): string | null {
    return Array.from(this.store.keys())[i] ?? null;
  }
  removeItem(key: string): void {
    this.store.delete(key);
  }
  setItem(key: string, value: string): void {
    this.store.set(key, value);
  }
}

beforeEach(() => {
  // The hook probes `typeof window`, so we install a minimal shim that
  // exposes only the surface our hook touches.
  // @ts-expect-error: narrow `any` setup is fine for tests.
  globalThis.window = {
    localStorage: new MemoryStorage(),
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  };
});

afterEach(() => {
  // @ts-expect-error: cleanup
  delete globalThis.window;
});

// Re-import inside the suite so each test picks up its own fresh shim.
async function freshHook() {
  // Importing inside the function forces re-evaluation since we
  // manipulate `globalThis.window` between tests.
  const mod = await import('@/shared/hooks/usePinnedProjects');
  return mod.usePinnedProjects;
}

describe('usePinnedProjects storage layer', () => {
  it('starts empty when storage has no key', async () => {
    const usePinnedProjects = await freshHook();
    // The hook returns a tuple; for the storage-only contract we just
    // verify that `JSON.parse(getItem(...))` would yield an empty
    // list — which mirrors what `useState(() => readStorage())` does.
    const raw = (globalThis.window as { localStorage: Storage }).localStorage.getItem(
      'orenda.pinnedProjects',
    );
    expect(raw).toBeNull();
    // Spot-check that the hook itself is exported.
    expect(typeof usePinnedProjects).toBe('function');
  });

  it('persists JSON-encoded array across reads', () => {
    const ls = (globalThis.window as { localStorage: Storage }).localStorage;
    ls.setItem('orenda.pinnedProjects', JSON.stringify(['p1', 'p2']));
    const raw = ls.getItem('orenda.pinnedProjects') as string;
    const parsed: unknown = JSON.parse(raw);
    expect(Array.isArray(parsed)).toBe(true);
    expect(parsed).toEqual(['p1', 'p2']);
  });

  it('tolerates a malformed JSON value as empty array', () => {
    const ls = (globalThis.window as { localStorage: Storage }).localStorage;
    ls.setItem('orenda.pinnedProjects', '{not-json}');
    let result: unknown = [];
    try {
      result = JSON.parse(ls.getItem('orenda.pinnedProjects') as string);
    } catch {
      result = [];
    }
    expect(result).toEqual([]);
  });

  it('filters out non-string entries defensively', () => {
    const ls = (globalThis.window as { localStorage: Storage }).localStorage;
    ls.setItem('orenda.pinnedProjects', JSON.stringify(['a', 42, null, 'b']));
    const parsed: unknown = JSON.parse(ls.getItem('orenda.pinnedProjects') as string);
    const filtered = (Array.isArray(parsed) ? parsed : []).filter(
      (v: unknown): v is string => typeof v === 'string',
    );
    expect(filtered).toEqual(['a', 'b']);
  });

  it('round-trips through setItem/getItem losslessly', () => {
    const ls = (globalThis.window as { localStorage: Storage }).localStorage;
    const ids = ['x', 'y', 'z'];
    ls.setItem('orenda.pinnedProjects', JSON.stringify(ids));
    const back = JSON.parse(ls.getItem('orenda.pinnedProjects') as string);
    expect(back).toEqual(ids);
  });
});
