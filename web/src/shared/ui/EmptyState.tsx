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
      className={`rounded border border-border p-6 text-center text-muted-foreground dark:text-muted-foreground ${className}`.trim()}
    >
      {icon && <p className="text-2xl mb-2">{icon}</p>}
      <p className="text-sm">{message}</p>
      {children}
    </div>
  );
}
