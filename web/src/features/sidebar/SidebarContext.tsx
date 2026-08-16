/**
 * Persistent sidebar expanded/collapsed state.
 *
 * The user can hide the sidebar entirely (replace it with a slim column
 * of project colour-dots). We store that preference in localStorage so
 * it survives reloads and is shared across tabs via the `storage`
 * event. The default is `expanded: true` (sidebar visible at 240px).
 */
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';

const STORAGE_KEY = 'orenda.sidebar.collapsed';

function readCollapsed(): boolean {
  try {
    if (typeof window === 'undefined') return false;
    return window.localStorage.getItem(STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

function writeCollapsed(v: boolean): void {
  try {
    if (typeof window === 'undefined') return;
    window.localStorage.setItem(STORAGE_KEY, v ? '1' : '0');
  } catch {
    // Storage may be disabled in private mode; fail silently.
  }
}

interface SidebarContextValue {
  /** True when the sidebar is collapsed to its narrow (icon-only) width. */
  collapsed: boolean;
  /** Toggle the current state. */
  toggle: () => void;
  /** Force a particular state (used by the overlay-close button). */
  set: (next: boolean) => void;
}

const SidebarContext = createContext<SidebarContextValue | null>(null);

export function SidebarProvider({
  children,
  initialCollapsed = false,
}: {
  children: ReactNode;
  initialCollapsed?: boolean;
}): JSX.Element {
  const [collapsed, setCollapsed] = useState<boolean>(
    () =>
      // The `initialCollapsed` prop wins over localStorage when supplied
      // (used in tests). Production mounts defer to localStorage.
      initialCollapsed || readCollapsed(),
  );

  useEffect(() => {
    function onStorage(ev: StorageEvent): void {
      if (ev.key !== STORAGE_KEY) return;
      setCollapsed(readCollapsed());
    }
    if (typeof window !== 'undefined') {
      window.addEventListener('storage', onStorage);
      return () => window.removeEventListener('storage', onStorage);
    }
    return undefined;
  }, []);

  const toggle = useCallback(() => {
    setCollapsed((prev) => {
      const next = !prev;
      writeCollapsed(next);
      return next;
    });
  }, []);

  const set = useCallback((next: boolean) => {
    setCollapsed(next);
    writeCollapsed(next);
  }, []);

  const value = useMemo<SidebarContextValue>(
    () => ({ collapsed, toggle, set }),
    [collapsed, toggle, set],
  );

  return <SidebarContext.Provider value={value}>{children}</SidebarContext.Provider>;
}

/**
 * Access the sidebar state from anywhere below `<SidebarProvider>`.
 * Throws if used outside the provider so we never silently fall back
 * to a default at runtime.
 */
export function useSidebar(): SidebarContextValue {
  const ctx = useContext(SidebarContext);
  if (!ctx) {
    throw new Error('useSidebar must be used inside a <SidebarProvider>');
  }
  return ctx;
}
