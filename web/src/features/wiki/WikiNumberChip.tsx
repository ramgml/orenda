import { useEffect, useRef, useState } from 'react';

/**
 * Human-readable wiki page number chip (`W123`).
 *
 * Click copies `W123` to the clipboard and briefly swaps the label to
 * a confirmation. Follows the same pattern as TaskNumberChip (Phase 33):
 * transient local state instead of a toast (no global toast library).
 *
 * Both `onClick` and `onPointerDown` stop propagation: the chip lives
 * inside the PageEditor title area and must not leak gestures.
 *
 * Renders nothing when `number <= 0` (rows that predate numbering or
 * old fixtures); the field is required on the `Page` type, the
 * conditional render lives here.
 *
 * The chip is a `<span role="button">`, not a real button element,
 * because it sits inside the title input row — nested buttons are
 * invalid HTML.
 */
export function WikiNumberChip({ number }: { number: number }): JSX.Element | null {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      clearTimeout(timer.current);
    },
    [],
  );

  if (!number || number <= 0) return null;

  const label = `W${number}`;

  function copy(): void {
    navigator.clipboard
      .writeText(label)
      .then(() => {
        setCopied(true);
        timer.current = setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {
        // Clipboard unavailable (permissions, non-secure context) —
        // keep the chip quiet rather than surfacing an error for a
        // convenience action.
        setCopied(false);
      });
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLSpanElement>): void {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      e.stopPropagation();
      copy();
    }
  }

  return (
    <span
      role="button"
      tabIndex={0}
      data-testid="wiki-number-chip"
      onClick={(e) => {
        e.stopPropagation();
        copy();
      }}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={handleKeyDown}
      title={`Copy ${label}`}
      aria-label={`Copy ${label}`}
      className="inline-flex items-center px-1 rounded font-mono text-[10px] text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800 cursor-pointer select-none shrink-0"
    >
      {copied ? `Copied ${label}` : label}
    </span>
  );
}
