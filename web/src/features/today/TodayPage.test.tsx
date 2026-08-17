// @vitest-environment jsdom
/**
 * TodayPage component tests.
 *
 * Pins the contracts that matter for the daily-driver screen:
 *   - Loading state renders the placeholder.
 *   - Empty state ("Day is clear") shows when all sections are empty.
 *   - Sections (Overdue / Due today / Scheduled today) render with
 *     the right colours and counts.
 *   - awaiting_count > 0 surfaces a /review banner with the count.
 *   - active_timer renders the elapsed-time row.
 *   - upcoming_week renders one row per day with a count badge.
 *   - WS 'tasks' events trigger a re-fetch (the today list
 *     reflects tasks across every project).
 *
 * The WebSocket client is exercised via its `wsClient.on(topic, fn)`
 * API directly — no real socket needed; we just register a listener
 * the same way useWebSocketTopic does and verify it gets called.
 */
import { AxiosError } from 'axios';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TodayPage } from '@/features/today/TodayPage';
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
});

afterEach(() => {
  cleanup();
});

function mount() {
  return render(
    <MemoryRouter>
      <TodayPage />
    </MemoryRouter>,
  );
}

function makeTask(overrides: Partial<{
  id: string;
  title: string;
  study_course_id: string;
}> = {}): {
  id: string;
  title: string;
  project_id: string;
  column_id: string | null;
  status: string;
  priority: string;
  awaiting: string;
  time_spent_s: number;
  position: number;
  color: string;
  study_course_id?: string;
  created_at: string;
  updated_at: string;
} {
  return {
    id: 'task-1',
    title: 'Task title',
    project_id: '',
    column_id: null,
    status: 'todo',
    priority: 'medium',
    awaiting: 'none',
    time_spent_s: 0,
    position: 0,
    color: '',
    created_at: '2026-08-12T00:00:00Z',
    updated_at: '2026-08-12T00:00:00Z',
    ...overrides,
  };
}

const emptyToday = {
  overdue: [],
  due_today: [],
  scheduled_today: [],
  upcoming_week: [],
  awaiting_count: 0,
  proposals: [],
};

describe('TodayPage', () => {
  it('renders the loading placeholder while /today is in flight', () => {
    // Never-resolving promise keeps the page in its initial loading state.
    stubHttp.get.mockReturnValue(new Promise(() => {}));

    mount();

    expect(screen.getByText('Loading…')).toBeTruthy();
  });

  it('renders the empty state when nothing is owed', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: emptyToday });

    mount();

    expect(await screen.findByText(/Day is clear\./)).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Today' })).toBeTruthy();
  });

  it('shows the "X things need attention" header and three section counts', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        overdue: [makeTask({ id: 'o1', title: 'Overdue one' })],
        due_today: [
          makeTask({ id: 'd1', title: 'Due one' }),
          makeTask({ id: 'd2', title: 'Due two' }),
        ],
        scheduled_today: [],
        upcoming_week: [],
        awaiting_count: 0,
        proposals: [],
      },
    });

    mount();

    // Header counts the total of all three sections (1 + 2 + 0 = 3).
    expect(await screen.findByText('3 things need attention.')).toBeTruthy();
    // Section titles include their own count in parens.
    expect(screen.getByText('Overdue (1)')).toBeTruthy();
    expect(screen.getByText('Due today (2)')).toBeTruthy();
    expect(screen.getByText('Scheduled today (0)')).toBeTruthy();
  });

  it('renders the awaiting banner linking to /review when count > 0', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        overdue: [],
        due_today: [],
        scheduled_today: [],
        upcoming_week: [],
        awaiting_count: 3,
        proposals: [],
      },
    });

    mount();

    // The banner text is "⏳ 3 task[s] awaiting your review → /review"
    // with the count inside <strong>; match on the tail so the
    // multi-node split doesn't break the assertion.
    const banner = await screen.findByText(/awaiting your review/);
    expect(banner).toBeTruthy();
    expect(banner.closest('a')?.getAttribute('href')).toBe('/review');
  });

  it('does not render the awaiting banner when awaiting_count is 0', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: emptyToday });

    mount();

    await screen.findByText(/Day is clear\./);
    expect(screen.queryByText(/awaiting your review/)).toBeNull();
  });

  it('renders the active timer row when active_timer is present', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        ...emptyToday,
        active_timer: { task_id: 'abc12345-deadbeef', started_at: '2026-08-12T10:00:00Z' },
      },
    });

    mount();

    expect(await screen.findByTestId('active-timer-row')).toBeTruthy();
  });

  it('renders one upcoming_week row per date with the due count', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        ...emptyToday,
        upcoming_week: [
          { date: '2026-08-13', count: 2 },
          { date: '2026-08-14', count: 1 },
        ],
      },
    });

    mount();

    const rows = await screen.findAllByTestId('upcoming-day-row');
    expect(rows.length).toBe(2);
    expect(rows[0].textContent).toContain('2 due');
    expect(rows[1].textContent).toContain('1 due');
  });

  it('renders the error message when /today rejects', async () => {
    stubHttp.get.mockRejectedValueOnce(new AxiosError('boom'));

    mount();

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('re-fetches when a WS "tasks" event arrives', async () => {
    // First call: empty payload; second call (after WS event): same.
    stubHttp.get.mockResolvedValue({ data: emptyToday });

    // wsClient is a module singleton; pre-clear the 'tasks' topic so
    // we only count the listener that TodayPage itself registers.
    wsClient.disconnect();
    const before = stubHttp.get.mock.calls.length;

    mount();
    await screen.findByText(/Day is clear\./);

    // TodayPage subscribed to the 'tasks' topic via useWebSocketTopic;
    // dispatching that topic via the singleton should trigger load().
    wsClient['listeners'].get('tasks')?.forEach((fn) => fn({ topic: 'tasks', body: {} }));

    await waitFor(() => expect(stubHttp.get.mock.calls.length).toBeGreaterThan(before + 1));
  });

  // --- Phase 31.9: study proposals tray ---
  //
  // The tray is the Dashboard-side of the planner → user loop.
  // Phase 31.5/31.6 added the API; here we test the render + accept
  // and dismiss button wiring (no real network — the test just
  // verifies the right endpoint is called and the tray re-fetches
  // afterwards).

  it('renders the proposal tray with each pending proposal', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        ...emptyToday,
        proposals: [
          {
            id: 'p1',
            course_id: 'c-1',
            title: 'Read chapter 5',
            body_md: 'rust-book chapter 5',
            target_date: '2099-08-17',
            agent_id: 'a-planner',
            created_at: '2026-08-17T00:00:00Z',
          },
          {
            id: 'p2',
            title: 'Free-standing',
            target_date: '2099-08-17',
            agent_id: 'a-planner',
            created_at: '2026-08-17T00:00:00Z',
          },
        ],
      },
    });

    mount();

    const tray = await screen.findByTestId('proposal-tray');
    expect(tray).toBeTruthy();
    const cards = screen.getAllByTestId('proposal-card');
    expect(cards.length).toBe(2);
    expect(cards[0].textContent).toContain('Read chapter 5');
    expect(cards[0].textContent).toContain('rust-book chapter 5');
    expect(cards[0].textContent).toContain('open course');
    expect(cards[1].textContent).toContain('Free-standing');
    expect(cards[1].textContent).not.toContain('open course'); // no course_id
  });

  it('does not render the tray when proposals is empty', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: emptyToday });
    mount();
    await screen.findByText(/Day is clear\./);
    expect(screen.queryByTestId('proposal-tray')).toBeNull();
  });

  it('accept button calls POST /study-proposals/{id}/accept and re-fetches', async () => {
    let getCallCount = 0;
    stubHttp.get.mockImplementation(async () => {
      getCallCount++;
      return {
        data: {
          ...emptyToday,
          proposals:
            getCallCount === 1
              ? [
                  {
                    id: 'p-accept',
                    title: 'Read chapter 5',
                    target_date: '2099-08-17',
                    agent_id: 'a-planner',
                    created_at: '2026-08-17T00:00:00Z',
                  },
                ]
              : [],
        },
      };
    });
    stubHttp.post.mockResolvedValueOnce({ data: { ok: true } });

    mount();
    await screen.findByTestId('proposal-card');

    const beforeAccept = getCallCount;
    const acceptBtn = screen.getByTestId('proposal-accept');
    acceptBtn.click();

    // POST fires to /accept.
    await waitFor(() =>
      expect(stubHttp.post).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/study-proposals/p-accept/accept'),
      ),
    );

    // Re-fetch happened (parent load() re-runs).
    await waitFor(() => expect(getCallCount).toBeGreaterThan(beforeAccept));
  });

  it('dismiss button calls POST /study-proposals/{id}/dismiss and re-fetches', async () => {
    let getCallCount = 0;
    stubHttp.get.mockImplementation(async () => {
      getCallCount++;
      return {
        data: {
          ...emptyToday,
          proposals:
            getCallCount === 1
              ? [
                  {
                    id: 'p-dismiss',
                    title: 'Skip this',
                    target_date: '2099-08-17',
                    agent_id: 'a-planner',
                    created_at: '2026-08-17T00:00:00Z',
                  },
                ]
              : [],
        },
      };
    });
    stubHttp.post.mockResolvedValueOnce({ data: { ok: true } });

    mount();
    await screen.findByTestId('proposal-card');

    const beforeDismiss = getCallCount;
    screen.getByTestId('proposal-dismiss').click();

    await waitFor(() =>
      expect(stubHttp.post).toHaveBeenCalledWith(
        expect.stringContaining('/api/v1/study-proposals/p-dismiss/dismiss'),
      ),
    );
    await waitFor(() => expect(getCallCount).toBeGreaterThan(beforeDismiss));
  });

  // --- Phase 31.9: study-marker on study-reminders ---
  //
  // Today carries 📖 for tasks with study_course_id set — that's the
  // single visual distinction from a regular project task in the
  // same section.

  it('marks study-reminders with 📖 and a course link', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        ...emptyToday,
        due_today: [
          makeTask({
            id: 'reminder-1',
            title: 'Read chapter 5',
            study_course_id: 'c-rust',
          }),
        ],
      },
    });

    mount();

    const li = await screen.findByTestId('today-study-task');
    expect(li.textContent).toContain('📖');
    expect(li.textContent).toContain('Read chapter 5');

    const marker = screen.getByTestId('today-study-marker');
    expect(marker.closest('a')?.getAttribute('href')).toBe('/courses/c-rust');
  });

  it('does not mark regular tasks (no study_course_id)', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        ...emptyToday,
        due_today: [
          makeTask({ id: 'regular-1', title: 'Bug fix' }), // no study_course_id
        ],
      },
    });

    mount();

    const li = await screen.findByTestId('today-task');
    expect(li.textContent).not.toContain('📖');
    expect(li.textContent).toContain('Bug fix');
    expect(screen.queryByTestId('today-study-marker')).toBeNull();
  });

  // --- Phase 31.9: invalidation on WS task events ---
  //
  // The tray is part of /today's payload, so the existing WS
  // subscription on 'tasks' is enough — no separate hook needed.
  // This test pins that a fresh proposal added by the agent via
  // a study.proposed event shows up on the next fetch.

  it('re-fetches the tray when a WS task event arrives', async () => {
    let getCallCount = 0;
    stubHttp.get.mockImplementation(async () => {
      getCallCount++;
      return {
        data: {
          ...emptyToday,
          proposals:
            getCallCount === 1
              ? []
              : [
                  {
                    id: 'p-new',
                    title: 'Newly proposed',
                    target_date: '2099-08-17',
                    agent_id: 'a-planner',
                    created_at: '2026-08-17T00:00:00Z',
                  },
                ],
        },
      };
    });

    wsClient.disconnect();
    const before = getCallCount;
    mount();
    await screen.findByText(/Day is clear\./);

    // Dispatch a WS event to trigger the existing useWebSocketTopic
    // subscription. The handler calls load() which re-fetches; the
    // second call returns one proposal.
    wsClient['listeners']
      .get('tasks')
      ?.forEach((fn) => fn({ topic: 'tasks', body: { proposal_id: 'p-new' } }));

    await waitFor(() => expect(getCallCount).toBeGreaterThan(before));
    expect(await screen.findByTestId('proposal-card')).toBeTruthy();
  });
});
