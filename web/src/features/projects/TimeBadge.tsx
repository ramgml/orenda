// Phase 30.12: small inline badge for the kanban TaskCard showing
// time spent / estimate / active-timer state. Pure function — no
// hooks, no API calls, just a styled span with the right numbers.
import type { JSX } from 'react';

/**
 * TimeBadge — renders one of three states:
 *   1. Active timer (no completed_at): "● 0:23" pulsing dot.
 *   2. estimateS null, spentS > 0: "⏱ 0:23" (no overrun possible).
 *   3. estimateS set: "⏱ 0:23 / 1:00" (red if spent > estimate).
 *
 * The badge is hidden by the caller's `detailed` check; we always
 * render the active-timer pulse regardless of `detailed` because
 * a leaked timer is a time-tracking bug — the operator needs to
 * see it even at compact density. The compact-mode check above
 * applies to the time-spent / estimate comparison only.
 */
export function TimeBadge({
  estimateS,
  spentS,
  timerActive,
}: {
  estimateS: number | null;
  spentS: number;
  /**
   * True when a single-active-timer row is open (started_at != null
   * && completed_at == null). The kanban's bulk-list payload doesn't
   * carry this — we infer it from the field pair so the operator
   * doesn't need a follow-up call to spot a leaked timer.
   */
  timerActive: boolean;
}): JSX.Element | null {
  if (timerActive) {
    return (
      <span
        data-testid="timer-active-badge"
        title="Active timer — stop it from the task page"
        className="inline-flex items-center px-1.5 py-0.5 rounded border border-emerald-300 bg-emerald-50 text-emerald-800 font-mono"
      >
        <span
          aria-hidden="true"
          className="inline-block w-1.5 h-1.5 rounded-full bg-emerald-500 mr-1 animate-pulse"
        />
        ●
      </span>
    );
  }

  if (spentS <= 0 && estimateS == null) {
    return null;
  }

  const spentLabel = formatHMMSS(spentS);
  const overBudget =
    estimateS != null && spentS > estimateS;
  const estimateLabel =
    estimateS != null ? formatHMMSS(estimateS) : null;

  return (
    <span
      data-testid="time-badge"
      className={`inline-flex items-center px-1.5 py-0.5 rounded border font-mono ${
        overBudget
          ? 'border-red-300 bg-red-50 text-red-700'
          : 'border-slate-300 text-slate-600'
      }`}
      title={estimateLabel ? `${spentLabel} of ${estimateLabel}` : spentLabel}
    >
      ⏱ {spentLabel}
      {estimateLabel ? ` / ${estimateLabel}` : ''}
    </span>
  );
}

// formatHMMSS turns seconds into H:MM:SS or MM:SS depending on the
// magnitude. Used by TimeBadge. Kept here (not in shared util) so
// the kanban's visual language stays localised.
function formatHMMSS(s: number): string {
  if (s < 0) s = 0;
  const totalSec = Math.floor(s);
  const h = Math.floor(totalSec / 3600);
  const m = Math.floor((totalSec % 3600) / 60);
  const sec = totalSec % 60;
  if (h > 0) {
    return `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`;
  }
  return `${m}:${String(sec).padStart(2, '0')}`;
}
