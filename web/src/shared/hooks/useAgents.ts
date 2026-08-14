/**
 * React Query wrapper around the agent list endpoint.
 *
 * Phase 28.19: the kanban card's AssigneeChip looks up the assigned
 * agent to surface its free-form labels in the title attribute (a
 * quick visual hint that the assignee is "qwen+installer", not just
 * an opaque id). Funneling the lookup through a cached query means
 * every TaskCard on the board shares one network round-trip; the
 * cache also gets re-used by AgentsPage, AgentsSidebarBadge, etc.
 */
import { useQuery } from '@tanstack/react-query';

import { api, type Agent } from '@/shared/api/client';

export const agentsQueryKey = ['agents'] as const;

/**
 * Returns the cached list of registered agents. The result is shared
 * across every caller for the QueryClient's global `staleTime` (30s);
 * `invalidateQueries(agentsQueryKey)` is the standard refresh path.
 *
 * The hook deliberately does not surface `isLoading`/`error`: callers
 * that need the data should handle `data === undefined` defensively
 * (the kanban card falls back to the legacy "id.slice(0,6)" rendering
 * when the cache is cold or empty).
 */
export function useAgents(): { data: Agent[] | undefined } {
  const q = useQuery({
    queryKey: agentsQueryKey,
    queryFn: () => api.listAgents(),
    refetchOnWindowFocus: true,
  });
  return { data: q.data };
}
