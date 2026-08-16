/**
 * Shared loading placeholder (Phase 28.23).
 *
 * One canonical "Loading…" line so pages stop hand-rolling
 * `<p className="... italic">Loading…</p>` variants. Dark-mode aware.
 */
export function Loading({
  label = 'Loading…',
  className = '',
}: {
  label?: string;
  className?: string;
}): JSX.Element {
  return (
    <p className={`text-sm text-slate-400 dark:text-slate-500 italic ${className}`.trim()}>
      {label}
    </p>
  );
}
