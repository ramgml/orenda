import { useEffect, useRef, useState } from 'react';

/**
 * Human-readable task number chip (`#123`).
 *
 * Click copies `#123` to the clipboard and briefly swaps the label to
 * a confirmation. The project has no global toast library (no sonner /
 * shadcn toast in dependencies), so the confirmation follows the
 * existing copy-to-clipboard pattern from `settings/Backups.tsx`:
 * transient local state instead of a toast.
 *
 * Both `onClick` and `onPointerDown` stop propagation: the chip lives
 * inside clickable cards (TaskCard opens the task on click) and dnd-kit
 * draggable wrappers (pointerdown starts a drag) — neither must fire
 * when the user only wants to copy the number.
 *
 * Renders nothing when `number <= 0` (rows that predate numbering or
 * old fixtures); the field is required on the `Task` type, the
 * conditional render lives here.
 *
 * The chip is a `<span role="button">`, not a `<button>`, because one
 * of its hosts (ReviewPage's ReviewRow) already renders a real
 * `<button>` around the row — nested buttons are invalid HTML.
 */
export function TaskNumberChip({ number }: { number: number }): JSX.Element | null {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (timer.current !== null) clearTimeout(timer.current);
    },
    [],
  );

  if (!number || number <= 0) return null;

  const label = `#${number}`;

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
      data-testid="task-number-chip"
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
