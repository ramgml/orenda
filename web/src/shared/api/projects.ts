import type { ApiClient } from './core';
import type { Task, TaskActivity, TaskAttachment } from './taskDetails';

export interface Project {
  id: string;
  /**
   * Human-readable sequential project number (`P1`). Assigned by the
   * server on creation and stable for the project's lifetime — agents and
   * humans reference projects by it in conversation. `0` means the row
   * predates numbering; the UI hides the chip in that case.
   */
  number: number;
  name: string;
  color: string;
  description?: string;
  /**
   * wiki:project-wiki-link. Slug of the wiki page that holds the
   * project's documentation (постановка, decision log, roadmap
   * slice). Empty / undefined means no link — the project header
   * hides the "Open wiki" button in that case. Setting an unknown
   * slug returns 422 from the user/agent PATCH.
   */
  wiki_slug?: string;
  owner_id: string;
  archived: boolean;
  /**
   * Agent access control. `false` = only granted agents see/claim
   * this project's tasks (empty grant list = no agents); `true` =
   * open to every agent.
   */
  agents_allowed: boolean;
  created_at: string;
  updated_at: string;
}

export interface BoardColumn {
  id: string;
  board_id: string;
  name: string;
  position: number;
  wip_limit?: number;
  color?: string;
  /**
   * Phase 27.8: machine key the column carries. The invariant
   * `task.status ≡ column.status` (when both are set) means a
   * single-axis UI — the Status select renders project columns
   * instead of a fixed enum.
   */
  status?: string;
}

/** Alias kept for PATCH /columns/:id which returns the same shape. */
export type Column = BoardColumn;

interface Board {
  id: string;
  project_id: string;
  name: string;
  position: number;
  created_at: string;
}

export interface ProjectBoard {
  board: Board;
  columns: BoardColumn[];
}

/**
 * One row in the project Activity tab. Extends TaskActivity with
 * the joined task title so the UI can render "X commented on Y"
 * without a second round-trip per row.
 */
export interface ProjectActivityItem extends TaskActivity {
  task_title: string;
}

/**
 * Project domain endpoints (`/api/v1/projects`, boards, columns,
 * project activity, project agent access).
 * Merged onto the ApiClient prototype in client.ts.
 */
export const projectsEndpoints = {
  listProjects(): Promise<Project[]> {
    return this.http.get<{ projects: Project[] }>('/api/v1/projects').then((r) => r.data.projects);
  },

  createProject(input: { name: string; color?: string; description?: string }): Promise<Project> {
    return this.http.post<Project>('/api/v1/projects', input).then((r) => r.data);
  },

  updateProject(
    projectId: string,
    input: Partial<{
      name: string;
      color: string;
      description: string;
      wiki_slug: string;
      archived: boolean;
      agents_allowed: boolean;
    }>,
  ): Promise<Project> {
    return this.http.patch<Project>(`/api/v1/projects/${projectId}`, input).then((r) => r.data);
  },

  getProject(projectId: string): Promise<Project> {
    return this.http.get<Project>(`/api/v1/projects/${projectId}`).then((r) => r.data);
  },

  deleteProject(projectId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/projects/${projectId}`).then(() => undefined);
  },

  getBoard(projectId: string): Promise<ProjectBoard> {
    return this.http.get<ProjectBoard>(`/api/v1/projects/${projectId}/board`).then((r) => r.data);
  },

  /** Update mutable column fields (name, position, wip_limit, color, status).
   * wip_limit=null → leave as-is; wip_limit=0 → clear; >0 → set. */
  updateColumn(
    columnId: string,
    input: {
      name?: string;
      position?: number;
      wip_limit?: number | null;
      color?: string;
      status?: string;
    },
  ): Promise<Column> {
    return this.http.patch<Column>(`/api/v1/columns/${columnId}`, input).then((r) => r.data);
  },

  /** Append a new column to the project's board (Phase 12).
   *  name is required; color/wip_limit are optional. The server picks
   *  the position (max+1024, end of the board) and broadcasts a WS
   *  event so other tabs refresh. */
  createColumn(
    projectId: string,
    input: { name: string; color?: string; wip_limit?: number | null; status?: string },
  ): Promise<Column> {
    return this.http
      .post<Column>(`/api/v1/projects/${projectId}/columns`, input)
      .then((r) => r.data);
  },

  /** Remove a column. Throws AxiosError with response.status === 422
   *  (and response.data.current = N) when the column still holds N
   *  tasks — the UI uses that count to render a helpful hint. 404
   *  means the column is already gone (idempotent from the user's
   *  POV). */
  deleteColumn(columnId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/columns/${columnId}`).then(() => undefined);
  },

  listProjectTasks(
    projectId: string,
    params?: { status?: string; column_id?: string },
  ): Promise<Task[]> {
    return this.http
      .get<{ tasks: Task[] }>(`/api/v1/projects/${projectId}/tasks`, { params })
      .then((r) => r.data.tasks);
  },

  /** Aggregate activity across every task in a project, newest first.
   * `limit` is optional; the server defaults to 200 and clamps at 500. */
  getProjectActivity(
    projectId: string,
    limit?: number,
  ): Promise<{ activity: ProjectActivityItem[] }> {
    const params = limit && limit > 0 ? { limit } : undefined;
    return this.http
      .get<{
        activity: ProjectActivityItem[];
      }>(`/api/v1/projects/${projectId}/activity`, { params })
      .then((r) => r.data);
  },

  // ---- Project attachments (Phase 11) ----

  listProjectAttachments(projectId: string): Promise<{ attachments: TaskAttachment[] }> {
    return this.http
      .get<{ attachments: TaskAttachment[] }>(`/api/v1/projects/${projectId}/attachments`)
      .then((r) => r.data);
  },

  uploadProjectAttachment(projectId: string, file: File): Promise<TaskAttachment> {
    const form = new FormData();
    form.append('file', file);
    return this.http
      .post<TaskAttachment>(`/api/v1/projects/${projectId}/attachments`, form, {
        headers: { 'Content-Type': 'multipart/form-data' },
      })
      .then((r) => r.data);
  },

  // ---- Project agent access (Phase 33 task 140) ----

  /**
   * Agent IDs granted access to a restricted project
   * (agents_allowed=false). Empty list = no agents can see the
   * project's tasks.
   */
  listProjectAgents(projectId: string): Promise<string[]> {
    return this.http
      .get<{ agent_ids: string[] }>(`/api/v1/projects/${projectId}/agents`)
      .then((r) => r.data.agent_ids);
  },

  /**
   * Full replacement of the granted-agent list for a restricted
   * project (agents_allowed=false). Send the complete set of agent
   * IDs every time — omitted IDs lose access.
   */
  setProjectAgents(projectId: string, agentIds: string[]): Promise<void> {
    return this.http
      .put<{ agent_ids: string[] }>(`/api/v1/projects/${projectId}/agents`, {
        agent_ids: agentIds,
      })
      .then(() => undefined);
  },
} satisfies ThisType<ApiClient>;

/** Method surface contributed by this domain to the ApiClient type. */
export type ProjectsApi = typeof projectsEndpoints;
