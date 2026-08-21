/**
 * A header row that groups projects inside the sidebar.
 *
 * Behaviour:
 *  - Always shows the label (short, e.g. "Pinned", "Active").
 *  - Shows a count badge when `count` > 0.
 *  - Click the chevron to collapse/expand the children list. The
 *    collapsed state lives in this component (per-section) and is not
 *    persisted — sections reset to expanded on reload, matching the
 *    Weeek default.
 */
import { ReactNode, useState } from 'react';

import { Button } from '@/shared/ui/button';
import { cn } from '@/shared/util/cn';

interface SidebarSectionProps {
  label: string;
  /** Optional count badge to the right of the label (e.g. total items). */
  count?: number;
  /**
   * Starting collapsed state. Defaults to false (expanded). The Archived
   * section uses true so the list stays tidy when most projects are active.
   */
  defaultCollapsed?: boolean;
  children: ReactNode;
}

export function SidebarSection({
  label,
  count,
  defaultCollapsed = false,
  children,
}: SidebarSectionProps): JSX.Element {
  const [open, setOpen] = useState(!defaultCollapsed);

  return (
    <div className="px-2 pt-3">
      <Button
        variant="ghost"
        onClick={() => setOpen((v) => !v)}
        className={cn(
          'w-full justify-start gap-1 px-2 py-1 text-[11px] uppercase tracking-wide text-slate-500 hover:text-slate-700 dark:hover:text-slate-300 h-auto rounded',
        )}
        aria-expanded={open}
      >
        <span
          aria-hidden
          className={`inline-block transition-transform duration-150 ${open ? 'rotate-90' : ''}`}
        >
          {/* Simple chevron tip drawn with CSS to avoid an extra dep. */}▶
        </span>
        <span className="font-semibold">{label}</span>
        {typeof count === 'number' && count > 0 && (
          <span className="ml-1 px-1.5 py-0.5 rounded text-[10px] bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400 tabular-nums">
            {count}
          </span>
        )}
      </Button>
      {open && <div className="mt-1 space-y-0.5">{children}</div>}
    </div>
  );
}
