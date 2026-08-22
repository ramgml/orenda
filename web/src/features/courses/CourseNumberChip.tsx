import { useEffect, useRef, useState } from 'react';

/**
 * Human-readable course number chip (`C7`).
 *
 * Click copies `C7` to the clipboard and briefly swaps the label to
 * a confirmation. Follows the same pattern as ProjectNumberChip.
 *
 * Renders nothing when `number <= 0` (rows that predate numbering or
 * old fixtures).
 *
 * The chip is a `<span role="button">`, not a real button element,
 * for consistent styling with TaskNumberChip and ProjectNumberChip.
 */
export function CourseNumberChip({ number }: { number: number }): JSX.Element | null {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      clearTimeout(timer.current);
    },
    [],
  );

  if (!number || number <= 0) return null;

  const label = `C${number}`;

  function copy(): void {
    navigator.clipboard
      .writeText(label)
      .then(() => {
        setCopied(true);
        timer.current = setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => {
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
      data-testid="course-number-chip"
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
