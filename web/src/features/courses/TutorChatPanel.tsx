/**
 * TutorChatPanel (T16): the lesson-scoped dialog tutor.
 *
 * Mounted under the lesson content on LessonPage. Data flow:
 *
 *   - History: TanStack Query over api.tutorHistory (loaded once
 *     per lesson; the WS path appends, so no polling).
 *   - Ask: api.tutorAsk appends the user turn and flips the thread
 *     to pending (server derives pending = last message role=user).
 *   - Live: useWebSocketTopic("tutor") — every event carries
 *     user_id (hub filter), lesson_id (panel filter) and the full
 *     message. An agent turn resolves the pending state.
 *
 * The "typing" indicator is exactly the pending state: we asked
 * and the agent hasn't replied yet.
 */
import { useMemo, useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';

import { api } from '@/shared/api/client';
import type { TutorMessage } from '@/shared/api/client';
import { useWebSocketTopic } from '@/shared/ws';
import { Button } from '@/shared/ui/button';
import { Textarea } from '@/shared/ui/textarea';

interface TutorEventBody {
  user_id: string;
  lesson_id: string;
  role: 'user' | 'agent';
  message: TutorMessage;
}

export function TutorChatPanel({ lessonId }: { lessonId: string }): JSX.Element {
  const [draft, setDraft] = useState('');
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const historyQ = useQuery({
    queryKey: ['tutor', lessonId],
    queryFn: () => api.tutorHistory(lessonId),
    enabled: !!lessonId,
  });

  // Local turns = history + live WS turns not yet in the history.
  // Keyed by id so a later refetch (query invalidation) dedupes.
  const [live, setLive] = useState<TutorMessage[]>([]);

  // Live updates: filter by lesson (events from other lessons must
  // not touch this panel). Dedup on message id — the ask POST
  // result and its WS echo can both arrive.
  useWebSocketTopic('tutor', (evt) => {
    const body = evt.body as Partial<TutorEventBody> | undefined;
    if (!body || body.lesson_id !== lessonId || !body.message) return;
    setLive((prev) => {
      if (prev.some((m) => m.id === body.message!.id)) return prev;
      return [...prev, body.message!];
    });
    requestAnimationFrame(() => {
      scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
    });
  });

  const messages: TutorMessage[] = useMemo(() => {
    const hist = historyQ.data?.messages ?? [];
    const seen = new Set(hist.map((m) => m.id));
    return [...hist, ...live.filter((m) => !seen.has(m.id))];
  }, [historyQ.data, live]);

  const askQ = useMutation({
    mutationFn: (questionMd: string) => api.tutorAsk(lessonId, questionMd),
    onSuccess: (m) => {
      setDraft('');
      setLive((prev) => (prev.some((x) => x.id === m.id) ? prev : [...prev, m]));
    },
    // Mutation failure surfaces inline; the thread state is
    // server-derived so nothing to roll back locally.
  });

  // Pending = the newest turn is ours and the agent hasn't spoken
  // since. Mirrors the server's derivation exactly.
  const pending =
    messages.length > 0 && messages[messages.length - 1].role === 'user' && !historyQ.isLoading;

  const send = (): void => {
    const text = draft.trim();
    if (!text || askQ.isPending || pending) return;
    askQ.mutate(text);
  };

  return (
    <div data-testid="tutor-panel" className="rounded border border-border bg-background">
      <div className="flex items-center justify-between px-4 py-2 border-b border-border">
        <h2 className="text-sm font-semibold text-foreground">Ask the tutor</h2>
        {pending && (
          <span data-testid="tutor-typing" className="text-xs text-slate-500 italic">
            tutor is typing…
          </span>
        )}
      </div>
      <div
        ref={scrollRef}
        data-testid="tutor-thread"
        className="max-h-72 overflow-y-auto px-4 py-3 space-y-3"
      >
        {messages.length === 0 && !historyQ.isLoading && (
          <p className="text-xs text-slate-400 italic">
            No questions yet — ask anything about this lesson.
          </p>
        )}
        {messages.map((m) => (
          <div
            key={m.id}
            data-testid={`tutor-msg-${m.role}`}
            className={
              'rounded px-3 py-2 text-sm max-w-[85%] ' +
              (m.role === 'user'
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
          data-testid="tutor-input"
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
          placeholder={pending ? 'Waiting for the tutor…' : 'Ask about this lesson…'}
          disabled={pending}
        />
        <div className="flex items-center gap-3">
          <Button
            type="button"
            data-testid="tutor-send"
            onClick={send}
            disabled={pending || askQ.isPending || !draft.trim()}
            size="sm"
            className="bg-slate-700 hover:bg-slate-800"
          >
            {askQ.isPending ? 'Sending…' : 'Ask'}
          </Button>
          {askQ.isError && (
            <span className="text-xs text-amber-700">Failed to send — try again.</span>
          )}
        </div>
      </div>
    </div>
  );
}
