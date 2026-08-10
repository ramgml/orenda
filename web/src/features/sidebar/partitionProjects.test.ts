/**
 * Unit tests for `partitionProjects`. The function is pure, so we can
 * exercise every ordering edge case without touching React or the
 * network.
 */
import { describe, expect, it } from 'vitest'

import type { Project } from '@/shared/api/client'
import { INBOX_PROJECT_ID } from '@/shared/constants'
import { partitionProjects } from '@/features/sidebar/partitionProjects'

function p(
  id: string,
  name: string,
  opts: Partial<Project> = {},
): Project {
  return {
    id,
    name,
    color: '#000',
    owner_id: 'u1',
    archived: false,
    created_at: '',
    updated_at: '',
    ...opts,
  }
}

const inbox = p(INBOX_PROJECT_ID, 'Inbox')
const orenda = p('p-orenda', 'Orenda')
const blog = p('p-blog', 'Blog')
const side = p('p-side', 'Side project')
const archivedOld = p('p-old', 'Old site', { archived: true })

describe('partitionProjects', () => {
  it('puts the Inbox at the top and out of other buckets', () => {
    const out = partitionProjects([blog, inbox, orenda], [])
    expect(out.inbox?.id).toBe(INBOX_PROJECT_ID)
    expect(out.active.map((x) => x.id)).toEqual(['p-blog', 'p-orenda'])
    expect(out.pinned).toEqual([])
    expect(out.archived).toEqual([])
  })

  it('sorts Active alphabetically regardless of input order', () => {
    const out = partitionProjects([orenda, blog, side, inbox], [])
    expect(out.active.map((x) => x.name)).toEqual(['Blog', 'Orenda', 'Side project'])
  })

  it('preserves pin insertion order and removes pinned projects from Active', () => {
    // User pinned Side first, then Orenda, then Blog.
    const out = partitionProjects([blog, orenda, side, inbox], ['p-side', 'p-orenda', 'p-blog'])
    expect(out.pinned.map((x) => x.id)).toEqual(['p-side', 'p-orenda', 'p-blog'])
    expect(out.active).toEqual([])
  })

  it('drops archived projects into the archived bucket only', () => {
    const out = partitionProjects([archivedOld, orenda, inbox], [])
    expect(out.archived.map((x) => x.id)).toEqual(['p-old'])
    expect(out.active.map((x) => x.id)).toEqual(['p-orenda'])
  })

  it('Inbox never appears in Archived even if archived=true', () => {
    const archivedInbox: Project = { ...inbox, archived: true }
    const out = partitionProjects([archivedInbox, orenda], [])
    // The Inbox is still surfaced — it's a system project.
    expect(out.inbox?.id).toBe(INBOX_PROJECT_ID)
    expect(out.archived).toEqual([])
  })

  it('returns undefined inbox when the project is missing', () => {
    const out = partitionProjects([orenda, blog], [])
    expect(out.inbox).toBeUndefined()
    expect(out.active.map((x) => x.id)).toEqual(['p-blog', 'p-orenda'])
  })

  it('ignores pinned IDs that no longer exist (defensive)', () => {
    const out = partitionProjects([orenda, inbox], ['p-ghost', 'p-orenda'])
    expect(out.pinned.map((x) => x.id)).toEqual(['p-orenda'])
    expect(out.active).toEqual([])
  })

  it('keeps Inbox and a pinned project in two distinct buckets', () => {
    const out = partitionProjects([inbox, orenda, blog], ['p-orenda'])
    expect(out.inbox?.id).toBe(INBOX_PROJECT_ID)
    expect(out.pinned.map((x) => x.id)).toEqual(['p-orenda'])
    // Blog is the only remaining non-inbox, non-pinned project.
    expect(out.active.map((x) => x.id)).toEqual(['p-blog'])
  })
})
