// @vitest-environment jsdom
/**
 * DashboardPage component tests (Task 107).
 *
 * Pins the contracts that matter for the system-readings screen:
 *   - Loading state renders the placeholder.
 *   - Metric cards render the API values (projects / tasks / wiki / events).
 *   - Tasks-by-status chips mirror tasks_by_status.
 *   - The activity chart renders one bar pair per activity day.
 *   - The 7d/30d range switch filters the series (interactive chart).
 *   - Hovering a bar shows the tooltip with that day's values.
 */
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { DashboardPage } from '@/features/dashboard/DashboardPage';

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

// 30 days ending today; day i has created=i+1, completed=i.
function makeActivity(days: number) {
  const out = [];
  for (let i = 0; i < days; i++) {
    const d = new Date();
    d.setDate(d.getDate() - (days - 1 - i));
    out.push({ date: d.toISOString().slice(0, 10), created: i + 1, completed: i });
  }
  return out;
}

const overview = {
  projects: 3,
  tasks_by_status: { todo: 5, done: 2 },
  wiki_pages: 7,
  events: 11,
  activity: makeActivity(30),
};

function mount() {
  return render(
    <MemoryRouter>
      <DashboardPage />
    </MemoryRouter>,
  );
}

describe('DashboardPage', () => {
  it('renders the loading placeholder while /overview is in flight', () => {
    stubHttp.get.mockReturnValue(new Promise(() => {}));
    mount();
    expect(document.querySelector('[data-testid="dashboard-page"]')).toBeNull();
  });

  it('renders metric cards with the API values', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: overview });
    mount();

    expect(await screen.findByTestId('dashboard-page')).toBeTruthy();
    const values = screen.getAllByTestId('metric-value').map((el) => el.textContent);
    expect(values).toEqual(['3', '7', '7', String(overview.events)]);
    expect(screen.getByTestId('metric-projects')).toBeTruthy();
    expect(screen.getByTestId('status-todo').textContent).toContain('5');
    expect(screen.getByTestId('status-done').textContent).toContain('2');
  });

  it('renders one bar per activity day (30d default) and switches to 7d', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: overview });
    mount();

    await screen.findByTestId('dashboard-page');
    expect(stubHttp.get).toHaveBeenCalledWith('/api/v1/overview');
    expect(document.querySelectorAll('[data-testid^="bar-"]').length).toBe(30);

    fireEvent.click(screen.getByRole('button', { name: '7d' }));
    expect(document.querySelectorAll('[data-testid^="bar-"]').length).toBe(7);
  });

  it('shows the tooltip with the hovered day values', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: overview });
    mount();

    await screen.findByTestId('dashboard-page');
    const lastDay = overview.activity[overview.activity.length - 1];
    fireEvent.mouseEnter(screen.getByTestId(`bar-${lastDay.date}`));

    const tip = screen.getByTestId('chart-tooltip');
    expect(tip.textContent).toContain(lastDay.date);
    expect(tip.textContent).toContain(`created: ${lastDay.created}`);
    expect(tip.textContent).toContain(`completed: ${lastDay.completed}`);
  });

  it('renders the error banner when /overview rejects', async () => {
    stubHttp.get.mockRejectedValueOnce(new Error('boom'));
    mount();
    expect(await screen.findByText(/Failed to load overview: boom/)).toBeTruthy();
  });
});
