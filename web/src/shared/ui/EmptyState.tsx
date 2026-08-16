import type { ReactNode } from 'react';

/**
 * Shared empty state (Phase 28.23).
 *
 * Centered bordered block for "nothing here yet" surfaces. Optional
 * icon/glyph above the message; children can carry extra hints or
 * actions. Dark-mode aware.
 */
export function EmptyState({
  icon,
  message,
  children,
  className = '',
}: {
  /** Optional glyph rendered large above the message (e.g. "✓"). */
  icon?: ReactNode;
  message: string;
  children?: ReactNode;
  className?: string;
}): JSX.Element {
  return (
    <div
      className={`rounded border border-slate-200 dark:border-slate-800 p-6 text-center text-slate-400 dark:text-slate-500 ${className}`.trim()}
    >
      {icon && <p className="text-2xl mb-2">{icon}</p>}
      <p className="text-sm">{message}</p>
      {children}
    </div>
  );
}
