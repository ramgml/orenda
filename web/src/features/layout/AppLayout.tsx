/**
 * Authenticated app shell after the sidebar redesign.
 *
 * Layout:
 *   ┌──────────────┬────────────────────────────┐
 *   │  Sidebar     │  TopBar                     │
 *   │  (Projects + │ ──────────────────────────  │
 *   │   navigation)│  <Outlet/>                  │
 *   │              │                            │
 *   └──────────────┴────────────────────────────┘
 *
 * The sidebar is `hidden md:flex` so phones fall back to a slide-in
 * overlay (state lifted into this component). The Outlet lives in
 * `<main>` with horizontal padding and overflow-y scrolling.
 */
import { Suspense, useCallback, useState } from 'react';
import { Outlet } from 'react-router-dom';

import { ProjectSidebar } from '@/features/sidebar/ProjectSidebar';
import { SidebarProvider, useSidebar } from '@/features/sidebar/SidebarContext';
import { useWebSocketConnection } from '@/shared/ws';

import { AppTopBar } from './AppTopBar';

export function AppLayout(): JSX.Element {
  return (
    <SidebarProvider>
      <AppLayoutInner />
    </SidebarProvider>
  );
}

function AppLayoutInner(): JSX.Element {
  // Phase 27.2: mount the WS hook once at the layout root so every
  // authenticated route gets realtime updates without each component
  // having to subscribe. The connection itself is owned by the
  // singleton wsClient; this hook only drives connect/disconnect.
  useWebSocketConnection();

  const { collapsed } = useSidebar();
  const [mobileOpen, setMobileOpen] = useState(false);

  const openMobile = useCallback(() => setMobileOpen(true), []);
  const closeMobile = useCallback(() => setMobileOpen(false), []);

  return (
    <div className="min-h-full flex flex-col md:flex-row h-screen">
      {/* Desktop sidebar — always visible ≥ md. */}
      <ProjectSidebar />

      {/* Mobile sidebar overlay. Clicking the backdrop closes it. */}
      {mobileOpen && (
        <div
          className="md:hidden fixed inset-0 z-40 bg-black/40"
          onClick={closeMobile}
          aria-hidden
        />
      )}
      <div
        className={`md:hidden fixed inset-y-0 left-0 z-50 transform transition-transform ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        }`}
        aria-hidden={!mobileOpen}
      >
        {/* Render a second copy on mobile so we can keep styles intact. */}
        <MobileSidebar onClose={closeMobile} />
      </div>

      <div className="flex-1 flex flex-col min-w-0">
        <AppTopBar onMobileMenuClick={openMobile} />
        <main className="flex-1 overflow-y-auto px-4 md:px-6 py-4 md:py-6">
          <Suspense fallback={<div className="text-slate-500">Loading…</div>}>
            <Outlet />
          </Suspense>
        </main>
      </div>

      {/* Sidebar empty space on mobile when sidebar collapsed on desktop */}
      {collapsed && null}
    </div>
  );
}

/**
 * Mobile variant: a sidebar that fills the screen width and exposes a
 * close button. It renders the same Sections but allows closing itself.
 */
function MobileSidebar({ onClose }: { onClose: () => void }): JSX.Element {
  return (
    <div className="relative h-screen w-72 bg-slate-50 dark:bg-slate-900 border-r border-slate-200 dark:border-slate-800 flex flex-col">
      <div className="h-12 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between px-4">
        <span className="font-semibold">Navigation</span>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close navigation"
          className="h-7 w-7 rounded flex items-center justify-center hover:bg-slate-100 dark:hover:bg-slate-800"
        >
          ×
        </button>
      </div>
      <div className="flex-1 overflow-y-auto">
        <ProjectSidebar />
      </div>
    </div>
  );
}
