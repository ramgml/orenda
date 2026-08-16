/**
 * React Query wrapper around the project list endpoint.
 *
 * The sidebar, dashboard stats and many other surfaces all need the
 * same authoritative list of projects. Funneling them through a single
 * cached query lets us invalidate once when something changes (e.g. an
 * archive toggle in the project detail page) and avoids re-fetching
 * on every mount.
 *
 * NOTE: the server does not currently broadcast project lifecycle
 * events over WebSocket, so this hook relies on the global
 * `staleTime` from the QueryClient (30s). Components that mutate
 * projects (createProject, updateProject) should `invalidateQueries`
 * with this key after the mutation settles.
 */
import { useQuery } from '@tanstack/react-query';

import { api, type Project } from '@/shared/api/client';

export const projectsQueryKey = ['projects'] as const;

/**
 * Returns the cached list of projects owned by the authenticated user.
 * The result is shared across all callers for `staleTime` (30s) and
 * refetched automatically when invalidated.
 */
export function useProjects(): {
  data: Project[] | undefined;
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
} {
  const q = useQuery({
    queryKey: projectsQueryKey,
    queryFn: () => api.listProjects(),
    // The list endpoint is cheap and stable; rely on explicit
    // invalidation from mutators instead of background polling.
    refetchOnWindowFocus: true,
  });
  return {
    data: q.data,
    isLoading: q.isLoading,
    error: q.error as Error | null,
    refetch: () => {
      void q.refetch();
    },
  };
}
