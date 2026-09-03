// @vitest-environment jsdom
/**
 * SidebarNav tests (Task 123).
 *
 * The review-badge count used to be fetched once per SidebarNavItem —
 * ten items, and AppLayout renders SidebarNav twice (desktop + mobile),
 * so every WS "tasks" event fired up to 20 GET /review-queue/count
 * calls and a drag burst produced 429s. These tests pin the fixed
 * contract:
 *
 *   - Exactly ONE api.getReviewQueueCount call per SidebarNav (mount).
 *   - A burst of WS "tasks" events collapses into ONE trailing
 *     debounced fetch.
 *   - The badge reflects the fetched count and never flashes "0"
 *     before the first response.
 *   - Unmount inside the debounce window tears the timer down cleanly.
 */
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { SidebarNav } from '@/features/sidebar/SidebarNav';
import { wsClient } from '@/shared/ws';

const { mockGetReviewQueueCount } = vi.hoisted(() => ({
  mockGetReviewQueueCount: vi.fn<() => Promise<{ count: number }>>(),
}));

vi.mock('@/shared/api/client', () => ({
  api: {
    getReviewQueueCount: mockGetReviewQueueCount,
  },
}));

beforeEach(() => {
  vi.clearAllMocks();
  wsClient.disconnect();
  wsClient['listeners'].clear();
});

afterEach(() => {
  cleanup();
  wsClient['listeners'].clear();
});

/** Dispatch a WS "tasks" event straight to the registered listeners. */
function dispatchTasks(): void {
  wsClient['listeners'].get('tasks')?.forEach((fn) => fn({ topic: 'tasks', body: {} }));
}

/**
 * Mount inside the router (NavLink needs it) with a 10ms debounce so
 * the trailing window is far below waitFor's default poll interval.
 */
function mountNav() {
  return render(
    <MemoryRouter>
      <SidebarNav collapsed={false} />
    </MemoryRouter>,
  );
}

describe('SidebarNav review badge (Task 123)', () => {
  it('fetches the count exactly once on mount', async () => {
    mockGetReviewQueueCount.mockResolvedValue({ count: 0 });
    mountNav();

    await waitFor(() => {
      expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
    });
    // Still one after the microtask queue settles.
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 20);
    });
    expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
  });

  it('one WS tasks event triggers exactly one debounced fetch', async () => {
    mockGetReviewQueueCount.mockResolvedValue({ count: 0 });
    mountNav();
    await waitFor(() => {
      expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
    });

    dispatchTasks();

    // During the debounce window no extra fetch has been issued yet.
    expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
    await waitFor(
      () => {
        expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(2);
      },
      { timeout: 1000 },
    );
  });

  it('a burst of 5 WS events collapses into one trailing fetch (2 total with mount)', async () => {
    mockGetReviewQueueCount.mockResolvedValue({ count: 0 });
    mountNav();
    await waitFor(() => {
      expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
    });

    // Five events inside one debounce window — e.g. a series of drags.
    dispatchTasks();
    dispatchTasks();
    dispatchTasks();
    dispatchTasks();
    dispatchTasks();

    await waitFor(
      () => {
        expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(2);
      },
      { timeout: 1000 },
    );
  });

  it('badge shows the fetched count, updates via WS, and hides at zero', async () => {
    mockGetReviewQueueCount.mockResolvedValueOnce({ count: 3 });
    mountNav();

    expect((await screen.findByTestId('review-badge')).textContent).toBe('3');

    // WS event → trailing fetch returns 7.
    mockGetReviewQueueCount.mockResolvedValueOnce({ count: 7 });
    dispatchTasks();
    await waitFor(
      () => {
        expect(screen.getByTestId('review-badge').textContent).toBe('7');
      },
      { timeout: 1000 },
    );

    // WS event → trailing fetch returns 0 → badge disappears.
    mockGetReviewQueueCount.mockResolvedValueOnce({ count: 0 });
    dispatchTasks();
    await waitFor(() => {
      expect(screen.queryByTestId('review-badge')).toBeNull();
    });
  });

  it('does not flash the badge before the first fetch resolves', () => {
    // Never-resolving promise = "response has not arrived yet".
    mockGetReviewQueueCount.mockReturnValue(new Promise<{ count: number }>(() => {}));
    mountNav();

    expect(screen.queryByTestId('review-badge')).toBeNull();
  });

  it('unmount inside the debounce window neither fetches nor warns', async () => {
    const warnSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    mockGetReviewQueueCount.mockResolvedValue({ count: 5 });
    const { unmount } = mountNav();
    await waitFor(() => {
      expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
    });

    dispatchTasks();
    unmount();

    // Give a (non-cleared) timer + the in-flight promise time to blow up.
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 50);
    });
    expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
    const stateUpdates = warnSpy.mock.calls.filter((args) => args.join(' ').includes('setState'));
    expect(stateUpdates).toHaveLength(0);
    warnSpy.mockRestore();
  });

  it('collapsed mode still renders the badge from the shared count', async () => {
    mockGetReviewQueueCount.mockResolvedValueOnce({ count: 3 });
    render(
      <MemoryRouter>
        <SidebarNav collapsed />
      </MemoryRouter>,
    );

    // Collapsed rows show the glyph-only chip without data-testid, so
    // assert via the shared-count fetch + badge text within the Review link.
    await waitFor(() => {
      expect(mockGetReviewQueueCount).toHaveBeenCalledTimes(1);
    });
    expect(screen.getByLabelText('Review').textContent).toContain('3');
  });
});
