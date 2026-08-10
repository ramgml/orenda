/**
 * Pure partitioning of a flat project list into the three buckets the
 * sidebar renders (Inbox / Pinned / Active / Archived).
 *
 * Extracted from ProjectSidebar.tsx so we can unit-test the ordering
 * rules without mounting React. The sidebar itself is a thin wrapper
 * over this function plus react-query glue.
 *
 * Ordering contract:
 *  - The Inbox (well-known id, see INBOX_PROJECT_ID) is *always* first.
 *    It never appears in any other bucket, even if it is archived in
 *    the database (defensive — the server doesn't allow archiving it).
 *  - The Pinned bucket holds user projects the local user pinned via
 *    the star button, in the order they were pinned. Pinned projects
 *    must NOT also appear in Active.
 *  - The Active bucket is the remaining non-archived, non-pinned,
 *    non-inbox projects, sorted A→Z by name.
 *  - The Archived bucket is the remaining archived projects (excluding
 *    the Inbox), sorted A→Z. The bucket is rendered collapsed by
 *    default by the sidebar.
 */
import type { Project } from '@/shared/api/client'

import { INBOX_PROJECT_ID } from '@/shared/constants'

export interface ProjectPartition {
  /** The system Inbox project, or undefined if it hasn't been seeded yet. */
  inbox: Project | undefined
  pinned: Project[]
  active: Project[]
  archived: Project[]
}

export function partitionProjects(
  projects: Project[],
  pinnedIds: readonly string[],
): ProjectPartition {
  const inbox = projects.find((p) => p.id === INBOX_PROJECT_ID)
  const nonInbox = projects.filter((p) => p.id !== INBOX_PROJECT_ID)

  const byId = new Map(nonInbox.map((p) => [p.id, p]))
  const seen = new Set<string>()
  const pinned: Project[] = []
  for (const id of pinnedIds) {
    const p = byId.get(id)
    if (p && !p.archived) {
      pinned.push(p)
      seen.add(id)
    }
  }
  const active = nonInbox
    .filter((p) => !p.archived && !seen.has(p.id))
    .sort((a, b) => a.name.localeCompare(b.name))
  const archived = nonInbox
    .filter((p) => p.archived)
    .sort((a, b) => a.name.localeCompare(b.name))
  return { inbox, pinned, active, archived }
}
