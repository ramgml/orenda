/**
 * Unit tests for `partitionProjects`. The function is pure, so we can
 * exercise every ordering edge case without touching React or the
 * network.
 *
 * Phase 16: the Inbox is no longer a project. partitionProjects no
 * longer special-cases the well-known Inbox id; the Inbox lives in
 * the sidebar as a separate static link.
 */
import { describe, expect, it } from 'vitest'

import type { Project } from '@/shared/api/client'
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

const orenda = p('p-orenda', 'Orenda')
const blog = p('p-blog', 'Blog')
const side = p('p-side', 'Side project')
const archivedOld = p('p-old', 'Old site', { archived: true })

describe('partitionProjects', () => {
  it('classifies plain projects into Active only', () => {
    const out = partitionProjects([blog, orenda], [])
    expect(out.active.map((x) => x.id)).toEqual(['p-blog', 'p-orenda'])
    expect(out.pinned).toEqual([])
    expect(out.archived).toEqual([])
  })

  it('sorts Active alphabetically regardless of input order', () => {
    const out = partitionProjects([orenda, blog, side], [])
    expect(out.active.map((x) => x.name)).toEqual(['Blog', 'Orenda', 'Side project'])
  })

  it('preserves pin insertion order and removes pinned projects from Active', () => {
    // User pinned Side first, then Orenda, then Blog.
    const out = partitionProjects([blog, orenda, side], ['p-side', 'p-orenda', 'p-blog'])
    expect(out.pinned.map((x) => x.id)).toEqual(['p-side', 'p-orenda', 'p-blog'])
    expect(out.active).toEqual([])
  })

  it('drops archived projects into the archived bucket only', () => {
    const out = partitionProjects([archivedOld, orenda], [])
    expect(out.archived.map((x) => x.id)).toEqual(['p-old'])
    expect(out.active.map((x) => x.id)).toEqual(['p-orenda'])
  })

  it('returns an empty partition on an empty project list', () => {
    const out = partitionProjects([], [])
    expect(out).toEqual({ pinned: [], active: [], archived: [] })
  })

  it('ignores pinned IDs that no longer exist (defensive)', () => {
    const out = partitionProjects([orenda], ['p-ghost', 'p-orenda'])
    expect(out.pinned.map((x) => x.id)).toEqual(['p-orenda'])
    expect(out.active).toEqual([])
  })
})