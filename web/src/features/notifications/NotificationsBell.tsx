import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';

import { api, type Notification } from '@/shared/api/client';
import { useWebSocketTopic } from '@/shared/ws';

interface Payload {
  title?: string;
  body?: string;
  link?: string;
}

/**
 * Bell in the top nav with unread count and a dropdown of recent
 * notifications. Refreshes on every WS "notifications" event.
 */
export function NotificationsBell(): JSX.Element {
  const [open, setOpen] = useState(false);
  const [unread, setUnread] = useState(0);
  const [items, setItems] = useState<Notification[]>([]);
  const [error, setError] = useState<string | null>(null);
  const ref = useRef<HTMLDivElement>(null);

  async function load(): Promise<void> {
    try {
      const res = await api.listNotifications({ limit: 30 });
      setItems(res.notifications);
      setUnread(res.unread);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    load();
  }, []);

  useWebSocketTopic('notifications', () => {
    load();
  });

  // Close dropdown on outside click.
  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent): void => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', onClick);
    return () => document.removeEventListener('mousedown', onClick);
  }, [open]);

  async function markRead(id: string): Promise<void> {
    try {
      await api.markNotificationRead(id);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  function parsePayload(n: Notification): Payload {
    try {
      return JSON.parse(n.payload) as Payload;
    } catch {
      return {};
    }
  }

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="relative px-2 py-1 rounded hover:bg-slate-100 dark:hover:bg-slate-800"
        aria-label="Notifications"
      >
        <svg
          viewBox="0 0 24 24"
          className="h-5 w-5"
          fill="none"
          stroke="currentColor"
          strokeWidth={1.6}
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M15 17h5l-1.4-1.4A2 2 0 0 1 18 14.2V11a6 6 0 1 0-12 0v3.2a2 2 0 0 1-.6 1.4L4 17h5m6 0v1a3 3 0 1 1-6 0v-1m6 0H9"
          />
        </svg>
        {unread > 0 && (
          <span className="absolute -top-1 -right-1 min-w-[18px] px-1 rounded-full bg-red-600 text-white text-[10px] font-semibold text-center">
            {unread > 99 ? '99+' : unread}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute right-0 mt-2 w-80 rounded-lg border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 shadow-xl z-50">
          <div className="px-3 py-2 border-b border-slate-100 dark:border-slate-800 text-xs text-slate-500">
            {unread} unread
          </div>
          {error && <p className="px-3 py-2 text-xs text-red-600">{error}</p>}
          <ul className="max-h-96 overflow-auto divide-y divide-slate-100 dark:divide-slate-800">
            {items.length === 0 ? (
              <li className="px-3 py-4 text-sm text-slate-500">No notifications.</li>
            ) : (
              items.map((n) => {
                const p = parsePayload(n);
                return (
                  <li
                    key={n.id}
                    className="px-3 py-2 text-sm hover:bg-slate-50 dark:hover:bg-slate-800"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex-1 min-w-0">
                        <p className="font-medium truncate">{p.title || n.type}</p>
                        {p.body && <p className="text-xs text-slate-500 truncate">{p.body}</p>}
                        <p className="text-[10px] text-slate-400 mt-0.5">
                          {new Date(n.created_at).toLocaleString()}
                        </p>
                      </div>
                      <div className="flex gap-1">
                        {p.link && (
                          <Link
                            to={p.link}
                            onClick={() => setOpen(false)}
                            className="text-xs text-orenda-600 hover:underline"
                          >
                            open
                          </Link>
                        )}
                        {n.read_at == null && (
                          <button
                            type="button"
                            onClick={() => markRead(n.id)}
                            className="text-xs text-slate-500 hover:underline"
                          >
                            mark read
                          </button>
                        )}
                      </div>
                    </div>
                  </li>
                );
              })
            )}
          </ul>
        </div>
      )}
    </div>
  );
}
