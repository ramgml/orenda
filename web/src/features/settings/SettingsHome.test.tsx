// @vitest-environment jsdom
/**
 * SettingsHome (Phase 28.2) thin-coverage smoke tests.
 *
 * Goals:
 *  - The hub page renders all four navigation cards with their
 *    correct `to` paths, so a click from the sidebar lands the
 *    user on the right sub-page.
 *  - The About block pulls /api/v1/stats and renders the values
 *    we expect humans to look at (uptime, DB size, request count,
 *    WS connections).
 *  - When the stats endpoint fails, the About block still renders
 *    gracefully with "—" placeholders.
 */
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';

import { SettingsHome } from '@/features/settings/SettingsHome';

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

function stubStats(stats: object | null, fail = false): void {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/stats') {
      if (fail) return Promise.reject(new Error('boom'));
      return Promise.resolve({ data: stats });
    }
    return Promise.resolve({ data: {} });
  });
}

function renderHome(): ReturnType<typeof render> {
  // MemoryRouter keeps <Link> happy; we don't navigate within
  // these tests — they assert on the rendered href. The '/' entry
  // is the default so we're not asserting on redirect behaviour.
  return render(
    <MemoryRouter>
      <SettingsHome />
    </MemoryRouter>,
  );
}

describe('SettingsHome', () => {
  it('renders all four navigation cards with the correct hrefs', async () => {
    stubStats({
      uptime_seconds: 0,
      requests_total: 0,
      requests_2xx: 0,
      requests_3xx: 0,
      requests_4xx: 0,
      requests_5xx: 0,
      slow_requests: 0,
      ws_connections: 0,
      db_bytes: 0,
      db_path: '/tmp/test.db',
    });

    renderHome();

    // Cards render as <Link> → <a> with hrefs the operator can
    // bookmark / share. Pin the four destinations so a future
    // rename of the Settings tree doesn't silently break the hub.
    const backups = await screen.findByTestId('settings-card-backups');
    expect(backups.getAttribute('href')).toBe('/settings/backups');
    const bots = screen.getByTestId('settings-card-bots');
    expect(bots.getAttribute('href')).toBe('/settings/bots');
    const agents = screen.getByTestId('settings-card-agents');
    expect(agents.getAttribute('href')).toBe('/agents');
    const reports = screen.getByTestId('settings-card-reports');
    expect(reports.getAttribute('href')).toBe('/reports');

    // The titles render too — that's the operator-facing label.
    expect(screen.getByText('Backups')).toBeTruthy();
    expect(screen.getByText('Bots & notifications')).toBeTruthy();
    expect(screen.getByText('Agents')).toBeTruthy();
    expect(screen.getByText('Reports')).toBeTruthy();
  });

  it('renders About stats once /api/v1/stats resolves', async () => {
    stubStats({
      // 2 days, 3 hours, 4 minutes = 183844 seconds
      uptime_seconds: 2 * 86400 + 3 * 3600 + 4 * 60,
      requests_total: 1234,
      requests_2xx: 1100,
      requests_3xx: 0,
      requests_4xx: 100,
      requests_5xx: 34,
      slow_requests: 2,
      ws_connections: 3,
      db_bytes: 5 * 1024 * 1024, // 5 MiB
      db_path: '/tmp/test.db',
    });

    renderHome();

    // Uptime → "2d 3h" (4m rounds down below the hour threshold).
    const uptime = await screen.findByTestId('about-uptime');
    expect(uptime.textContent).toBe('2d 3h');

    // DB size → "5.0 MiB".
    const db = screen.getByTestId('about-db');
    expect(db.textContent).toBe('5.0 MiB');

    // Request count formatted with locale grouping.
    const req = screen.getByTestId('about-requests');
    expect(req.textContent).toBe('1,234');

    // WS connections rendered verbatim.
    expect(screen.getByTestId('about-ws').textContent).toBe('3');
  });

  it('renders About placeholders when /api/v1/stats fails', async () => {
    stubStats(null, /* fail */ true);

    renderHome();

    // Wait one tick for the rejected promise to settle — we don't
    // want a flash of "0" before the catch handler runs.
    await waitFor(() => {
      expect(stubHttp.get).toHaveBeenCalledWith('/api/v1/stats');
    });
    // The cards still render; the About block stays "—".
    expect(screen.getByTestId('settings-card-backups')).toBeTruthy();
    expect(screen.getByTestId('about-uptime').textContent).toBe('—');
    expect(screen.getByTestId('about-db').textContent).toBe('—');
    expect(screen.getByTestId('about-requests').textContent).toBe('—');
    expect(screen.getByTestId('about-ws').textContent).toBe('—');
  });

  it('formats small byte values without a unit suffix and large values as GiB', async () => {
    // Edge cases for formatBytes: < 1 KiB → bytes, >= 1 GiB → GiB.
    stubStats({
      uptime_seconds: 0,
      requests_total: 0,
      requests_2xx: 0,
      requests_3xx: 0,
      requests_4xx: 0,
      requests_5xx: 0,
      slow_requests: 0,
      ws_connections: 0,
      db_bytes: 512, // < 1 KiB
      db_path: '/tmp/test.db',
    });

    renderHome();

    const db = await screen.findByTestId('about-db');
    expect(db.textContent).toBe('512 B');
  });
});
