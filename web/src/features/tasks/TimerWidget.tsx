import { useEffect, useState } from 'react';

import { useAuth } from '@/features/auth/AuthContext';
import { api, type Task } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { useWebSocketTopic } from '@/shared/ws';

/**
 * Floating timer widget — sticky at the bottom-right of the screen.
 *
 * When the user clicks "Start" on a task, the timer runs until they hit
 * Stop. The widget shows elapsed time and lets them stop from any page.
 *
 * Phase 4 keeps this client-side: the timer start/stop state is in
 * localStorage + server; the actual duration is computed by the server.
 */
interface ActiveTimer {
  taskId: string;
  taskTitle: string;
  startedAt: string; // ISO
}

export function TimerWidget(): JSX.Element {
  const { status } = useAuth();
  const [active, setActive] = useState<ActiveTimer | null>(null);
  const [elapsed, setElapsed] = useState(0);
  const [error, setError] = useState<string | null>(null);

  // Load on mount.
  useEffect(() => {
    try {
      const saved = localStorage.getItem('orenda.activeTimer');
      if (saved) setActive(JSON.parse(saved));
    } catch {
      // ignore malformed storage
    }
  }, []);

  // Persist on change.
  useEffect(() => {
    try {
      if (active) localStorage.setItem('orenda.activeTimer', JSON.stringify(active));
      else localStorage.removeItem('orenda.activeTimer');
    } catch {
      // ignore quota errors
    }
  }, [active]);

  // Tick every second while active.
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => {
      const start = new Date(active.startedAt).getTime();
      setElapsed(Math.floor((Date.now() - start) / 1000));
    }, 1000);
    return () => clearInterval(id);
  }, [active]);

  // Listen for timer events from other tabs (started/stopped).
  useWebSocketTopic('timers', () => {
    // Reload state on every event.
    // The active timer lives in localStorage; an event could mean the
    // other tab stopped it. We reconcile optimistically.
  });

  if (status !== 'authenticated') return <></>;

  async function startOn(task: Task): Promise<void> {
    try {
      await api.startTimer(task.id);
      setActive({
        taskId: task.id,
        taskTitle: task.title,
        startedAt: new Date().toISOString(),
      });
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function stop(): Promise<void> {
    if (!active) return;
    try {
      await api.stopTimer(active.taskId);
      setActive(null);
      setElapsed(0);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  const fmt = (s: number): string => {
    const h = Math.floor(s / 3600)
      .toString()
      .padStart(2, '0');
    const m = Math.floor((s % 3600) / 60)
      .toString()
      .padStart(2, '0');
    const sec = (s % 60).toString().padStart(2, '0');
    return `${h}:${m}:${sec}`;
  };

  // T95: the widget renders only when it has something to say — an
  // active timer or an error. The old always-visible "No active
  // timer" empty card occupied the same fixed bottom-right corner as
  // the QuickCapture FAB (+) and visually covered it. With no idle
  // card, the corner belongs to the FAB whenever the timer is off.
  if (!active && !error) return <></>;

  // The container sits one FAB-height above the corner (bottom-20):
  // the active card / error banner must never overlap the FAB, which
  // stays at bottom-4 in any widget state.
  //
  // The fixed container spans a viewport corner area, so it would
  // otherwise swallow clicks aimed at UI underneath (e.g.
  // TaskViewBody's "Start timer" button — T86). The container is
  // click-transparent; only the active card opts back into pointer
  // events.
  return (
    <div className="pointer-events-none fixed bottom-20 right-4 z-40">
      {active && (
        <div className="pointer-events-auto rounded-lg shadow-lg border border-border bg-card p-3 w-72">
          <div className="flex items-start justify-between gap-2">
            <div className="flex-1 min-w-0">
              <p className="text-xs text-slate-500 mb-1">Timer on</p>
              <p className="text-sm font-medium truncate">{active.taskTitle}</p>
              <p className="text-lg font-mono tabular-nums mt-1">{fmt(elapsed)}</p>
            </div>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              onClick={stop}
              className="bg-red-600 hover:bg-red-700 text-white text-xs"
            >
              Stop
            </Button>
          </div>
        </div>
      )}
      {error && (
        <div className="pointer-events-auto mt-2 rounded border border-red-300 bg-red-50 text-red-800 px-2 py-1 text-xs">
          {error}
        </div>
      )}
      {/* expose startOn so other components can call it */}
      <TimerLauncher startOn={startOn} />
    </div>
  );
}

// TimerLauncher is a tiny pub-sub handle other components use to start
// timers without prop-drilling through the widget.
interface LauncherProps {
  startOn: (task: Task) => Promise<void>;
}

let launcherRef: LauncherProps | null = null;

function TimerLauncher({ startOn }: LauncherProps): JSX.Element {
  useEffect(() => {
    launcherRef = { startOn };
    return () => {
      launcherRef = null;
    };
  }, [startOn]);
  return <></>;
}

/**
 * StartTimer is the public API for starting a timer from any component.
 *
 *   <Button onClick={() => StartTimer(task)}>Start</Button>
 */
export function StartTimer(task: Task): void {
  if (!launcherRef) {
    // eslint-disable-next-line no-console
    console.warn('StartTimer called but no TimerWidget is mounted');
    return;
  }
  void launcherRef.startOn(task);
}
