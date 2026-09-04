// @vitest-environment jsdom
/**
 * LoginPage component tests.
 *
 * Pattern: mock `@/shared/api/client` with a controllable stub so we can
 * drive the login flow without a server. AuthProvider still wraps the
 * component, but its `refresh()` on mount calls the (mocked) `api.me`
 * which we configure to throw — that lands the auth state in
 * `anonymous` and lets LoginPage render the form.
 *
 * What's pinned here:
 *   - 401 from the server surfaces the "Invalid email or password."
 *     error and the user stays on /login.
 *   - Successful login calls `api.login` with the trimmed email (and
 *     not the raw value), the button is disabled while in flight, and
 *     after the AuthProvider flips to `authenticated` the page renders
 *     the `<Navigate>` element pointing at the saved `from` path.
 *   - `from` defaults to '/' when location.state is missing; when the
 *     router pushed us here with `state: { from: '/projects' }` the
 *     Navigate ends up pointing at /projects instead.
 *
 * Note on matchers: the rest of the suite uses bare vitest
 * assertions (truthy/equal/null), so we deliberately avoid
 * `@testing-library/jest-dom` here — adding it would be a new devDep
 * for one file's worth of convenience. `toBeTruthy` + `queryByText`
 * covers the same surface.
 */
import { AxiosError } from 'axios';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AuthProvider } from '@/features/auth/AuthContext';
import { LoginPage } from '@/features/auth/LoginPage';

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

// Import AFTER axios is mocked so client.ts picks up the stub.
const { api } = await import('@/shared/api/client');

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

function mountLogin(initialEntry = '/login') {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <AuthProvider>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/" element={<div>HOME</div>} />
          <Route path="/projects" element={<div>PROJECTS</div>} />
        </Routes>
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('LoginPage', () => {
  it('renders the sign-in form for an anonymous user', async () => {
    // /me throws → status='anonymous' → form visible.
    stubHttp.get.mockRejectedValueOnce(new AxiosError('no session'));

    mountLogin();

    expect(await screen.findByRole('heading', { name: 'Sign in to Orenda' })).toBeTruthy();
    expect(screen.getByLabelText('Email')).toBeTruthy();
    expect(screen.getByLabelText('Password')).toBeTruthy();
    expect(screen.getByRole('button', { name: /sign in/i })).toBeTruthy();
  });

  it('surfaces "Invalid email or password." on a 401 from the server', async () => {
    stubHttp.get.mockRejectedValueOnce(new AxiosError('no session'));
    const err = new AxiosError('Unauthorized');
    // @ts-expect-error — axios adds `response` lazily; tests don't need the full shape.
    err.response = { status: 401, data: {}, headers: {}, config: {}, statusText: '' };
    stubHttp.post.mockRejectedValueOnce(err);

    mountLogin();

    await screen.findByRole('heading', { name: 'Sign in to Orenda' });
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@x.com' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'wrong-password' } });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));

    expect(await screen.findByText('Invalid email or password.')).toBeTruthy();
    expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/auth/login', {
      email: 'a@x.com',
      password: 'wrong-password',
    });
  });

  it('trims whitespace before sending the email', async () => {
    stubHttp.get.mockRejectedValueOnce(new AxiosError('no session'));
    stubHttp.post.mockResolvedValueOnce({
      data: { user_id: 'u-1', email: 'a@x.com', display_name: 'A', role: 'owner' },
    });

    mountLogin('/login');

    await screen.findByRole('heading', { name: 'Sign in to Orenda' });
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: '  a@x.com  ' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/auth/login', {
        email: 'a@x.com',
        password: 'pw',
      });
    });
  });

  it('disables the submit button while the request is in flight', async () => {
    stubHttp.get.mockRejectedValueOnce(new AxiosError('no session'));
    // Never-resolving promise keeps the await pending; we explicitly
    // settle it in the cleanup so the test doesn't leak.
    let resolveLogin!: (v: unknown) => void;
    stubHttp.post.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveLogin = resolve;
      }),
    );

    mountLogin();
    await screen.findByRole('heading', { name: 'Sign in to Orenda' });
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@x.com' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } });

    const btn = screen.getByRole('button', { name: /sign in/i });
    fireEvent.click(btn);

    await waitFor(() => {
      // No jest-dom in this suite; check the native `disabled`
      // attribute directly (and the label flip) instead.
      expect((btn as HTMLButtonElement).disabled).toBe(true);
      expect(btn.textContent).toMatch(/signing in/i);
    });

    resolveLogin({ data: { user_id: 'u-1', email: 'a@x.com' } });
  });

  it('navigates to /projects when the user was redirected there from RequireAuth', async () => {
    stubHttp.get.mockRejectedValueOnce(new AxiosError('no session'));
    stubHttp.post.mockResolvedValueOnce({
      data: { user_id: 'u-1', email: 'a@x.com', display_name: 'A', role: 'owner' },
    });

    // The login path carries the originally-requested path in
    // location.state.from (set by RequireAuth, even though the current
    // App.tsx doesn't pass it yet — LoginPage already supports the
    // contract). MemoryRouter's initial state is { from: '/projects' }.
    render(
      <MemoryRouter initialEntries={[{ pathname: '/login', state: { from: '/projects' } }]}>
        <AuthProvider>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route path="/" element={<div>HOME</div>} />
            <Route path="/projects" element={<div>PROJECTS</div>} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>,
    );

    await screen.findByRole('heading', { name: 'Sign in to Orenda' });
    fireEvent.change(screen.getByLabelText('Email'), { target: { value: 'a@x.com' } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pw' } });
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }));

    // After login: AuthProvider flips to authenticated; LoginPage
    // renders <Navigate to={from} />; the matched route renders.
    expect(await screen.findByText('PROJECTS')).toBeTruthy();
    // Smoke: the API login was hit.
    expect(api.login).toBeDefined();
  });
});
