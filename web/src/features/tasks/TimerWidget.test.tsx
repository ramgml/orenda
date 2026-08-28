// @vitest-environment jsdom
/**
 * T86 regression tests — the timer widget must never block the UI.
 *
 * Symptom: TimerWidget is mounted globally as a fixed bottom-right
 * element. Its container div covered the "Start timer" button in the
 * task sidebar, so the click landed on the widget and
 * POST /tasks/:id/timer/start was never sent (zero hits in 12 days
 * of access logs).
 *
 * jsdom has no layout engine and does not load the Tailwind
 * stylesheet, so real hit-testing can't be exercised here — these
 * tests pin the classes the browser hit-tester consumes (the
 * compiled .pointer-events-{none,auto} utilities), plus the Stop
 * behaviour end-to-end within the component. The true
 * elementFromPoint check over the built UI lives in the PR evidence
 * (headless Chrome).
 *
 *   1. The fixed container is click-transparent (pointer-events-none)
 *      in BOTH states — a click through its area can only land on the
 *      element underneath.
 *   2. The active card opts back in (pointer-events-auto) so Stop
 *      stays clickable, and clicking it actually stops the timer.
 *   3. The empty card stays inert (no pointer-events opt-in).
 */
import { fireEvent, render, screen } from '@testing-library/react';
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

describe('TimerWidget (T86 pointer events)', () => {
  it('container is click-transparent in the empty state', () => {
    render(<TimerWidget />);
    // The container is the parent of the empty card.
    const emptyCard = screen.getByText(/No active timer/);
    const container = emptyCard.parentElement;
    assert(container, 'widget container not found');
    expect(container.className).toMatch(/\bpointer-events-none\b/);
    expect(container.className).toMatch(/\bfixed\b/);
  });

  it('container is click-transparent in the active state too', () => {
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
    // call and dismisses the widget to its empty state.
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(stopTimer).toHaveBeenCalledWith('task-1');
    // setActive(null) lands after the API promise resolves.
    await screen.findByText(/No active timer/);
    expect(screen.queryByRole('button', { name: 'Stop' })).toBeNull();
  });

  it('empty card itself stays inert (no pointer-events opt-in)', () => {
    render(<TimerWidget />);
    const emptyCard = screen.getByText(/No active timer/);
    expect(emptyCard.className).not.toMatch(/\bpointer-events-auto\b/);
    // The card sits inside the click-transparent container and opts
    // itself out of hit-testing too.
    expect(emptyCard.className).not.toMatch(/\bpointer-events-none\b/);
  });
});
