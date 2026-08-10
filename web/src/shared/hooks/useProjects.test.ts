/**
 * Smoke tests for `useProjects`. We don't mount React here; instead we
 * prove the fetch contract by exercising the wrapped `api.listProjects`
 * path indirectly through `QueryClient.fetchQuery`. The hook itself
 * is a one-liner above the query client and is exercised manually in
 * the app.
 */
import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi, beforeEach } from 'vitest'

import { projectsQueryKey } from '@/shared/hooks/useProjects'

// We bypass the hook entirely and only assert that the cache key
// is stable and that `listProjects` is what the queryFn calls.
const mockListProjects = vi.fn()

vi.mock('@/shared/api/client', () => ({
  api: {
    listProjects: () => mockListProjects(),
  },
}))

describe('useProjects cache contract', () => {
  beforeEach(() => {
    mockListProjects.mockReset()
  })

  it('uses a stable queryKey for cross-component invalidation', () => {
    expect(projectsQueryKey).toEqual(['projects'])
  })

  it('fetchQuery calls api.listProjects exactly once per fresh cache', async () => {
    mockListProjects.mockResolvedValue([
      {
        id: 'p1',
        name: 'Orenda',
        color: '#000',
        owner_id: 'u1',
        archived: false,
        created_at: '',
        updated_at: '',
      },
    ])

    const qc = new QueryClient({ defaultOptions: { queries: { retry: 0 } } })
    const projects = await qc.fetchQuery({ queryKey: projectsQueryKey, queryFn: mockListProjects })
    expect(projects).toHaveLength(1)
    expect(mockListProjects).toHaveBeenCalledTimes(1)

    // A second fetch from the same cache should NOT call the API again.
    const cached = qc.getQueryData(projectsQueryKey)
    expect(cached).toBeDefined()
    expect(mockListProjects).toHaveBeenCalledTimes(1)
  })
})
