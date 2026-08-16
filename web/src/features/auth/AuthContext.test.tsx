// @vitest-environment jsdom
/**
 * AuthContext tests (Phase 28.23).
 *
 * Same axios-stub pattern as RequireAuth.test.tsx / LoginPage.test.tsx:
 * `api.me()` hits the stubbed http client so the provider's initial
 * refresh is deterministic. A probe component exposes the context
 * value (status/user) and captures `logout` so tests can invoke it.
 *
 * What's pinned:
 *   - /me failing (401 or any error) settles into 'anonymous'.
 *   - /me resolving settles into 'authenticated' with the profile.
 *   - logout() calls the API and clears the state — even if the call
 *     itself fails (the provider's `finally` block).
 */
import { AxiosError } from 'axios';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AuthProvider, useAuth } from '@/features/auth/AuthContext';

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

const OWNER = {
  user_id: 'u-1',
  email: 'owner@x.com',
  display_name: 'Owner',
  role: 'owner',
  scopes: [],
};

interface ProbeHandle {
  logout: () => Promise<void>;
}

function mountProvider(): ProbeHandle {
  const handle: ProbeHandle = {
    logout: () => Promise.reject(new Error('logout not captured yet')),
  };
  function Probe(): JSX.Element {
    const { user, status, logout } = useAuth();
    handle.logout = logout;
    return (
      <div>
        <span data-testid="status">{status}</span>
        <span data-testid="user">{user ? user.email : 'none'}</span>
      </div>
    );
  }
  render(
    <AuthProvider>
      <Probe />
    </AuthProvider>,
  );
  return handle;
}

describe('AuthContext', () => {
  it('settles into anonymous when /me rejects (401)', async () => {
    stubHttp.get.mockRejectedValueOnce(new AxiosError('unauthorized', '401'));

    mountProvider();

    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('anonymous');
    });
    expect(screen.getByTestId('user').textContent).toBe('none');
  });

  it('settles into authenticated with the profile when /me resolves', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: OWNER });

    mountProvider();

    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('authenticated');
    });
    expect(screen.getByTestId('user').textContent).toBe('owner@x.com');
  });

  it('logout calls the endpoint and clears state', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: OWNER });
    stubHttp.post.mockResolvedValueOnce({ data: undefined });

    const handle = mountProvider();
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('authenticated');
    });

    await act(async () => {
      await handle.logout();
    });

    expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/auth/logout');
    expect(screen.getByTestId('status').textContent).toBe('anonymous');
    expect(screen.getByTestId('user').textContent).toBe('none');
  });

  it('logout clears state even when the endpoint call fails', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: OWNER });
    stubHttp.post.mockRejectedValueOnce(new AxiosError('network down'));

    const handle = mountProvider();
    await waitFor(() => {
      expect(screen.getByTestId('status').textContent).toBe('authenticated');
    });

    // The provider clears state in a `finally` block, so the returned
    // promise still rejects — swallow it here and assert on the state.
    await act(async () => {
      await handle.logout().catch(() => undefined);
    });

    expect(screen.getByTestId('status').textContent).toBe('anonymous');
    expect(screen.getByTestId('user').textContent).toBe('none');
  });
});
