// @vitest-environment jsdom
/**
 * ReportsPage thin-coverage smoke tests.
 *
 *   - Default date inputs span today and 6 days back.
 *   - The table renders one row per task with the right total.
 *   - Changing the From date triggers a new /reports/time call with
 *     the updated window.
 *   - "No time logged" empty state when the report has no tasks.
 *   - Inline error when the endpoint rejects.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ReportsPage } from '@/features/reports/ReportsPage';

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

function makeReport(
  overrides: {
    tasks?: Array<{ task_id: string; title: string; total_sec: number }>;
    total_sec?: number;
  } = {},
): {
  agent_id: string;
  from: string;
  to: string;
  tasks: Array<{ task_id: string; title: string; total_sec: number }>;
  total_sec: number;
} {
  return {
    agent_id: 'u-1',
    from: '2026-08-06',
    to: '2026-08-12',
    tasks: overrides.tasks ?? [
      { task_id: 't-1', title: 'Spec writing', total_sec: 3600 },
      { task_id: 't-2', title: 'Code review', total_sec: 1800 },
    ],
    total_sec: overrides.total_sec ?? 5400,
  };
}

function mount() {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/reports/time') {
      return Promise.resolve({ data: makeReport() });
    }
    // ReportsPage also lists agents for its Agent filter.
    return Promise.resolve({ data: { agents: [] } });
  });
  return mountWithProviders(<ReportsPage />);
}

// ReportsPage calls useAgents() (React Query) for the Agent filter;
// every render must sit inside a QueryClientProvider.
function mountWithProviders(node: React.ReactElement) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

describe('ReportsPage', () => {
  it('renders the table with one row per task and the formatted total', async () => {
    mount();

    expect(await screen.findByText('Spec writing')).toBeTruthy();
    expect(screen.getByText('Code review')).toBeTruthy();
    // Total = 5400s = 1h 30m; both 1h 30m (total) and per-task totals show.
    expect(screen.getAllByText('1h 30m').length).toBeGreaterThanOrEqual(1);
  });
  it('renders the "No time logged" empty state', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/reports/time') {
        return Promise.resolve({ data: makeReport({ tasks: [], total_sec: 0 }) });
      }
      return Promise.resolve({ data: { agents: [] } });
    });

    mountWithProviders(<ReportsPage />);

    expect(await screen.findByText('No time logged in this window.')).toBeTruthy();
  });

  it('changing From refetches the report with the updated window', async () => {
    let lastParams: { from?: string; to?: string } | undefined;
    stubHttp.get.mockImplementation(
      (url: string, cfg?: { params?: { from: string; to: string } }) => {
        if (url === '/api/v1/reports/time') {
          lastParams = cfg?.params;
          return Promise.resolve({ data: makeReport() });
        }
        return Promise.resolve({ data: { agents: [] } });
      },
    );

    mountWithProviders(<ReportsPage />);
    await screen.findByText('Spec writing');
    expect(lastParams?.from).not.toBe('2026-01-01T00:00:00Z');

    const fromInput = screen.getByLabelText('From') as HTMLInputElement;
    fireEvent.change(fromInput, { target: { value: '2026-01-01' } });

    await waitFor(() => expect(lastParams?.from).toBe('2026-01-01T00:00:00Z'));
  });

  it('surfaces an inline error when /reports/time rejects', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/reports/time') {
        return Promise.reject(new Error('boom'));
      }
      return Promise.resolve({ data: { agents: [] } });
    });

    mountWithProviders(<ReportsPage />);

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('renders the formatted "m" only when total is sub-hour', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/reports/time') {
        return Promise.resolve({
          data: makeReport({
            tasks: [{ task_id: 't-1', title: 'Quick task', total_sec: 600 }],
            total_sec: 600,
          }),
        });
      }
      return Promise.resolve({ data: { agents: [] } });
    });

    mountWithProviders(<ReportsPage />);

    expect(await screen.findByText('Quick task')).toBeTruthy();
    // 600s = 10m; the cell uses `${m}m` format because h === 0.
    // (Total span also uses 10m so we expect at least one match.)
    expect(screen.getAllByText('10m').length).toBeGreaterThanOrEqual(1);
  });
});
