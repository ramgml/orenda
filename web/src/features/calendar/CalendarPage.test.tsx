// @vitest-environment jsdom
/**
 * CalendarPage thin-coverage smoke tests.
 *
 * `react-big-calendar` is heavy and DOM-coupled; we don't drive its
 * interactions here (those belong in the Playwright E2E suite).
 * What we *do* pin:
 *   - The toolbar buttons (Today, prev/next, view switcher) render
 *     and the view switcher toggles its active state.
 *   - The [+ Create] button opens the EventModal in 'create' mode.
 *   - The error banner appears when /events rejects.
 *   - The WS 'events' topic triggers a re-fetch.
 *   - The default view is 'week'; clicking 'month' updates the title.
 *
 * react-big-calendar's drag-and-drop addon pulls in react-dnd; we
 * accept that the calendar surface itself may render with a warning
 * in jsdom — what matters for these tests is the surrounding chrome.
 */
import { CalendarPage, dropDeadline } from '@/features/calendar/CalendarPage';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { wsClient } from '@/shared/ws';

// Radix UI components (Checkbox, Dialog, Select) use
// @radix-ui/react-use-size which needs ResizeObserver in jsdom.
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

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
});

afterEach(() => {
  cleanup();
});

function stubEmptyList() {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/events') return Promise.resolve({ data: { events: [] } });
    if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
    // Phase 30.8: tasks with a due_at — empty for the smoke tests.
    if (url === '/api/v1/tasks/with-due') return Promise.resolve({ data: { tasks: [] } });
    return Promise.resolve({ data: {} });
  });
}

describe('CalendarPage (chrome only)', () => {
  it('renders the toolbar with the default view (week)', async () => {
    stubEmptyList();

    render(
      <MemoryRouter initialEntries={['/calendar']}>
        <CalendarPage />
      </MemoryRouter>,
    );

    // The toolbar's Today button exists; the sidebar mini-calendar
    // also has its own Today button — both should render.
    const todayButtons = await screen.findAllByRole('button', { name: 'Today' });
    expect(todayButtons.length).toBeGreaterThanOrEqual(1);
    // Prev/Next have no accessible name (the ‹ / › chars); fall back to title.
    expect(screen.getByTitle('Previous')).toBeTruthy();
    expect(screen.getByTitle('Next')).toBeTruthy();
    // View switcher: day/week/month are visible in the toolbar.
    expect(screen.getByRole('button', { name: 'day' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'week' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'month' })).toBeTruthy();
    // [+ Create] is on the toolbar (different from the sidebar's
    // '+ Create event' button, which uses longer text).
    expect(screen.getByRole('button', { name: '+ Create' })).toBeTruthy();
  });

  it('switches the active view when a view button is clicked', async () => {
    stubEmptyList();

    render(
      <MemoryRouter initialEntries={['/calendar']}>
        <CalendarPage />
      </MemoryRouter>,
    );

    await screen.findByRole('button', { name: 'month' });
    // Click 'month' — titleFor('month') returns "August 2026" or
    // similar; the default 'week' returns "Aug 11 – 17, 2026".
    // We assert the title includes the long month name after the
    // click (week format wouldn't match).
    fireEvent.click(screen.getByRole('button', { name: 'month' }));

    await waitFor(() => {
      const titles = screen.getAllByRole('heading', { level: 2 });
      const titleText = titles.map((h) => h.textContent ?? '').join(' ');
      expect(titleText).toMatch(/[A-Z][a-z]+ \d{4}/);
    });
  });

  it('opens the create modal when the toolbar [+ Create] is clicked', async () => {
    stubEmptyList();

    render(
      <MemoryRouter initialEntries={['/calendar']}>
        <CalendarPage />
      </MemoryRouter>,
    );

    // The toolbar button is labelled "+ Create" (the sidebar one
    // says "+ Create event"); using a regex ensures we don't match
    // the sidebar button by accident.
    fireEvent.click(await screen.findByRole('button', { name: /^\+ Create$/ }));

    // EventModal shows up; its title is 'Create event'.
    expect(await screen.findByText('Create event')).toBeTruthy();
  });

  it('shows an error banner when the events endpoint rejects', async () => {
    stubHttp.get.mockRejectedValue(new Error('boom'));

    render(
      <MemoryRouter initialEntries={['/calendar']}>
        <CalendarPage />
      </MemoryRouter>,
    );

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('refetches on a WS "events" event', async () => {
    stubEmptyList();

    render(
      <MemoryRouter initialEntries={['/calendar']}>
        <CalendarPage />
      </MemoryRouter>,
    );
    // The Today button is a cheap anchor that exists once the page
    // settles; don't gate the WS test on it being unique.
    await screen.findAllByRole('button', { name: 'Today' });
    const before = stubHttp.get.mock.calls.length;

    wsClient['listeners'].get('events')?.forEach((fn) => fn({ topic: 'events', body: {} }));

    await waitFor(() => expect(stubHttp.get.mock.calls.length).toBeGreaterThan(before));
  });
});

// ---------------------------------------------------------------------------
// T90: task↔calendar link
// ---------------------------------------------------------------------------

// The month-view grid is where deadlines surface as all-day chips.
// Clicks and drops are dispatched through rbc's onSelectEvent /
// onEventDrop props; the DnD machinery itself stays out of jsdom.
describe('CalendarPage task deadlines (T90)', () => {
  function stubWithTasks(tasks: unknown[]): void {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/events') return Promise.resolve({ data: { events: [] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url === '/api/v1/tasks/with-due') return Promise.resolve({ data: { tasks } });
      return Promise.resolve({ data: {} });
    });
  }

  function renderCalendar(search = '/calendar'): void {
    render(
      <MemoryRouter initialEntries={[search]}>
        <Routes>
          <Route path="/calendar" element={<CalendarPage />} />
          <Route path="/tasks/:id" element={<div>task-page-reached</div>} />
        </Routes>
      </MemoryRouter>,
    );
  }

  it('navigates to /tasks/<id> when a deadline is clicked', async () => {
    stubWithTasks([
      { id: 't-1', title: 'Ship release', status: 'todo', due_at: '2030-01-15T00:00:00.000Z' },
    ]);
    renderCalendar('/calendar?date=2030-01-15');

    fireEvent.click(await screen.findByRole('button', { name: 'month' }));
    const deadline = await waitFor(() => {
      const el = document.querySelector('.rbc-event');
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    expect(deadline.textContent).toContain('Ship release');

    fireEvent.click(deadline);

    expect(await screen.findByText('task-page-reached')).toBeTruthy();
    // The edit modal must not open for task-kind events.
    expect(screen.queryByText('Edit event')).toBeNull();
  });

  it('clicking a plain calendar event still opens the EventModal', async () => {
    stubWithTasks([]);
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/tasks/with-due') return Promise.resolve({ data: { tasks: [] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url === '/api/v1/events')
        return Promise.resolve({
          data: {
            events: [
              {
                id: 'e-1',
                title: 'Standup',
                description: '',
                start_at: '2030-01-15T09:00:00.000Z',
                end_at: '2030-01-15T10:00:00.000Z',
                all_day: false,
              },
            ],
          },
        });
      return Promise.resolve({ data: {} });
    });
    renderCalendar('/calendar?date=2030-01-15');

    const ev = await waitFor(() => {
      const el = document.querySelector('.rbc-event');
      expect(el).not.toBeNull();
      return el as HTMLElement;
    });
    fireEvent.click(ev);

    expect(await screen.findByText('Edit event')).toBeTruthy();
  });
  it('dropping a deadline PATCHes the task due_at, not the event', async () => {
    stubWithTasks([
      { id: 't-2', title: 'Pay rent', status: 'todo', due_at: '2030-01-10T00:00:00.000Z' },
    ]);
    renderCalendar('/calendar?date=2030-01-15');
    fireEvent.click(await screen.findByRole('button', { name: 'month' }));
    await waitFor(() => {
      expect(document.querySelectorAll('.rbc-event').length).toBe(1);
    });
    stubHttp.patch.mockReturnValue(Promise.resolve({ data: {} }));

    // Drop contract, driven through the exported seam the onEventDrop
    // handler itself calls: a drop on Jan 20 must PATCH the TASK's
    // due_at (local midnight of the drop day) and never touch the
    // projected `task-<id>` event row. The pointer-drag machinery is
    // E2E territory; the PATCH routing is the unit-level promise.
    await dropDeadline('t-2', new Date(2030, 0, 20));
    expect(stubHttp.patch).toHaveBeenCalledTimes(1);
    expect(stubHttp.patch).toHaveBeenCalledWith('/api/v1/tasks/t-2', {
      due_at: new Date(2030, 0, 20).toISOString(),
    });
    expect(stubHttp.patch).not.toHaveBeenCalledWith('/api/v1/events/task-t-2', expect.anything());
  });

  it('mutes done deadlines in eventStyleGetter', async () => {
    stubWithTasks([
      { id: 't-open', title: 'Open item', status: 'todo', due_at: '2030-01-15T00:00:00.000Z' },
      { id: 't-done', title: 'Closed item', status: 'done', due_at: '2030-01-15T00:00:00.000Z' },
    ]);
    renderCalendar('/calendar?date=2030-01-15');

    fireEvent.click(await screen.findByRole('button', { name: 'month' }));
    await waitFor(() => {
      expect(document.querySelectorAll('.rbc-event').length).toBe(2);
    });
    const events = Array.from(document.querySelectorAll('.rbc-event')) as HTMLElement[];
    const styles = events.map((el) => el.style);
    const doneIdx = events.findIndex((el) => el.textContent?.includes('Closed item'));
    const openIdx = events.findIndex((el) => el.textContent?.includes('Open item'));
    expect(doneIdx).toBeGreaterThanOrEqual(0);
    expect(openIdx).toBeGreaterThanOrEqual(0);
    // Done: muted (reduced opacity + grey text). Open: full strength.
    expect(styles[doneIdx].opacity).toBe('0.55');
    expect(styles[openIdx].opacity).toBe('');
    expect(styles[doneIdx].color).not.toBe(styles[openIdx].color);
  });

  it('seeds the cursor from ?date= (deep link)', async () => {
    stubEmptyList();
    // 2025-03-15 is a Saturday; the week view (Mon-start) shows
    // Mar 10 – 16. The rbc toolbar label is the cheapest cursor readout.
    renderCalendar('/calendar?date=2025-03-15');

    await screen.findAllByRole('button', { name: 'Today' });
    const label = await screen.findByText(
      (_, el) => el?.classList.contains('rbc-toolbar-label') ?? false,
    );
    expect(label.textContent).toBe('March 10 – 16');
  });

  it('no ?date= keeps the cursor on today', async () => {
    stubEmptyList();
    renderCalendar();

    await screen.findAllByRole('button', { name: 'Today' });
    const label = await screen.findByText(
      (_, el) => el?.classList.contains('rbc-toolbar-label') ?? false,
    );
    // Current month abbreviation must appear (cursor = today).
    expect(label.textContent).toMatch(
      new Intl.DateTimeFormat('en-US', { month: 'long' }).format(new Date()),
    );
  });
});
