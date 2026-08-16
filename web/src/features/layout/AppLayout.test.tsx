// @vitest-environment jsdom
/**
 * AppLayout smoke tests.
 *
 * AppLayout is the authenticated shell: SidebarProvider + ProjectSidebar
 * + AppTopBar + a Suspense fallback around <Outlet />. The sidebar
 * itself is hard to unit-test (it has many react-query hooks); what we
 * can pin down here is the contract that matters: the Outlet renders
 * whatever the matched child route says, and the Suspense boundary
 * catches a lazy child that suspends.
 *
 * ProjectSidebar reads useAuth() so we have to wrap the shell in an
 * AuthProvider too; we mock the api client so AuthProvider settles in
 * 'anonymous' without making a real /me call.
 */
import { AxiosError } from 'axios';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { lazy } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AuthProvider } from '@/features/auth/AuthContext';
import { AppLayout } from '@/features/layout/AppLayout';
import { agentsQueryKey } from '@/shared/hooks/useAgents';
import { wsClient } from '@/shared/ws';

const { stubHttp } = vi.hoisted(() => ({
  stubHttp: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
    interceptors: { response: { use: vi.fn() } },
  },
}));

vi.mock('axios', async (importOriginal) => {
  const actual = await importOriginal<typeof import('axios')>();
  return {
    ...actual,
    default: { ...actual.default, create: vi.fn(() => stubHttp) },
  };
});

beforeEach(() => {
  vi.clearAllMocks();
  wsClient.disconnect();
  wsClient['listeners'].clear();
});

afterEach(() => {
  cleanup();
});

function mountShell(entry: string) {
  // AuthProvider calls api.me() on mount; pre-seed the rejection so
  // the auth state lands in 'anonymous' and never makes a real request.
  stubHttp.get.mockRejectedValue(new AxiosError('no session'));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const utils = render(
    <QueryClientProvider client={client}>
      <AuthProvider>
        <MemoryRouter initialEntries={[entry]}>
          <Routes>
            <Route element={<AppLayout />}>
              <Route path="/" element={<div>CHILD HOME</div>} />
              <Route path="/page-a" element={<div>CHILD PAGE A</div>} />
            </Route>
          </Routes>
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  );
  return { client, ...utils };
}

describe('AppLayout', () => {
  it('renders the matched child route through <Outlet />', async () => {
    mountShell('/');

    expect(await screen.findByText('CHILD HOME')).toBeTruthy();
  });

  it("does not render a sibling route's content", () => {
    mountShell('/');

    expect(screen.queryByText('CHILD PAGE A')).toBeNull();
  });

  it('shows the Suspense fallback when a lazy child suspends', async () => {
    // Lazy components suspend on first render until their module
    // resolves. We never resolve it, so the fallback ("Loading…")
    // stays visible.
    const LazyChild = lazy(() => new Promise<never>(() => {}));

    stubHttp.get.mockRejectedValue(new AxiosError('no session'));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <AuthProvider>
          <MemoryRouter initialEntries={['/']}>
            <Routes>
              <Route element={<AppLayout />}>
                <Route path="/" element={<LazyChild />} />
              </Route>
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    );

    // AppLayout's inner <Suspense> shows "Loading…" until the lazy
    // module resolves; we never resolve, so the fallback is what we see.
    await waitFor(() => {
      expect(screen.getByText('Loading…')).toBeTruthy();
    });
  });

  it('invalidates the agents query on a WS "agents" event (Phase 28.23)', async () => {
    const { client } = mountShell('/');
    expect(await screen.findByText('CHILD HOME')).toBeTruthy();

    const invalidateSpy = vi.spyOn(client, 'invalidateQueries');

    wsClient['listeners'].get('agents')?.forEach((fn) => fn({ topic: 'agents', body: {} }));

    await waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: agentsQueryKey });
    });
  });
});
