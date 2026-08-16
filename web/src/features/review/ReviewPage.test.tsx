// @vitest-environment jsdom
/**
 * ReviewPage component tests.
 *
 * Phase 19 review queue: list of tasks with awaiting='human' or
 * status='review'. Inline Accept (approve) / Return (reject) actions
 * wired to /api/v1/tasks/:id/review.
 *
 * Return requires a comment from the user; the page collects it via
 * window.prompt. Canceling the prompt (returning null) is a no-op —
 * the task stays in the queue.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { ReviewPage } from '@/features/review/ReviewPage';
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

function makeTask(
  id: string,
  title: string,
): {
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
  created_at: string;
  updated_at: string;
} {
  return {
    id,
    title,
    project_id: 'p-1',
    column_id: null,
    status: 'review',
    priority: 'medium',
    awaiting: 'human',
    time_spent_s: 0,
    position: 0,
    color: '',
    created_at: '2026-08-12T00:00:00Z',
    updated_at: '2026-08-12T00:00:00Z',
  };
}

function makeItem(
  id: string,
  title: string,
): {
  task: ReturnType<typeof makeTask>;
  project_name: string;
  project_color: string;
} {
  return {
    task: makeTask(id, title),
    project_name: 'Demo',
    project_color: '#3b82f6',
  };
}

describe('ReviewPage', () => {
  it('renders the loading placeholder while the queue is in flight', () => {
    stubHttp.get.mockReturnValue(new Promise(() => {}));
    render(<ReviewPage />);
    expect(screen.getByText('Loading…')).toBeTruthy();
  });

  it('renders the empty state when the queue is empty', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: { tasks: [], count: 0 } });
    render(<ReviewPage />);
    expect(await screen.findByText('Nothing to review — everything is up to date.')).toBeTruthy();
  });

  it('renders one row per queued task with Accept / Return buttons', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { tasks: [makeItem('t1', 'First'), makeItem('t2', 'Second')], count: 2 },
    });
    render(<ReviewPage />);

    const rows = await screen.findAllByTestId('review-row');
    expect(rows.length).toBe(2);
    // Each row has Accept and Return buttons.
    expect(screen.getAllByTestId('review-accept').length).toBe(2);
    expect(screen.getAllByTestId('review-reject').length).toBe(2);
    // Project label is per-row.
    expect(screen.getAllByText('Demo').length).toBeGreaterThanOrEqual(2);
  });

  it('Accept posts "approve" and removes the row optimistically', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { tasks: [makeItem('t1', 'Do it')], count: 1 },
    });
    stubHttp.post.mockResolvedValueOnce({ data: {} });

    render(<ReviewPage />);
    await screen.findByTestId('review-row');

    fireEvent.click(screen.getByTestId('review-accept'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/tasks/t1/review', {
        decision: 'approve',
        comment: '',
      });
    });
    expect(screen.queryByTestId('review-row')).toBeNull();
  });

  it('Return posts "reject" with the user-supplied comment', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { tasks: [makeItem('t1', 'Fix it')], count: 1 },
    });
    stubHttp.post.mockResolvedValueOnce({ data: {} });
    const promptSpy = vi.spyOn(window, 'prompt').mockReturnValue('Use a darker shade.');

    render(<ReviewPage />);
    await screen.findByTestId('review-row');

    fireEvent.click(screen.getByTestId('review-reject'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/tasks/t1/review', {
        decision: 'reject',
        comment: 'Use a darker shade.',
      });
    });
    expect(promptSpy).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('review-row')).toBeNull();
  });

  it('cancelling the Return prompt keeps the task in the queue', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { tasks: [makeItem('t1', 'Fix it')], count: 1 },
    });
    vi.spyOn(window, 'prompt').mockReturnValue(null); // cancel

    render(<ReviewPage />);
    await screen.findByTestId('review-row');

    fireEvent.click(screen.getByTestId('review-reject'));

    // No review POST should have been issued.
    expect(stubHttp.post).not.toHaveBeenCalled();
    // Row is still there.
    expect(screen.getByTestId('review-row')).toBeTruthy();
  });

  it('surfaces an error message when the queue endpoint rejects', async () => {
    stubHttp.get.mockRejectedValueOnce(new Error('boom'));

    render(<ReviewPage />);

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('surfaces an error when Accept fails', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { tasks: [makeItem('t1', 'X')], count: 1 },
    });
    stubHttp.post.mockRejectedValueOnce(new Error('nope'));

    render(<ReviewPage />);
    await screen.findByTestId('review-row');

    fireEvent.click(screen.getByTestId('review-accept'));

    expect(await screen.findByText('nope')).toBeTruthy();
    // The row stays — we only remove on success.
    expect(screen.getByTestId('review-row')).toBeTruthy();
  });

  it('refetches when a WS "tasks" event arrives', async () => {
    stubHttp.get.mockResolvedValue({
      data: { tasks: [makeItem('t1', 'A')], count: 1 },
    });

    render(<ReviewPage />);
    await screen.findByTestId('review-row');
    const callsBefore = stubHttp.get.mock.calls.length;

    wsClient['listeners'].get('tasks')?.forEach((fn) => fn({ topic: 'tasks', body: {} }));

    await waitFor(() => expect(stubHttp.get.mock.calls.length).toBeGreaterThan(callsBefore));
  });
});
