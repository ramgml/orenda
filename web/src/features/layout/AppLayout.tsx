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
import { Outlet } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';

import { LogOut } from 'lucide-react';

import { useAuth } from '@/features/auth/AuthContext';
import { Button } from '@/shared/ui/button';

import { ProjectSidebar } from '@/features/sidebar/ProjectSidebar';
import { SidebarNav } from '@/features/sidebar/SidebarNav';
import { SidebarProvider, useSidebar } from '@/features/sidebar/SidebarContext';
import { agentsQueryKey } from '@/shared/hooks/useAgents';
import { useWebSocketConnection, useWebSocketTopic } from '@/shared/ws';

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

  // Phase 28.23: agent lifecycle events (register/delete/heartbeat
  // status flips) live on the `agents` WS topic. Invalidating the
  // shared agents query here keeps every consumer (AgentsPage,
  // kanban AssigneeChip labels, sidebar badge) fresh without each
  // page wiring its own subscription.
  const queryClient = useQueryClient();
  useWebSocketTopic('agents', () => {
    void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
  });

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
  const { logout } = useAuth();
  return (
    <div className="relative h-screen w-72 bg-muted border-r border-border flex flex-col">
      <div className="h-12 border-b border-border flex items-center justify-between px-4">
        <span className="font-semibold">Navigation</span>
        <Button
          type="button"
          onClick={onClose}
          aria-label="Close navigation"
          variant="ghost"
          size="icon"
        >
          ×
        </Button>
      </div>
      <div className="flex-1 overflow-y-auto">
        <SidebarNav collapsed={false} />
      </div>
      <div className="border-t border-border px-2 py-2">
        <button
          type="button"
          onClick={() => {
            logout();
            onClose();
          }}
          className="flex items-center gap-3 px-3 py-1.5 text-sm rounded mx-2 text-muted-foreground hover:bg-slate-100 dark:hover:bg-slate-800 w-[calc(100%-16px)]"
        >
          <LogOut className="h-4 w-4 shrink-0" aria-hidden />
          Sign out
        </button>
      </div>
    </div>
  );
}
