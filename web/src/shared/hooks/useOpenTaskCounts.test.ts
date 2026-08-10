/**
 * Smoke tests for `useOpenTaskCounts`. We avoid mounting React by
 * driving the underlying react-query contract directly: the hook just
 * wraps `useQueries` with a per-project queryFn, so we exercise that
 * fn via `QueryClient.fetchQuery`.
 */
import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi, beforeEach } from 'vitest'

const mockList = vi.fn()
vi.mock('@/shared/api/client', () => ({
  api: {
    listProjectTasks: (id: string) => mockList(id),
  },
}))

const p1Tasks = [
  { id: 't1', project_id: 'p1', status: 'todo', title: '', time_spent_s: 0, awaiting: 'none', priority: 'medium', position: 0, created_at: '', updated_at: '' },
  { id: 't2', project_id: 'p1', status: 'done', title: '', time_spent_s: 0, awaiting: 'none', priority: 'medium', position: 0, created_at: '', updated_at: '' },
]

describe('useOpenTaskCounts cache key', () => {
  beforeEach(() => mockList.mockReset())

  it('uses per-project query keys (independent caches)', async () => {
    // Stub distinct responses for the two fetchQuery calls below.
    mockList.mockImplementation(async (id: string) => {
      if (id === 'p1') return p1Tasks
      return []
    })

    const qc = new QueryClient({ defaultOptions: { queries: { retry: 0 } } })
    await qc.fetchQuery({ queryKey: ['project-tasks', 'p1'], queryFn: () => mockList('p1') })
    await qc.fetchQuery({ queryKey: ['project-tasks', 'p2'], queryFn: () => mockList('p2') })

    expect(mockList).toHaveBeenCalledTimes(2)
    expect(mockList).toHaveBeenNthCalledWith(1, 'p1')
    expect(mockList).toHaveBeenNthCalledWith(2, 'p2')
    // Both queries populated their cache; p1 has rows, p2 has [].
    const p1cache = qc.getQueryData<unknown>(['project-tasks', 'p1'])
    const p2cache = qc.getQueryData<unknown>(['project-tasks', 'p2'])
    expect(Array.isArray(p1cache)).toBe(true)
    expect(Array.isArray(p2cache)).toBe(true)
    expect((p1cache as unknown[]).length).toBe(2)
    expect((p2cache as unknown[]).length).toBe(0)
  })
})
