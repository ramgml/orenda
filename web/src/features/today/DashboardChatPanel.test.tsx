// @vitest-environment jsdom
/**
 * DashboardChatPanel tests (T9).
 *
 * The panel boots from a TanStack Query (api.chatHistory) and merges
 * live turns from the WS topic "dashboard-chat". The tests pin the
 * contracts the DoD cares about:
 *
 *   1. Thread history renders on mount (user + agent turns).
 *   2. Sending "/plan day" POSTs the command, renders both turns,
 *      and fires onProposalCreated when the reply carries a
 *      result_ref (the tray must refresh).
 *   3. Plain text answers pending=true: the typing indicator shows
 *      and the agent reply arriving over WS clears it.
 *   4. WS events for other threads are ignored (thread_id filter).
 *
 * Network plumbing is mocked at the `api` boundary (same pattern as
 * TutorChatPanel.test.tsx); WS events are dispatched straight to the
 * registered listeners. Assertions use the vanilla vitest matchers —
 * this repo does not wire jest-dom.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { DashboardChatPanel } from '@/features/today/DashboardChatPanel';
import { api } from '@/shared/api/client';
import type { ChatMessage } from '@/shared/api/client';
import { wsClient } from '@/shared/ws';

const msg = (over: Partial<ChatMessage>): ChatMessage => ({
  id: 'm-' + Math.random().toString(36).slice(2, 8),
  thread_id: 'default',
  sender_type: 'user',
  body_md: 'message',
  created_at: new Date().toISOString(),
  ...over,
});

/** Dispatch a WS "dashboard-chat" event straight to the registered listeners. */
function dispatchChat(body: Record<string, unknown>): void {
  wsClient['listeners']
    .get('dashboard-chat')
    ?.forEach((fn) => fn({ topic: 'dashboard-chat', body }));
}

function renderPanel(onProposalCreated?: () => void) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  return render(
    <QueryClientProvider client={qc}>
      <DashboardChatPanel thread="default" onProposalCreated={onProposalCreated} />
    </QueryClientProvider>,
  );
}

const enabled = (el: HTMLElement | null): boolean => !!el && !el.hasAttribute('disabled');

describe('DashboardChatPanel', () => {
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
    vi.spyOn(api, 'chatHistory').mockResolvedValue({
      messages: [
        msg({ sender_type: 'user', body_md: '/plan day' }),
        msg({ sender_type: 'agent', body_md: 'Here is your plan.' }),
      ],
    });

    renderPanel();

    const userTurn = await screen.findByTestId('dashboard-chat-msg-user');
    expect(userTurn.textContent).toContain('/plan day');
    expect(screen.getByTestId('dashboard-chat-msg-agent').textContent).toContain(
      'Here is your plan.',
    );
  });

  it('sends /plan day, renders the agent answer, and refreshes the tray on result_ref', async () => {
    vi.spyOn(api, 'chatHistory').mockResolvedValue({ messages: [] });
    const send = vi.spyOn(api, 'sendChatMessage').mockResolvedValue({
      user_message: msg({ sender_type: 'user', body_md: '/plan day', command: '/plan' }),
      agent_message: msg({
        sender_type: 'agent',
        body_md: 'Planned. The proposal is in your tray.',
        result_ref: 'prop-1',
      }),
      result_ref: 'prop-1',
      pending: false,
    });
    const onProposalCreated = vi.fn();

    renderPanel(onProposalCreated);

    await waitFor(() => {
      expect(enabled(screen.getByTestId('dashboard-chat-input'))).toBe(true);
    });

    fireEvent.click(screen.getByTestId('dashboard-chat-plan-day'));

    await waitFor(() => {
      expect(send).toHaveBeenCalledWith('default', '/plan day');
    });
    await waitFor(() => {
      expect(screen.getByTestId('dashboard-chat-msg-user').textContent).toContain('/plan day');
    });
    expect(screen.getByTestId('dashboard-chat-msg-agent').textContent).toContain(
      'Planned. The proposal is in your tray.',
    );
    // The tray refresh fired because the reply carried a result_ref.
    expect(onProposalCreated).toHaveBeenCalledTimes(1);
    // The command answered synchronously: no typing indicator.
    expect(screen.queryByTestId('dashboard-chat-typing')).toBeNull();
  });

  it('shows the typing indicator for plain text until the WS agent reply lands', async () => {
    vi.spyOn(api, 'chatHistory').mockResolvedValue({ messages: [] });
    vi.spyOn(api, 'sendChatMessage').mockResolvedValue({
      user_message: msg({ sender_type: 'user', body_md: 'what should I learn today?' }),
      agent_message: null,
      result_ref: '',
      pending: true,
    });

    renderPanel();

    await waitFor(() => {
      expect(enabled(screen.getByTestId('dashboard-chat-input'))).toBe(true);
    });
    fireEvent.change(screen.getByTestId('dashboard-chat-input'), {
      target: { value: 'what should I learn today?' },
    });
    fireEvent.click(screen.getByTestId('dashboard-chat-send'));

    await waitFor(() => {
      expect(screen.getByTestId('dashboard-chat-msg-user').textContent).toContain(
        'what should I learn today?',
      );
    });
    // Pending: the typing indicator is up and the input is locked.
    expect(screen.getByTestId('dashboard-chat-typing')).toBeTruthy();
    expect(enabled(screen.getByTestId('dashboard-chat-send'))).toBe(false);

    // The dashboard agent answers through /agent/chat; the reply
    // rides in on the "dashboard-chat" topic.
    dispatchChat({
      user_id: 'u-1',
      thread_id: 'default',
      sender_type: 'agent',
      message: msg({ sender_type: 'agent', body_md: 'Start with the Rust course.' }),
    });

    await waitFor(() => {
      expect(screen.getByTestId('dashboard-chat-msg-agent').textContent).toContain(
        'Start with the Rust course.',
      );
    });
    // The thread resolved: typing indicator gone, input re-opens.
    expect(screen.queryByTestId('dashboard-chat-typing')).toBeNull();
    expect((screen.getByTestId('dashboard-chat-input') as HTMLTextAreaElement).disabled).toBe(
      false,
    );
  });

  it('ignores chat events for other threads', async () => {
    vi.spyOn(api, 'chatHistory').mockResolvedValue({ messages: [] });
    vi.spyOn(api, 'sendChatMessage').mockResolvedValue({
      user_message: msg({ sender_type: 'user', body_md: 'q' }),
      agent_message: null,
      result_ref: '',
      pending: true,
    });

    renderPanel();

    await waitFor(() => {
      expect(enabled(screen.getByTestId('dashboard-chat-input'))).toBe(true);
    });
    fireEvent.change(screen.getByTestId('dashboard-chat-input'), { target: { value: 'q' } });
    fireEvent.click(screen.getByTestId('dashboard-chat-send'));

    await waitFor(() => {
      expect(screen.getByTestId('dashboard-chat-typing')).toBeTruthy();
    });

    dispatchChat({
      user_id: 'u-1',
      thread_id: 'someone-else',
      sender_type: 'agent',
      message: msg({ thread_id: 'someone-else', sender_type: 'agent', body_md: 'not for you' }),
    });

    await new Promise((r) => setTimeout(r, 25));
    expect(screen.queryByTestId('dashboard-chat-msg-agent')).toBeNull();
    expect(screen.getByTestId('dashboard-chat-typing')).toBeTruthy();
  });

  it('sends on Enter without Shift', async () => {
    vi.spyOn(api, 'chatHistory').mockResolvedValue({ messages: [] });
    const send = vi.spyOn(api, 'sendChatMessage').mockResolvedValue({
      user_message: msg({ sender_type: 'user', body_md: 'hello' }),
      agent_message: msg({ sender_type: 'agent', body_md: 'hi' }),
      result_ref: '',
      pending: false,
    });

    renderPanel();

    await waitFor(() => {
      expect(enabled(screen.getByTestId('dashboard-chat-input'))).toBe(true);
    });
    fireEvent.change(screen.getByTestId('dashboard-chat-input'), { target: { value: 'hello' } });
    fireEvent.keyDown(screen.getByTestId('dashboard-chat-input'), {
      key: 'Enter',
      shiftKey: false,
    });

    await waitFor(() => {
      expect(send).toHaveBeenCalledWith('default', 'hello');
    });
  });
});
