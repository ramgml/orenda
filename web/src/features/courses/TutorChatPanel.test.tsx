// @vitest-environment jsdom
/**
 * TutorChatPanel tests (T16).
 *
 * The panel boots from a TanStack Query (api.tutorHistory) and
 * merges live turns from the WS topic "tutor". The tests pin the
 * four contracts the DoD cares about:
 *
 *   1. Thread history renders on mount (question + agent answer).
 *   2. Sending a question POSTs it, renders it immediately, and
 *      shows the "typing" indicator while the thread is pending.
 *   3. A WS "tutor" event carrying the agent reply appears live —
 *      no reload, no refetch.
 *   4. WS events for other lessons are ignored (lesson_id filter).
 *
 * Network plumbing is mocked at the `api` boundary (same pattern
 * as LessonPage.test.tsx); WS events are dispatched straight to
 * the registered listeners (same pattern as SidebarNav.test.tsx).
 * Assertions use the vanilla vitest matchers — this repo does not
 * wire jest-dom.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TutorChatPanel } from '@/features/courses/TutorChatPanel';
import { api } from '@/shared/api/client';
import type { TutorMessage } from '@/shared/api/client';
import { wsClient } from '@/shared/ws';

const msg = (over: Partial<TutorMessage>): TutorMessage => ({
  id: 'm-' + Math.random().toString(36).slice(2, 8),
  lesson_id: 'lesson-1',
  user_id: 'u-1',
  role: 'user',
  body_md: 'question',
  created_at: new Date().toISOString(),
  ...over,
});

/** Dispatch a WS "tutor" event straight to the registered listeners. */
function dispatchTutor(body: Record<string, unknown>): void {
  wsClient['listeners'].get('tutor')?.forEach((fn) => fn({ topic: 'tutor', body }));
}

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(
    <QueryClientProvider client={qc}>
      <TutorChatPanel lessonId="lesson-1" />
    </QueryClientProvider>,
  );
}

const enabled = (el: HTMLElement | null): boolean => !!el && !el.hasAttribute('disabled');

describe('TutorChatPanel', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    wsClient.disconnect();
    wsClient['listeners'].clear();
  });

  afterEach(() => {
    cleanup();
    wsClient['listeners'].clear();
    vi.restoreAllMocks();
  });

  it('renders thread history on mount', async () => {
    vi.spyOn(api, 'tutorHistory').mockResolvedValue({
      messages: [
        msg({ role: 'user', body_md: 'What is a borrow?' }),
        msg({ role: 'agent', body_md: 'A borrow is a reference.' }),
      ],
    });

    renderPanel();

    const userTurn = await screen.findByTestId('tutor-msg-user');
    expect(userTurn.textContent).toContain('What is a borrow?');
    expect(screen.getByTestId('tutor-msg-agent').textContent).toContain('A borrow is a reference.');
  });

  it('sends a question, shows it, and flips to typing while pending', async () => {
    vi.spyOn(api, 'tutorHistory').mockResolvedValue({ messages: [] });
    const ask = vi
      .spyOn(api, 'tutorAsk')
      .mockResolvedValue(msg({ role: 'user', body_md: 'Explain lifetimes' }));

    renderPanel();

    await waitFor(() => {
      expect(enabled(screen.getByTestId('tutor-input'))).toBe(true);
    });

    fireEvent.change(screen.getByTestId('tutor-input'), {
      target: { value: 'Explain lifetimes' },
    });
    fireEvent.click(screen.getByTestId('tutor-send'));

    await waitFor(() => {
      expect(ask).toHaveBeenCalledWith('lesson-1', 'Explain lifetimes');
    });
    await waitFor(() => {
      expect(screen.getByTestId('tutor-msg-user').textContent).toContain('Explain lifetimes');
    });
    // Awaiting the agent: the typing indicator is visible and the
    // send button is disabled (one open question per thread).
    expect(screen.getByTestId('tutor-typing')).toBeTruthy();
    expect(enabled(screen.getByTestId('tutor-send'))).toBe(false);
  });

  it('renders the agent reply arriving over WS without a reload', async () => {
    vi.spyOn(api, 'tutorHistory').mockResolvedValue({ messages: [] });
    vi.spyOn(api, 'tutorAsk').mockResolvedValue(msg({ role: 'user', body_md: 'q' }));

    renderPanel();

    await waitFor(() => {
      expect(enabled(screen.getByTestId('tutor-input'))).toBe(true);
    });
    fireEvent.change(screen.getByTestId('tutor-input'), { target: { value: 'q' } });
    fireEvent.click(screen.getByTestId('tutor-send'));

    await waitFor(() => {
      expect(screen.getByTestId('tutor-typing')).toBeTruthy();
    });

    // The agent answers: the reply rides in on the "tutor" topic.
    dispatchTutor({
      user_id: 'u-1',
      lesson_id: 'lesson-1',
      role: 'agent',
      message: msg({ role: 'agent', body_md: 'Lifetimes bound references.' }),
    });

    await waitFor(() => {
      expect(screen.getByTestId('tutor-msg-agent').textContent).toContain(
        'Lifetimes bound references.',
      );
    });
    // The thread resolved: typing indicator gone, the textarea
    // re-opens (the ask cleared the draft, so typing a follow-up
    // re-enables the send button without a reload).
    expect(screen.queryByTestId('tutor-typing')).toBeNull();
    const input = screen.getByTestId('tutor-input') as HTMLTextAreaElement;
    expect(input.disabled).toBe(false);
    // The ask cleared the draft; typing a follow-up re-enables the
    // send button with no reload.
    fireEvent.change(input, { target: { value: 'follow-up' } });
    expect(enabled(screen.getByTestId('tutor-send'))).toBe(true);
  });

  it('ignores tutor events for other lessons', async () => {
    vi.spyOn(api, 'tutorHistory').mockResolvedValue({ messages: [] });
    vi.spyOn(api, 'tutorAsk').mockResolvedValue(msg({ role: 'user', body_md: 'q' }));

    renderPanel();

    await waitFor(() => {
      expect(enabled(screen.getByTestId('tutor-input'))).toBe(true);
    });
    fireEvent.change(screen.getByTestId('tutor-input'), { target: { value: 'q' } });
    fireEvent.click(screen.getByTestId('tutor-send'));

    await waitFor(() => {
      expect(screen.getByTestId('tutor-typing')).toBeTruthy();
    });

    dispatchTutor({
      user_id: 'u-1',
      lesson_id: 'other-lesson',
      role: 'agent',
      message: msg({ lesson_id: 'other-lesson', role: 'agent', body_md: 'not for you' }),
    });

    await new Promise((r) => setTimeout(r, 25));
    expect(screen.queryByTestId('tutor-msg-agent')).toBeNull();
    expect(screen.getByTestId('tutor-typing')).toBeTruthy();
  });
});
