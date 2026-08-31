// @vitest-environment jsdom
/**
 * T86/T95 regression tests — the timer widget must never block the UI.
 *
 * T86: TimerWidget is mounted globally as a fixed bottom-right
 * element. Its container div covered the "Start timer" button in the
 * task sidebar, so the click landed on the widget and
 * POST /tasks/:id/timer/start was never sent (zero hits in 12 days
 * of access logs). The container is click-transparent
 * (pointer-events-none); only the active card opts back into pointer
 * events.
 *
 * T95: the widget previously rendered an always-visible "No active
 * timer" empty card in the same fixed bottom-right corner as the
 * QuickCapture FAB (+), visually covering it. Since T95 the widget
 * renders NOTHING when there is no active timer and no error — the
 * corner belongs to the FAB whenever the timer is off.
 *
 * jsdom has no layout engine and does not load the Tailwind
 * stylesheet, so real hit-testing can't be exercised here — these
 * tests pin the observable contract: empty-state renders nothing,
 * the compiled .pointer-events-{none,auto} utilities on the visible
 * states, and Stop behaviour end-to-end within the component. The
 * true elementFromPoint check over the built UI lives in the PR
 * evidence (headless Chrome).
 *
 *   1. Empty state (no active timer, no error) renders nothing — no
 *      card to cover the FAB (T95).
 *   2. The fixed container is click-transparent (pointer-events-none)
 *      in the active state — a click through its area can only land
 *      on the element underneath.
 *   3. The active card opts back in (pointer-events-auto) so Stop
 *      stays clickable, and clicking it actually stops the timer.
 *   4. An error renders the widget (with pointer-events-auto on the
 *      error banner) even without an active timer.
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import assert from 'node:assert/strict';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TimerWidget } from '@/features/tasks/TimerWidget';

vi.mock('@/features/auth/AuthContext', () => ({
  useAuth: () => ({
    user: { user_id: 'u-me', email: 'me@x.io', display_name: 'Me' },
    status: 'authenticated',
    login: vi.fn(),
    logout: vi.fn(),
    refresh: vi.fn(),
  }),
}));

const stopTimer = vi.fn((_taskId: string) => Promise.resolve({}));

vi.mock('@/shared/api/client', () => ({
  api: {
    startTimer: vi.fn(() => Promise.resolve({})),
    stopTimer: (taskId: string) => stopTimer(taskId),
  },
}));

afterEach(() => {
  // globals:false → RTL auto-cleanup is disabled; tests share the DOM
  // otherwise and "found multiple elements" would poison the suite.
  document.body.replaceChildren();
});

beforeEach(() => {
  localStorage.clear();
});

describe('TimerWidget (T86 pointer events / T95 no empty card)', () => {
  it('renders nothing when there is no active timer and no error (T95)', () => {
    const { container } = render(<TimerWidget />);
    // No idle card at all: the fixed bottom-right corner belongs to
    // the QuickCapture FAB.
    expect(container.childElementCount).toBe(0);
    expect(screen.queryByText(/No active timer/)).toBeNull();
  });

  it('container is click-transparent in the active state', () => {
    localStorage.setItem(
      'orenda.activeTimer',
      JSON.stringify({
        taskId: 'task-1',
        taskTitle: 'Tracked task',
        startedAt: new Date(Date.now() - 65_000).toISOString(),
      }),
    );
    render(<TimerWidget />);
    const container = screen.getByText('Timer on').closest('.fixed');
    assert(container, 'widget container not found');
    expect(container.className).toMatch(/\bpointer-events-none\b/);
  });

  it('active card opts back into pointer events (Stop clickable)', async () => {
    localStorage.setItem(
      'orenda.activeTimer',
      JSON.stringify({
        taskId: 'task-1',
        taskTitle: 'Tracked task',
        startedAt: new Date(Date.now() - 65_000).toISOString(),
      }),
    );
    render(<TimerWidget />);

    const card = screen.getByText('Timer on').closest('.pointer-events-auto');
    expect(card).not.toBeNull();

    // And stopping actually works through the card: fires the API
    // call and dismisses the widget entirely (no empty card anymore).
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(stopTimer).toHaveBeenCalledWith('task-1');
    // setActive(null) lands after the API promise resolves; the
    // widget then unmounts instead of showing the idle card (T95).
    await waitFor(() => expect(document.querySelector('.fixed')).toBeNull());
    expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();
  });

  it('error renders the widget even without an active timer', async () => {
    // Error state is unreachable without an API failure; simulate it
    // by making stopTimer reject while an active timer exists, then
    // stopping.
    localStorage.setItem(
      'orenda.activeTimer',
      JSON.stringify({
        taskId: 'task-1',
        taskTitle: 'Tracked task',
        startedAt: new Date(Date.now() - 65_000).toISOString(),
      }),
    );
    stopTimer.mockRejectedValueOnce(new Error('backend down'));
    render(<TimerWidget />);
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    // Widget stays visible with the error banner, click-transparent
    // container, pointer-events-auto banner. setState lands after the
    // rejected promise, so wait for it.
    const banner = await screen.findByText('backend down');
    expect(banner.className).toMatch(/\bpointer-events-auto\b/);
    const container = banner.closest('.fixed');
    assert(container, 'widget container not found');
    expect(container.className).toMatch(/\bpointer-events-none\b/);
    // And once the error is gone the widget disappears again.
    expect(screen.queryByText(/No active timer/)).toBeNull();
  });
});
