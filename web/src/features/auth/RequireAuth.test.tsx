// @vitest-environment jsdom
/**
 * RequireAuth gate tests.
 *
 * RequireAuth is the route wrapper that decides what to render based
 * on `useAuth().status`:
 *
 *   - 'loading'    → a "Loading…" placeholder (no /me round-trip yet)
 *   - 'anonymous'  → <Navigate to="/login" replace />
 *   - 'authenticated' → renders children
 *
 * We let the real AuthProvider drive the state and just mock the api
 * client so /me can be made to throw (anonymous), resolve (authed),
 * or hang (loading) deterministically.
 */
import { AxiosError } from 'axios';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AuthProvider } from '@/features/auth/AuthContext';
import { RequireAuth } from '@/App';

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
});

afterEach(() => {
  cleanup();
});

function mountGate({ entry = '/protected' }: { entry?: string }) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<div>LOGIN</div>} />
          <Route
            path="/protected"
            element={
              <RequireAuth>
                <div>PROTECTED CONTENT</div>
              </RequireAuth>
            }
          />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('RequireAuth', () => {
  it('renders the "Loading…" placeholder while /me is in flight', async () => {
    // Never-resolving promise keeps AuthProvider in the initial 'loading' state.
    stubHttp.get.mockReturnValueOnce(new Promise(() => {}));

    mountGate({});

    // The placeholder is rendered synchronously; no await needed, but
    // findBy* tolerates React's effect timing.
    expect(await screen.findByText('Loading…')).toBeTruthy();
    // Crucially: no redirect to /login yet (that would cause a flicker
    // loop on hard reloads — see comment in App.tsx).
    expect(screen.queryByText('LOGIN')).toBeNull();
    expect(screen.queryByText('PROTECTED CONTENT')).toBeNull();
  });

  it('redirects to /login when /me fails (anonymous user)', async () => {
    stubHttp.get.mockRejectedValueOnce(new AxiosError('no session'));

    mountGate({});

    // Navigate renders the matched /login route element.
    expect(await screen.findByText('LOGIN')).toBeTruthy();
    expect(screen.queryByText('PROTECTED CONTENT')).toBeNull();
  });

  it('renders children when /me resolves (authenticated user)', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { user_id: 'u-1', email: 'me@x.com', display_name: 'Me', role: 'owner' },
    });

    mountGate({});

    expect(await screen.findByText('PROTECTED CONTENT')).toBeTruthy();
    expect(screen.queryByText('LOGIN')).toBeNull();

    // Sanity: AuthProvider kept the user around.
    await waitFor(() => {
      expect(stubHttp.get).toHaveBeenCalledWith('/api/v1/me');
    });
  });
});
