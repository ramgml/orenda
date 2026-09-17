/**
 * DashboardChatPanel (T9): the agent chat pane on the Today
 * dashboard.
 *
 * Data flow mirrors TutorChatPanel (T16):
 *
 *   - History: TanStack Query over api.chatHistory (loaded once per
 *     thread; the WS path appends, so no polling).
 *   - Send: api.sendChatMessage appends the user turn. A command
 *     ("/plan day", "/help") answers synchronously — the response
 *     carries agent_message. Plain text answers pending=true with
 *     agent_message=null: the dashboard agent replies later through
 *     /api/v1/agent/chat.
 *   - Live: useWebSocketTopic("dashboard-chat") — every event
 *     carries thread_id (panel filter) and the full message. An
 *     agent turn resolves the pending state.
 *
 * The "typing" indicator is exactly the pending state: we asked and
 * the agent hasn't replied yet. When the reply carries a result_ref
 * (e.g. a study-proposal id from "/plan day"), onProposalCreated
 * lets the page refresh the tray that renders it.
 */
import { api } from '@/shared/api/client';
import type { ChatEventBody, ChatMessage } from '@/shared/api/client';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';

import { useWebSocketTopic } from '@/shared/ws';
import { Button } from '@/shared/ui/button';
import { Textarea } from '@/shared/ui/textarea';

interface ChatEvent {
  body: Partial<ChatEventBody> | undefined;
}

export function DashboardChatPanel({
  thread = 'default',
  onProposalCreated,
}: {
  thread?: string;
  onProposalCreated?: () => void;
}): JSX.Element {
  const [draft, setDraft] = useState('');
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const historyQ = useQuery({
    queryKey: ['dashboard-chat', thread],
    queryFn: () => api.chatHistory(thread),
  });

  // Local turns = history + live WS turns not yet in the history.
  // Keyed by id so a later refetch (query invalidation) dedupes.
  const [live, setLive] = useState<ChatMessage[]>([]);

  // Each accepted turn schedules a requestAnimationFrame to keep the
  // thread scrolled to the bottom. If the panel unmounts before the
  // frame fires (jsdom tears the test DOM down while the frame is
  // still pending), the callback must not touch the dead node —
  // cancel the frame on unmount (same pattern as TutorChatPanel).
  const scrollFrame = useRef<number>(0);
  useEffect(
    () => () => {
      cancelAnimationFrame(scrollFrame.current);
    },
    [],
  );

  // Live updates: filter by thread (events from other threads must
  // not touch this panel). Dedup on message id — the send POST
  // result and its WS echo can both arrive.
  useWebSocketTopic('dashboard-chat', (evt) => {
    const body = (evt as ChatEvent).body;
    if (!body || body.thread_id !== thread || !body.message) return;
    setLive((prev) => {
      if (prev.some((m) => m.id === body.message!.id)) return prev;
      return [...prev, body.message!];
    });
    cancelAnimationFrame(scrollFrame.current);
    scrollFrame.current = requestAnimationFrame(() => {
      scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
    });
  });

  const messages: ChatMessage[] = useMemo(() => {
    const hist = historyQ.data?.messages ?? [];
    const seen = new Set(hist.map((m) => m.id));
    return [...hist, ...live.filter((m) => !seen.has(m.id))];
  }, [historyQ.data, live]);

  const sendQ = useMutation({
    mutationFn: (text: string) => api.sendChatMessage(thread, text),
    onSuccess: (res) => {
      setDraft('');
      setLive((prev) =>
        prev.some((x) => x.id === res.user_message.id) ? prev : [...prev, res.user_message],
      );
      if (res.agent_message) {
        setLive((prev) =>
          prev.some((x) => x.id === res.agent_message!.id) ? prev : [...prev, res.agent_message!],
        );
      }
      // A command produced a side-effect (e.g. "/plan day" created a
      // study proposal): let the page refresh the tray that shows it.
      if (res.result_ref) onProposalCreated?.();
    },
    // Mutation failure surfaces inline; the thread state is
    // server-derived so nothing to roll back locally.
  });

  // Pending = the newest turn is ours and the agent hasn't spoken
  // since. Mirrors the server's derivation (Last user message).
  const pending =
    messages.length > 0 &&
    messages[messages.length - 1].sender_type === 'user' &&
    !historyQ.isLoading;

  const send = (): void => {
    const text = draft.trim();
    if (!text || sendQ.isPending || pending) return;
    sendQ.mutate(text);
  };

  const planDay = (): void => {
    if (sendQ.isPending || pending) return;
    sendQ.mutate('/plan day');
  };

  return (
    <div data-testid="dashboard-chat-panel" className="rounded border border-border bg-background">
      <div className="flex items-center justify-between px-4 py-2 border-b border-border">
        <h2 className="text-sm font-semibold text-foreground">Plan with the agent</h2>
        {pending && (
          <span data-testid="dashboard-chat-typing" className="text-xs text-slate-500 italic">
            agent is typing…
          </span>
        )}
      </div>
      <div
        ref={scrollRef}
        data-testid="dashboard-chat-thread"
        className="max-h-72 overflow-y-auto px-4 py-3 space-y-3"
      >
        {messages.length === 0 && !historyQ.isLoading && (
          <p className="text-xs text-slate-400 italic">
            No messages yet — try “/plan day” or just type.
          </p>
        )}
        {messages.map((m) => (
          <div
            key={m.id}
            data-testid={`dashboard-chat-msg-${m.sender_type}`}
            className={
              'rounded px-3 py-2 text-sm max-w-[85%] ' +
              (m.sender_type === 'user'
                ? 'ml-auto bg-orenda-50 dark:bg-orenda-900/40 text-foreground'
                : 'mr-auto bg-slate-100 dark:bg-slate-800 text-foreground')
            }
          >
            <div className="prose dark:prose-invert max-w-none text-sm">{m.body_md}</div>
          </div>
        ))}
      </div>
      <div className="px-4 py-3 border-t border-border space-y-2">
        <Textarea
          data-testid="dashboard-chat-input"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          rows={2}
          className="text-sm"
          placeholder={pending ? 'Waiting for the agent…' : 'Type a message or /command…'}
          disabled={pending}
        />
        <div className="flex items-center gap-3">
          <Button
            type="button"
            data-testid="dashboard-chat-send"
            onClick={send}
            disabled={pending || sendQ.isPending || !draft.trim()}
            size="sm"
            className="bg-slate-700 hover:bg-slate-800"
          >
            {sendQ.isPending ? 'Sending…' : 'Send'}
          </Button>
          <Button
            type="button"
            data-testid="dashboard-chat-plan-day"
            onClick={planDay}
            disabled={pending || sendQ.isPending}
            size="sm"
            variant="outline"
          >
            Спланировать день
          </Button>
          {sendQ.isError && (
            <span className="text-xs text-amber-700">Failed to send — try again.</span>
          )}
        </div>
      </div>
    </div>
  );
}
