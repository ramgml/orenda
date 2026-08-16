/**
 * Shared error banner (Phase 28.23).
 *
 * Red-bordered inline banner for recoverable page/section errors.
 * The dark: variants fix the light-only `bg-red-50 text-red-800`
 * banners that were unreadable in dark mode.
 */
export function ErrorBanner({
  message,
  className = '',
}: {
  message: string;
  className?: string;
}): JSX.Element {
  return (
    <div
      role="alert"
      className={`rounded border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 text-red-800 dark:text-red-300 px-3 py-2 text-sm ${className}`.trim()}
    >
      {message}
    </div>
  );
}
