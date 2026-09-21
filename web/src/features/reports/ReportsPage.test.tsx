// @vitest-environment jsdom
/**
 * ReportsPage tests.
 *
 *   - Default date inputs span today and 6 days back.
 *   - The table renders one row per task with the right total.
 *   - Changing the From date triggers a new /reports/time call with
 *     the updated window.
 *   - "No time logged" empty state when the report has no tasks.
 *   - Inline error when the endpoint rejects.
 *
 * T354:
 *   - One section header per project (dot colour + name + subtotal),
 *     ordered by project total desc.
 *   - Task bars render in the project colour (inline style).
 *   - Projectless tasks land in a trailing "No project" section.
 *   - Empty project_color falls back to the neutral default.
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

interface ReportRow {
  task_id?: string;
  title?: string;
  project_id?: string;
  project_name?: string;
  project_color?: string;
  total_sec: number;
}

interface Report {
  agent_id: string;
  from: string;
  to: string;
  projects?: ReportRow[];
  tasks: ReportRow[];
  total_sec: number;
}

function makeReport(
  overrides: {
    tasks?: ReportRow[];
    projects?: ReportRow[];
    total_sec?: number;
  } = {},
): Report {
  return {
    agent_id: 'u-1',
    from: '2026-08-06',
    to: '2026-08-12',
    projects: overrides.projects ?? [],
    tasks: overrides.tasks ?? [
      { task_id: 't-1', title: 'Spec writing', total_sec: 3600 },
      { task_id: 't-2', title: 'Code review', total_sec: 1800 },
    ],
    total_sec: overrides.total_sec ?? 5400,
  };
}

function mount(report: Report = makeReport()) {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/reports/time') {
      return Promise.resolve({ data: report });
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

/** The inline background colour of the nth project dot. */
function dotColorAt(index: number): string {
  const dots = document.querySelectorAll<HTMLElement>('[data-testid="project-dot"]');
  expect(dots.length).toBeGreaterThan(index);
  return dots[index]!.style.backgroundColor;
}

/** The inline background colour of the first distribution bar. */
function firstBarColor(): string {
  const bars = document.querySelectorAll<HTMLElement>('div[aria-label*=" of "]');
  expect(bars.length).toBeGreaterThan(0);
  return bars[0]!.style.backgroundColor;
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
    mount(makeReport({ tasks: [], total_sec: 0 }));

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

    // A later successful refetch (e.g. window or agent change) must
    // clear the stale error banner.
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/reports/time') {
        return Promise.resolve({ data: makeReport() });
      }
      return Promise.resolve({ data: { agents: [] } });
    });
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-01-01' } });

    await waitFor(() => expect(screen.queryByText('boom')).toBeNull());
    expect(await screen.findByText('Spec writing')).toBeTruthy();
  });

  it('renders the formatted "m" only when total is sub-hour', async () => {
    mount(
      makeReport({
        tasks: [{ task_id: 't-1', title: 'Quick task', total_sec: 600 }],
        total_sec: 600,
      }),
    );

    expect(await screen.findByText('Quick task')).toBeTruthy();
    // 600s = 10m; the cell uses `${m}m` format because h === 0.
    // (Total span also uses 10m so we expect at least one match.)
    expect(screen.getAllByText('10m').length).toBeGreaterThanOrEqual(1);
  });

  it('groups tasks into project sections sorted by project total desc', async () => {
    mount(
      makeReport({
        projects: [
          {
            project_id: 'p-alpha',
            project_name: 'Alpha',
            project_color: '#0000ff',
            total_sec: 5400,
          },
          { project_id: 'p-beta', project_name: 'Beta', project_color: '#ff0000', total_sec: 7200 },
        ],
        tasks: [
          {
            task_id: 't-1',
            title: 'Alpha one',
            project_id: 'p-alpha',
            project_name: 'Alpha',
            project_color: '#0000ff',
            total_sec: 3600,
          },
          {
            task_id: 't-2',
            title: 'Alpha two',
            project_id: 'p-alpha',
            project_name: 'Alpha',
            project_color: '#0000ff',
            total_sec: 1800,
          },
          {
            task_id: 't-3',
            title: 'Beta one',
            project_id: 'p-beta',
            project_name: 'Beta',
            project_color: '#ff0000',
            total_sec: 7200,
          },
        ],
        total_sec: 12600,
      }),
    );

    expect(await screen.findByText('Alpha one')).toBeTruthy();

    // Beta (2h) outscores Alpha (1h 30m) → Beta section first.
    const headers = screen.getAllByTestId('project-dot');
    expect(headers.length).toBe(2);
    const order = screen
      .getAllByTestId('project-dot')
      .map((dot) => dot.nextElementSibling?.textContent);
    expect(order).toEqual(['Beta', 'Alpha']);
    // Section subtotals show next to the project name.
    expect(screen.getAllByText('2h 0m').length).toBeGreaterThanOrEqual(1);
  });

  it('renders the section dot and task bar in the project colour', async () => {
    mount(
      makeReport({
        projects: [
          {
            project_id: 'p-1',
            project_name: 'Colourful',
            project_color: '#ff0000',
            total_sec: 600,
          },
        ],
        tasks: [
          {
            task_id: 't-1',
            title: 'Tinted task',
            project_id: 'p-1',
            project_name: 'Colourful',
            project_color: '#ff0000',
            total_sec: 600,
          },
        ],
        total_sec: 600,
      }),
    );

    expect(await screen.findByText('Tinted task')).toBeTruthy();
    expect(dotColorAt(0)).toBe('rgb(255, 0, 0)');
    expect(firstBarColor()).toBe('rgb(255, 0, 0)');
  });

  it('puts projectless tasks in a trailing "No project" section', async () => {
    mount(
      makeReport({
        projects: [
          { project_id: 'p-1', project_name: 'Alpha', project_color: '#0000ff', total_sec: 600 },
        ],
        tasks: [
          {
            task_id: 't-1',
            title: 'Claimed task',
            project_id: 'p-1',
            project_name: 'Alpha',
            project_color: '#0000ff',
            total_sec: 600,
          },
          { task_id: 't-2', title: 'Orphan task', total_sec: 300 },
        ],
        total_sec: 900,
      }),
    );

    expect(await screen.findByText('Orphan task')).toBeTruthy();
    const order = screen
      .getAllByTestId('project-dot')
      .map((dot) => dot.nextElementSibling?.textContent);
    expect(order).toEqual(['Alpha', 'No project']);
    // The orphan subtotal and its row total both show 5m.
    expect(screen.getAllByText('5m').length).toBeGreaterThanOrEqual(2);
  });

  it('falls back to the neutral default colour when project_color is empty', async () => {
    mount(
      makeReport({
        projects: [{ project_id: 'p-1', project_name: 'Uncoloured', total_sec: 600 }],
        tasks: [
          {
            task_id: 't-1',
            title: 'Plain task',
            project_id: 'p-1',
            project_name: 'Uncoloured',
            total_sec: 600,
          },
        ],
        total_sec: 600,
      }),
    );

    expect(await screen.findByText('Plain task')).toBeTruthy();
    expect(dotColorAt(0)).toBe('rgb(148, 163, 184)');
    expect(firstBarColor()).toBe('rgb(148, 163, 184)');
  });
});
