/**
 * Top-bar that sits above the page content (next to the sidebar).
 *
 * After the redesign the global navigation moved into the sidebar, so
 * this header only carries user-level affordances: notifications,
 * health, theme toggle, sign-out, and the page title context.
 */
import { Link } from 'react-router-dom';

import { useAuth } from '@/features/auth/AuthContext';
import { NotificationsBell } from '@/features/notifications/NotificationsBell';
import { Button } from '@/shared/ui/button';
import { HealthBadge } from '@/shared/ui/HealthBadge';
import { ThemeToggle } from '@/shared/ui/ThemeToggle';

/**
 * Mobile-only project rail entry point. On phones the sidebar is hidden
 * (`hidden md:flex`), so users need a way to reach the global navigation.
 * This renders a hamburger that opens the sidebar as an overlay.
 */
function HamburgerButton({ onClick }: { onClick: () => void }): JSX.Element {
  return (
    <Button
      variant="ghost"
      size="icon"
      onClick={onClick}
      aria-label="Open navigation"
      className="md:hidden h-8 w-8 text-muted-foreground"
    >
      <span
        aria-hidden
        className="block h-0.5 w-4 bg-current relative before:content-[''] before:absolute before:-top-1.5 before:left-0 before:h-0.5 before:w-4 before:bg-current after:content-[''] after:absolute after:top-1.5 after:left-0 after:h-0.5 after:w-4 after:bg-current"
      />
    </Button>
  );
}

interface AppTopBarProps {
  /** Optional click handler used by the mobile hamburger. */
  onMobileMenuClick?: () => void;
}

export function AppTopBar({ onMobileMenuClick }: AppTopBarProps): JSX.Element {
  const { user, logout } = useAuth();
  return (
    <header className="h-12 border-b border-border bg-background flex items-center justify-between gap-3 px-4">
      <div className="flex items-center gap-2">
        {onMobileMenuClick && <HamburgerButton onClick={onMobileMenuClick} />}
        <Link to="/" className="flex items-center gap-2 font-semibold md:hidden">
          <span className="inline-block h-5 w-5 rounded bg-orenda-500" aria-hidden />
          Orenda
        </Link>
      </div>
      <div className="flex items-center gap-3 text-sm text-muted-foreground">
        <NotificationsBell />
        <HealthBadge />
        <ThemeToggle />
        {user && (
          <span className="hidden sm:inline text-xs text-slate-400" title={user.email}>
            {user.email}
          </span>
        )}
        <Button
          variant="outline"
          size="sm"
          onClick={() => logout()}
          className="px-2 py-1 text-xs h-auto"
        >
          Sign out
        </Button>
      </div>
    </header>
  );
}
