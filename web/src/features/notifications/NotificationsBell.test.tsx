// @vitest-environment jsdom
/**
 * NotificationsBell component tests.
 *
 * Top-bar bell with unread badge and dropdown of recent notifications.
 * Pins the contracts that matter:
 *   - Badge shows the unread count (or "99+"); hidden when zero.
 *   - Clicking the bell toggles the dropdown.
 *   - Outside click closes the dropdown.
 *   - "mark read" calls the api and refreshes the list.
 *   - WS "notifications" event triggers a re-fetch.
 *   - Notifications with `read_at === null` show the mark-read
 *     button; read ones don't.
 *   - Empty list shows a "No notifications." placeholder.
 *   - The `link` payload renders an "open" link to that route.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { NotificationsBell } from '@/features/notifications/NotificationsBell';
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

function mount() {
  return render(
    <MemoryRouter>
      <NotificationsBell />
    </MemoryRouter>,
  );
}

function makeNotification(overrides: {
  id?: string;
  type?: string;
  read_at?: string | null;
  payload?: string;
  created_at?: string;
}): {
  id: string;
  type: string;
  read_at: string | null;
  payload: string;
  created_at: string;
} {
  return {
    id: overrides.id ?? 'n-1',
    type: overrides.type ?? 'task.review_needed',
    read_at: overrides.read_at === undefined ? null : overrides.read_at,
    payload: overrides.payload ?? '{"title":"A title","body":"A body","link":"/tasks/42"}',
    created_at: overrides.created_at ?? '2026-08-12T10:00:00Z',
  };
}

describe('NotificationsBell', () => {
  it('renders no badge when there are no unread notifications', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { notifications: [], unread: 0 },
    });

    mount();

    // The bell button itself is always present (aria-label="Notifications").
    expect(await screen.findByRole('button', { name: 'Notifications' })).toBeTruthy();
    expect(screen.queryByText(/^\d+\+$|^[1-9]\d*$/)).toBeNull();
  });

  it('renders the unread count in the badge', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        notifications: [makeNotification({})],
        unread: 7,
      },
    });

    mount();

    expect(await screen.findByText('7')).toBeTruthy();
  });

  it('caps the badge at "99+" when unread >= 100', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { notifications: [], unread: 150 },
    });

    mount();

    expect(await screen.findByText('99+')).toBeTruthy();
  });

  it('opens the dropdown when the bell is clicked', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { notifications: [makeNotification({})], unread: 1 },
    });

    mount();
    await screen.findByRole('button', { name: 'Notifications' });

    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));

    // The dropdown is now visible; the notification payload (title
    // "A title" from makeNotification) is rendered.
    expect(screen.getByText('A title')).toBeTruthy();
    // Toggle: second click closes.
    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));
    expect(screen.queryByText('A title')).toBeNull();
  });

  it('renders one entry per notification with the parsed payload', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        notifications: [
          makeNotification({
            id: 'n-1',
            payload: '{"title":"First","body":"Body 1","link":"/tasks/1"}',
          }),
          makeNotification({
            id: 'n-2',
            payload: '{"title":"Second","body":"Body 2","link":"/tasks/2"}',
          }),
        ],
        unread: 2,
      },
    });

    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));

    expect(await screen.findByText('First')).toBeTruthy();
    expect(screen.getByText('Second')).toBeTruthy();
    // Two "open" links, one per notification.
    const openLinks = screen.getAllByText('open');
    expect(openLinks.length).toBe(2);
  });

  it('shows "mark read" only for unread notifications', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        notifications: [
          makeNotification({
            id: 'unread',
            read_at: null,
            payload: '{"title":"First","body":"a"}',
          }),
          makeNotification({
            id: 'read',
            read_at: '2026-08-12T11:00:00Z',
            payload: '{"title":"Second","body":"b"}',
          }),
        ],
        unread: 1,
      },
    });

    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));

    expect(await screen.findByText('First')).toBeTruthy();
    expect(screen.getByText('Second')).toBeTruthy();

    const markReadButtons = screen.getAllByText('mark read');
    expect(markReadButtons.length).toBe(1);
  });

  it('"mark read" posts to the api and refreshes the list', async () => {
    stubHttp.get
      .mockResolvedValueOnce({
        data: {
          notifications: [makeNotification({ id: 'n-1', read_at: null })],
          unread: 1,
        },
      })
      // After mark-read, the second GET returns the notification as read.
      .mockResolvedValueOnce({
        data: {
          notifications: [makeNotification({ id: 'n-1', read_at: '2026-08-12T11:00:00Z' })],
          unread: 0,
        },
      });
    stubHttp.post.mockResolvedValueOnce({ data: undefined });

    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));
    await screen.findByText('mark read');

    fireEvent.click(screen.getByText('mark read'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/notifications/n-1/read');
    });
    await waitFor(() => {
      // The second GET was issued as part of the post-call refresh.
      expect(stubHttp.get.mock.calls.length).toBeGreaterThan(1);
    });
  });

  it('closes the dropdown on outside click', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { notifications: [], unread: 0 },
    });

    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));
    expect(screen.getByText('No notifications.')).toBeTruthy();

    // Click outside the bell's wrapping div.
    fireEvent.mouseDown(document.body);

    expect(screen.queryByText('No notifications.')).toBeNull();
  });

  it('refetches on a WS "notifications" event', async () => {
    stubHttp.get.mockResolvedValue({
      data: { notifications: [], unread: 0 },
    });

    mount();
    await screen.findByRole('button', { name: 'Notifications' });
    const before = stubHttp.get.mock.calls.length;

    wsClient['listeners']
      .get('notifications')
      ?.forEach((fn) => fn({ topic: 'notifications', body: {} }));

    await waitFor(() => expect(stubHttp.get.mock.calls.length).toBeGreaterThan(before));
  });

  it('falls back to the notification type when payload has no title', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        notifications: [
          makeNotification({
            id: 'n-1',
            type: 'agent.offline',
            payload: '{"body":"Agent went offline"}',
          }),
        ],
        unread: 1,
      },
    });

    mount();
    fireEvent.click(screen.getByRole('button', { name: 'Notifications' }));

    expect(await screen.findByText('agent.offline')).toBeTruthy();
  });
});
