import type { ApiClient } from './core';
import type { Task } from './taskDetails';

interface TimeEntry {
  id: string;
  task_id: string;
  agent_id: string;
  started_at: string;
  ended_at?: string;
  duration_s?: number;
  source: 'timer' | 'manual';
}

/** T354: one report row — a task, or a project subtotal (projects[]). */
export interface TimeReportRow {
  task_id?: string;
  title?: string;
  project_id?: string;
  project_name?: string;
  project_color?: string;
  total_sec: number;
}

export interface TimeReport {
  agent_id: string;
  from: string;
  to: string;
  /** Per-project subtotals, total_sec desc. No entry for projectless tasks. */
  projects?: TimeReportRow[];
  tasks: TimeReportRow[];
  total_sec: number;
}

/** Phase 13: a global label that can be attached to tasks. */
export interface Tag {
  id: string;
  name: string;
  color?: string;
}

/**
 * T164: per-op outcome of POST /api/v1/sync (mirrors Go's syncResult).
 */
interface SyncResultItem {
  client_id: string;
  ok: boolean;
  error?: string;
}

interface SyncResponse {
  results: SyncResultItem[];
}

/**
 * T164: chunk size for batched sync moves. The endpoint answers
 * too_many_ops above 200 ops (internal/api/handlers_sync.go), so
 * batches are cut below that limit before POSTing.
 */
const SYNC_MOVE_CHUNK_SIZE = 150;

/**
 * Task domain endpoints: the task CRUD core (`/api/v1/tasks`,
 * kanban moves, inbox, tags, sync batch).
 * Merged onto the ApiClient prototype in client.ts.
 */
export const tasksEndpoints = {
  createTask(
    projectId: string,
    input: { title: string; column_id?: string; description?: string },
  ): Promise<Task> {
    return this.http.post<Task>(`/api/v1/projects/${projectId}/tasks`, input).then((r) => r.data);
  },

  patchTask(taskId: string, input: Partial<Task>): Promise<Task> {
    return this.http.patch<Task>(`/api/v1/tasks/${taskId}`, input).then((r) => r.data);
  },

  bulkPatchTasks(input: {
    task_ids: string[];
    patch: Partial<Task>;
  }): Promise<{ tasks: Task[]; errors?: Record<string, string> }> {
    return this.http
      .post<{ tasks: Task[]; errors?: Record<string, string> }>('/api/v1/tasks/bulk-edit', input)
      .then((r) => r.data);
  },

  deleteTask(taskId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/tasks/${taskId}`).then(() => undefined);
  },

  // ---- Inbox (Phase 16) ----
  //
  // The Inbox is the set of tasks with project_id IS NULL. It's a
  // flat list (no board, no columns) — the user files these cards
  // onto a real project via PATCH /tasks/{id} {project_id: "..."}.

  /** List tasks in the Inbox (project_id IS NULL), newest first. */
  listInboxTasks(params?: { status?: string }): Promise<{ tasks: Task[] }> {
    return this.http.get<{ tasks: Task[] }>('/api/v1/inbox/tasks', { params }).then((r) => r.data);
  },

  /**
   * Phase 30.8: tasks with a due_at in [from, to]. The calendar uses
   * this to render deadlines alongside timed events. The returned
   * tasks are unordered by the API; the calendar sorts by date.
   */
  tasksWithDue(params: { from: string; to: string }): Promise<{ tasks: Task[] }> {
    return this.http
      .get<{ tasks: Task[] }>('/api/v1/tasks/with-due', { params })
      .then((r) => r.data);
  },

  /** Create a task with project_id explicitly empty (Inbox). */
  createInboxTask(input: {
    title: string;
    description?: string;
    parent_task_id?: string;
    status?: string;
    priority?: string;
    assignee_type?: string;
    assignee_id?: string;
    // Phase 30.10: optional due_at (ISO 8601). Undefined → no
    // deadline; the field is opt-in so the hotkey capture flow
    // stays one keystroke from thinking to done.
    due_at?: string;
  }): Promise<Task> {
    return this.http.post<Task>('/api/v1/inbox/tasks', input).then((r) => r.data);
  },

  /** Phase 2: relocate a task into a column (kanban move). */
  moveTask(taskId: string, columnId: string, position?: number): Promise<Task> {
    const body: Record<string, unknown> = { column_id: columnId };
    if (typeof position === 'number') body.position = position;
    return this.http.post<Task>(`/api/v1/tasks/${taskId}/move`, body).then((r) => r.data);
  },

  /**
   * T164: batch of kanban moves through POST /api/v1/sync — one HTTP
   * round-trip instead of one POST per suffix card (the fan-out that
   * exhausted the per-user rate limiter). The wire shape is 1:1 with
   * the offline outbox's wire() (see shared/offline/outbox.ts), so the
   * server applies identical ops either way. Batches above
   * SYNC_MOVE_CHUNK_SIZE ops are sent in sequential chunks (the
   * endpoint rejects >200 ops with too_many_ops). Per-op failures are
   * swallowed by callers — suffix bumps stay best-effort (a failed
   * bump re-syncs on the next WS refetch).
   */
  async moveTasksBatch(
    moves: Array<{ taskId: string; columnId: string; position?: number }>,
  ): Promise<void> {
    for (let i = 0; i < moves.length; i += SYNC_MOVE_CHUNK_SIZE) {
      const chunk = moves.slice(i, i + SYNC_MOVE_CHUNK_SIZE);
      const ops = chunk.map((m) => ({
        op: 'move_task',
        target: m.taskId,
        payload:
          typeof m.position === 'number'
            ? { column_id: m.columnId, position: m.position }
            : { column_id: m.columnId },
        client_id: crypto.randomUUID(),
        created_at: new Date().toISOString(),
      }));
      await this.http.post<SyncResponse>('/api/v1/sync', { ops });
    }
  },

  // ---- Tags (Phase 13) ----

  listTags(): Promise<{ tags: Tag[] }> {
    return this.http.get<{ tags: Tag[] }>('/api/v1/tags').then((r) => r.data);
  },

  createTag(input: { name: string; color?: string }): Promise<Tag> {
    return this.http.post<Tag>('/api/v1/tags', input).then((r) => r.data);
  },

  updateTag(id: string, input: { name?: string; color?: string }): Promise<Tag> {
    return this.http.patch<Tag>(`/api/v1/tags/${id}`, input).then((r) => r.data);
  },

  deleteTag(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/tags/${id}`).then(() => undefined);
  },

  listTaskTags(taskId: string): Promise<{ tags: Tag[] }> {
    return this.http.get<{ tags: Tag[] }>(`/api/v1/tasks/${taskId}/tags`).then((r) => r.data);
  },

  /** Replace the task's tag set atomically. Empty array = clear. */
  setTaskTags(taskId: string, tagIds: string[]): Promise<{ tags: Tag[] }> {
    return this.http
      .put<{ tags: Tag[] }>(`/api/v1/tasks/${taskId}/tags`, { tag_ids: tagIds })
      .then((r) => r.data);
  },

  // ---- Time tracking (Phase 4) ----

  startTimer(taskId: string): Promise<TimeEntry> {
    return this.http.post<TimeEntry>(`/api/v1/tasks/${taskId}/timer/start`, {}).then((r) => r.data);
  },

  stopTimer(taskId: string): Promise<TimeEntry> {
    return this.http.post<TimeEntry>(`/api/v1/tasks/${taskId}/timer/stop`, {}).then((r) => r.data);
  },

  addManualTime(taskId: string, input: { start_at: string; end_at: string }): Promise<TimeEntry> {
    return this.http.post<TimeEntry>(`/api/v1/tasks/${taskId}/time`, input).then((r) => r.data);
  },

  getTimeReport(params: { agent_id?: string; from?: string; to?: string }): Promise<TimeReport> {
    return this.http.get<TimeReport>('/api/v1/reports/time', { params }).then((r) => r.data);
  },
} satisfies ThisType<ApiClient>;

/** Method surface contributed by this domain to the ApiClient type. */
export type TasksApi = typeof tasksEndpoints;
