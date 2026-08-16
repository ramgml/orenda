/**
 * Pure partitioning of a flat project list into the three buckets the
 * sidebar renders (Pinned / Active / Archived).
 *
 * Phase 16: the Inbox is no longer a project — it's a flat list of
 * tasks under /inbox (see /web/src/features/inbox/InboxPage.tsx).
 * The sidebar renders Inbox as a separate static link with its own
 * badge (number of inbox tasks). This module is concerned only with
 * project partitioning; the Inbox entry is wired in ProjectSidebar.
 *
 * Extracted from ProjectSidebar.tsx so we can unit-test the ordering
 * rules without mounting React.
 *
 * Ordering contract:
 *  - Pinned bucket holds user projects the local user pinned via the
 *    star button, in the order they were pinned. Pinned projects
 *    must NOT also appear in Active.
 *  - Active bucket is the remaining non-archived, non-pinned
 *    projects, sorted A→Z by name.
 *  - Archived bucket is the remaining archived projects, sorted A→Z.
 *    The bucket is rendered collapsed by default by the sidebar.
 */
import type { Project } from '@/shared/api/client';

export interface ProjectPartition {
  pinned: Project[];
  active: Project[];
  archived: Project[];
}

export function partitionProjects(
  projects: Project[],
  pinnedIds: readonly string[],
): ProjectPartition {
  const byId = new Map(projects.map((p) => [p.id, p]));
  const seen = new Set<string>();
  const pinned: Project[] = [];
  for (const id of pinnedIds) {
    const p = byId.get(id);
    if (p && !p.archived) {
      pinned.push(p);
      seen.add(id);
    }
  }
  const active = projects
    .filter((p) => !p.archived && !seen.has(p.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  const archived = projects.filter((p) => p.archived).sort((a, b) => a.name.localeCompare(b.name));
  return { pinned, active, archived };
}
