/**
 * Singleton WebSocket client with reconnect.
 *
 * Connects to /api/v1/ws?token=<jwt>, dispatches incoming Events to
 * registered listeners by topic. Auto-reconnects with exponential backoff
 * (1s → 2s → 4s, capped at 30s) on disconnect.
 *
 * Listeners receive events synchronously; the WSClient returns immediately
 * to its caller and dispatches on a goroutine-style microtask via
 * Promise.resolve().then.
 */

import { useEffect } from 'react'

import { useAuth } from '@/features/auth/AuthContext'

export interface WSMessage {
  topic: string
  body: unknown
}

type Listener = (msg: WSMessage) => void

class WSClient {
  private ws: WebSocket | null = null
  private listeners = new Map<string, Set<Listener>>()
  private retryDelay = 1000
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null
  private token: string | null = null
  private closed = false

  /**
   * Connect (or reconnect) with a fresh JWT. The token is captured at
   * connect time so a subsequent login doesn't break the existing socket.
   */
  connect(token: string): void {
    this.closed = false
    this.token = token
    this.openSocket()
  }

  disconnect(): void {
    this.closed = true
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.ws?.close()
    this.ws = null
  }

  /** Register a listener for a topic. Returns an unsubscribe function. */
  on(topic: string, fn: Listener): () => void {
    let set = this.listeners.get(topic)
    if (!set) {
      set = new Set()
      this.listeners.set(topic, set)
    }
    set.add(fn)
    return () => {
      set?.delete(fn)
      if (set && set.size === 0) {
        this.listeners.delete(topic)
      }
    }
  }

  private openSocket(): void {
    if (!this.token || this.closed) return
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
    const url = `${proto}://${window.location.host}/api/v1/ws?token=${encodeURIComponent(this.token)}`
    const ws = new WebSocket(url)
    this.ws = ws

    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as WSMessage
        const set = this.listeners.get(msg.topic)
        if (!set) return
        // Dispatch each listener; they decide how to handle errors.
        for (const fn of set) {
          try {
            fn(msg)
          } catch (err) {
            // eslint-disable-next-line no-console
            console.error('WS listener threw', err)
          }
        }
      } catch {
        // ignore malformed frames
      }
    }

    ws.onclose = () => {
      this.ws = null
      if (this.closed) return
      this.scheduleReconnect()
    }

    ws.onerror = () => {
      // Let onclose handle reconnect; browsers fire onerror before close.
    }

    // Reset retry delay on successful connection.
    ws.onopen = () => {
      this.retryDelay = 1000
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer || this.closed) return
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null
      // Token might have expired; refresh from the api client.
      const tok = this.token
      if (tok) {
        this.openSocket()
        this.retryDelay = Math.min(this.retryDelay * 2, 30_000)
      }
    }, this.retryDelay)
  }
}

export const wsClient = new WSClient()

/**
 * Hook that subscribes to a topic while the component is mounted.
 *
 * Returns nothing; pairs with react-query's invalidateQueries to refresh
 * server-state when an event arrives.
 */
export function useWebSocketTopic(topic: string, fn: Listener): void {
  useEffect(() => {
    return wsClient.on(topic, fn)
  }, [topic, fn])
}

/**
 * Hook that connects/disconnects the WS whenever the user is authenticated.
 *
 * Uses the JWT captured at /auth/login time (Phase 2). Phase 3 will
 * introduce a dedicated /auth/ws-token endpoint that issues short-lived
 * tokens tied to the cookie session.
 */
export function useWebSocketConnection(): void {
  const { status, token } = useAuth()
  useEffect(() => {
    if (status !== 'authenticated' || !token) {
      wsClient.disconnect()
      return
    }
    wsClient.connect(token)
    return () => {
      wsClient.disconnect()
    }
  }, [status, token])
}