/**
 * Shared Vitest setup, loaded for every test environment.
 *
 * The jsdom environment runs with an `about:blank` document URL (see
 * vitest.config.ts): unmocked XHR then fails fast in the URL parser
 * instead of dialling http://localhost:3000 and spraying ECONNREFUSED
 * noise across the log. The cost of the opaque origin is that jsdom's
 * `window.localStorage` getter throws `SecurityError` (and surfaces as
 * `undefined`), while several components persist UI state there (kanban
 * card density, timer state, …). Install a minimal in-memory stand-in
 * that satisfies the Storage contract those components use.
 */
if (typeof localStorage === 'undefined') {
  const store = new Map<string, string>();

  const storage: Storage = {
    get length(): number {
      return store.size;
    },
    clear(): void {
      store.clear();
    },
    getItem(key: string): string | null {
      return store.has(key) ? (store.get(key) as string) : null;
    },
    key(index: number): string | null {
      return Array.from(store.keys())[index] ?? null;
    },
    removeItem(key: string): void {
      store.delete(key);
    },
    setItem(key: string, value: string): void {
      store.set(String(key), String(value));
    },
  };

  Object.defineProperty(globalThis, 'localStorage', {
    value: storage,
    configurable: true,
    writable: true,
  });
}
