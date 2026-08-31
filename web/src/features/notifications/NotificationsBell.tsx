import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { TaskLink } from '@/features/tasks/TaskModal';

import { api, type Notification } from '@/shared/api/client';
import { useWebSocketTopic } from '@/shared/ws';
import { Button } from '@/shared/ui/button';
import { Popover, PopoverContent, PopoverTrigger } from '@/shared/ui/popover';

interface Payload {
  title?: string;
  body?: string;
  link?: string;
}

/**
 * Bell in the top nav with unread count and a dropdown of recent
 * notifications. Refreshes on every WS "notifications" event.
 *
 * The dropdown is a controlled Radix Popover: its content renders through a
 * portal to document.body, so the top bar's overflow-hidden header no longer
 * clips it. Radix owns the open/close semantics — outside click and Esc close
 * it natively.
 */
export function NotificationsBell(): JSX.Element {
  const [open, setOpen] = useState(false);
  const [unread, setUnread] = useState(0);
  const [items, setItems] = useState<Notification[]>([]);
  const [error, setError] = useState<string | null>(null);

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
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="ghost" className="relative px-2 py-1 h-auto" aria-label="Notifications">
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
        </Button>
      </PopoverTrigger>

      <PopoverContent align="end" sideOffset={8} className="w-80 p-0">
        <div className="px-3 py-2 border-b border-border text-xs text-slate-500">
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
                      {/* Task 102: task payloads deep-link into the modal
                          overlay when the link is a /tasks/<uuid> route;
                          anything else (wiki, project, …) keeps the plain
                          Link behaviour. */}
                      {p.link && TASK_LINK_RE.test(p.link) ? (
                        <TaskLink
                          taskId={p.link.slice('/tasks/'.length)}
                          onClick={() => setOpen(false)}
                          className="text-xs text-orenda-600 hover:underline"
                        >
                          open
                        </TaskLink>
                      ) : (
                        p.link && (
                          <Link
                            to={p.link}
                            onClick={() => setOpen(false)}
                            className="text-xs text-orenda-600 hover:underline"
                          >
                            open
                          </Link>
                        )
                      )}
                      {n.read_at == null && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => markRead(n.id)}
                          className="text-xs text-slate-500 hover:underline h-auto px-1 py-0.5"
                        >
                          mark read
                        </Button>
                      )}
                    </div>
                  </div>
                </li>
              );
            })
          )}
        </ul>
      </PopoverContent>
    </Popover>
  );
}

// Task 102: a notification link opens the task modal only when it
// points at a bare /tasks/<uuid> route. Legacy payloads may carry
// other shapes (old numeric ids, extra segments, wiki/project links)
// — those keep the plain-Link full-page behaviour.
const TASK_LINK_RE = /^\/tasks\/[0-9a-f-]{36}$/;
