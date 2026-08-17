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
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { CalendarPage } from '@/features/calendar/CalendarPage';
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

    render(<CalendarPage />);

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

    render(<CalendarPage />);

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

    render(<CalendarPage />);

    // The toolbar button is labelled "+ Create" (the sidebar one
    // says "+ Create event"); using a regex ensures we don't match
    // the sidebar button by accident.
    fireEvent.click(await screen.findByRole('button', { name: /^\+ Create$/ }));

    // EventModal shows up; its title is 'Create event'.
    expect(await screen.findByText('Create event')).toBeTruthy();
  });

  it('shows an error banner when the events endpoint rejects', async () => {
    stubHttp.get.mockRejectedValue(new Error('boom'));

    render(<CalendarPage />);

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('refetches on a WS "events" event', async () => {
    stubEmptyList();

    render(<CalendarPage />);
    // The Today button is a cheap anchor that exists once the page
    // settles; don't gate the WS test on it being unique.
    await screen.findAllByRole('button', { name: 'Today' });
    const before = stubHttp.get.mock.calls.length;

    wsClient['listeners'].get('events')?.forEach((fn) => fn({ topic: 'events', body: {} }));

    await waitFor(() => expect(stubHttp.get.mock.calls.length).toBeGreaterThan(before));
  });
});
