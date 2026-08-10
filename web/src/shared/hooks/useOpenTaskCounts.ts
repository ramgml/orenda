/**
 * Counts of "open" tasks (anything not in `done`) per project, used by
 * the sidebar to surface where work is piling up.
 *
 * Implementation notes:
 *  - The Orenda REST API has no `/projects/tasks/counts` endpoint, so
 *    we lean on `api.listProjectTasks(id)` and filter client-side. For
 *    a single-user local-first app with ≤ a few hundred tasks per
 *    project this is comfortably fast (~5-50ms per project). When
 *    projects get large we should swap this for a dedicated
 *    `GET /api/v1/projects/tasks/counts` endpoint without changing
 *    the hook signature.
 *  - We only fetch projects that the caller asks for (`projectIds`).
 *    This keeps the hook cheap when the sidebar renders while still
 *    working from a partial cache.
 */
import { useQueries } from '@tanstack/react-query'

import { api, type Task } from '@/shared/api/client'

/**
 * Hook that returns a Map of `projectId -> openTaskCount` for the
 * supplied project IDs. A project still loading gets `undefined` in
 * the map; an empty project gets 0.
 */
export function useOpenTaskCounts(projectIds: string[]): Map<string, number | undefined> {
  const sortedIds = [...projectIds].sort()
  const queries = useQueries({
    queries: sortedIds.map((id) => ({
      // Keyed by id so the same project id always resolves to the same
      // slot in `queries[]` regardless of caller-supplied order.
      queryKey: ['project-tasks', id] as const,
      queryFn: () => api.listProjectTasks(id).catch(() => [] as Task[]),
      staleTime: 30_000,
    })),
  })

  const map = new Map<string, number | undefined>()
  sortedIds.forEach((id, i) => {
    const result = queries[i]
    if (!result || result.isLoading) {
      map.set(id, undefined)
      return
    }
    const tasks = (result.data ?? []) as Task[]
    map.set(
      id,
      tasks.filter((t) => t.status !== 'done').length,
    )
  })
  return map
}
