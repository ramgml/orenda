/**
 * Singleton WebSocket client with reconnect.
 *
 * Connects to /api/v1/ws (same origin), the browser sends the
 * orenda_session cookie automatically. Auto-reconnects with
 * exponential backoff (1s → 2s → 4s, capped at 30s) on disconnect.
 *
 * Listeners receive events synchronously; dispatch happens inside
 * the WS onmessage handler so subscribers don't pay a round-trip
 * cost.
 */

import { useEffect } from 'react';

import { useAuth } from '@/features/auth/AuthContext';

export interface WSMessage {
  topic: string;
  body: unknown;
}

type Listener = (msg: WSMessage) => void;

class WSClient {
  private ws: WebSocket | null = null;
  private listeners = new Map<string, Set<Listener>>();
  private retryDelay = 1000;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private closed = false;

  /**
   * Connect (or reconnect) to /api/v1/ws. Phase 27.2: no token
   * parameter — authentication rides on the same-origin cookie that
   * the browser sends automatically with the WS upgrade handshake.
   */
  connect(): void {
    this.closed = false;
    this.openSocket();
  }

  disconnect(): void {
    this.closed = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.ws?.close();
    this.ws = null;
  }

  /** Register a listener for a topic. Returns an unsubscribe function. */
  on(topic: string, fn: Listener): () => void {
    let set = this.listeners.get(topic);
    if (!set) {
      set = new Set();
      this.listeners.set(topic, set);
    }
    set.add(fn);
    return () => {
      set?.delete(fn);
      if (set && set.size === 0) {
        this.listeners.delete(topic);
      }
    };
  }

  private openSocket(): void {
    if (this.closed) return;
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${window.location.host}/api/v1/ws`;
    const ws = new WebSocket(url);
    this.ws = ws;

    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as WSMessage;
        const set = this.listeners.get(msg.topic);
        if (!set) return;
        // Dispatch each listener; they decide how to handle errors.
        for (const fn of set) {
          try {
            fn(msg);
          } catch (err) {
            // eslint-disable-next-line no-console
            console.error('WS listener threw', err);
          }
        }
      } catch {
        // ignore malformed frames
      }
    };

    ws.onclose = () => {
      this.ws = null;
      if (this.closed) return;
      this.scheduleReconnect();
    };

    ws.onerror = () => {
      // Let onclose handle reconnect; browsers fire onerror before close.
    };

    // Reset retry delay on successful connection.
    ws.onopen = () => {
      this.retryDelay = 1000;
    };
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer || this.closed) return;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.closed) {
        this.openSocket();
        this.retryDelay = Math.min(this.retryDelay * 2, 30_000);
      }
    }, this.retryDelay);
  }
}

export const wsClient = new WSClient();

/**
 * Hook that subscribes to a topic while the component is mounted.
 *
 * Returns nothing; pairs with react-query's invalidateQueries to refresh
 * server-state when an event arrives.
 */
export function useWebSocketTopic(topic: string, fn: Listener): void {
  useEffect(() => {
    return wsClient.on(topic, fn);
  }, [topic, fn]);
}

/**
 * Hook that connects/disconnects the WS whenever the user is authenticated.
 *
 * Phase 27.2: no token juggling. The hook opens the socket whenever
 * status flips to 'authenticated' and closes it on logout or unmount.
 * The WS upgrade is authenticated via the same-origin orenda_session
 * cookie.
 */
export function useWebSocketConnection(): void {
  const { status } = useAuth();
  useEffect(() => {
    if (status !== 'authenticated') {
      wsClient.disconnect();
      return;
    }
    wsClient.connect();
    return () => {
      wsClient.disconnect();
    };
  }, [status]);
}
