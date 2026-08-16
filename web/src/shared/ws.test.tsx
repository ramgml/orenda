// @vitest-environment jsdom
/**
 * useWebSocketTopic tests (Phase 28.23).
 *
 * The hook is used by ~11 call sites, all of which pass inline arrow
 * functions. If the effect depended on `fn`, every render would
 * unsubscribe + resubscribe and an event arriving in that gap would
 * be lost. These tests pin the contract:
 *
 *   - One subscription per topic across rerenders (the listeners Set
 *     never grows beyond one entry for a mounted component).
 *   - The LATEST render's handler is the one invoked on an event
 *     (stale closures are impossible).
 *   - Unmount unsubscribes.
 */
import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useWebSocketTopic, wsClient, type WSMessage } from '@/shared/ws';

/** Direct access to the client's listener registry (same convention as
 * NotificationsBell.test.tsx). */
function listenersOf(topic: string): Set<(msg: WSMessage) => void> | undefined {
  return wsClient['listeners'].get(topic);
}

function dispatch(topic: string, body: unknown = {}): void {
  listenersOf(topic)?.forEach((fn) => fn({ topic, body }));
}

beforeEach(() => {
  wsClient['listeners'].clear();
});

afterEach(() => {
  wsClient['listeners'].clear();
});

describe('useWebSocketTopic', () => {
  it('subscribes exactly once per topic across rerenders with inline arrows', () => {
    let n = 0;
    const { rerender, unmount } = renderHook(
      ({ step }: { step: number }) =>
        // Deliberately a fresh inline arrow per render — the shape every
        // real call site uses.
        useWebSocketTopic('tasks', () => {
          n += step;
        }),
      { initialProps: { step: 1 } },
    );

    const firstHandler = [...(listenersOf('tasks') ?? [])][0];
    expect(listenersOf('tasks')?.size).toBe(1);

    rerender({ step: 2 });
    rerender({ step: 3 });
    rerender({ step: 4 });

    // Still one subscription — and the SAME registered handler identity.
    // (Depending on `fn` would swap the registered listener on every
    // rerender; the gap between unsubscribe and resubscribe is where
    // events were lost.)
    expect(listenersOf('tasks')?.size).toBe(1);
    const currentHandler = [...(listenersOf('tasks') ?? [])][0];
    expect(currentHandler).toBe(firstHandler);

    // And the live handler is the latest render's closure.
    dispatch('tasks');
    expect(n).toBe(4);

    unmount();
    expect(listenersOf('tasks')?.size ?? 0).toBe(0);
  });

  it('invokes the latest render handler, not the one captured at subscribe time', () => {
    const first = vi.fn();
    const second = vi.fn();

    const { rerender } = renderHook(
      ({ fn }: { fn: (msg: WSMessage) => void }) => useWebSocketTopic('agents', fn),
      { initialProps: { fn: first } },
    );
    // New identity on rerender — mimics inline arrows.
    rerender({ fn: second });

    dispatch('agents', { kind: 'agent.registered' });

    expect(first).not.toHaveBeenCalled();
    expect(second).toHaveBeenCalledTimes(1);
    expect(second).toHaveBeenCalledWith({ topic: 'agents', body: { kind: 'agent.registered' } });
  });

  it('re-subscribes when the topic itself changes', () => {
    const fn = vi.fn();
    const { rerender } = renderHook(
      ({ topic }: { topic: string }) => useWebSocketTopic(topic, fn),
      {
        initialProps: { topic: 'tasks' },
      },
    );

    expect(listenersOf('tasks')?.size).toBe(1);

    rerender({ topic: 'notifications' });

    expect(listenersOf('tasks')?.size ?? 0).toBe(0);
    expect(listenersOf('notifications')?.size).toBe(1);

    dispatch('notifications');
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
