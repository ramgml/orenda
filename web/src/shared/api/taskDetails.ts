import type { ApiClient } from './core';
import type { Tag } from './tasks';

export interface Task {
  id: string;
  /**
   * Human-readable sequential task number (`#123`). Assigned by the
   * server on creation and stable for the task's lifetime — agents and
   * humans reference tasks by it in conversation. `0` means the row
   * predates numbering; the UI hides the chip in that case.
   */
  number: number;
  /**
   * Phase 16: empty string is a valid value — represents a task in
   * the Inbox (project_id IS NULL). Use the dedicated /inbox/tasks
   * endpoint to list those, or filter by project_id="" client-side.
   */
  project_id: string;
  parent_task_id?: string;
  column_id?: string;
  title: string;
  description?: string;
  status: string;
  priority: string;
  assignee_type?: string;
  assignee_id?: string;
  awaiting: string;
  context_md?: string;
  agent_notes?: string;
  /**
   * Phase 31: free-form study reminder marker. Empty/undefined
   * means "regular task"; non-empty means "soft reminder from a
   * study proposal". The Today screen reads this to apply the
   * no-escalation read semantics (a missed day never turns red).
   */
  study_course_id?: string;
  /** Calendar fields (Phase 12/14). A task with both start_at and
   * end_at shows on the calendar; otherwise it's a plain kanban row. */
  start_at?: string;
  end_at?: string;
  all_day?: boolean;
  /** Phase 13: hex colour label rendered as a left stripe on cards
   * and on the task sidebar. Empty string (default) means no stripe.
   * The backend always emits this field so PATCH responses can
   * distinguish "no change" from "cleared". */
  color: string;
  /** Phase 27.3: tag set attached to this task. Populated by the
   * server on every endpoint that returns tasks — GET /tasks/{id}
   * and the list endpoints (kanban + inbox). The batch join
   * (TagsForTasks) keeps it O(1) queries regardless of board size,
   * so the kanban card can render tags as chips without per-card
   * fetches. Optional for back-compat with older clients that
   * don't read this field. */
  tags?: Tag[];
  /** Phase 17: per-task counters populated by GET /projects/{id}/tasks
   * and /inbox/tasks (the list endpoints). Always undefined on the
   * single-task endpoint — the card UI treats absence as "0". */
  counters?: TaskCounters;
  /** Phase 17: number of unfinished blockers (Phase 15 graph). Populated
   * by the list endpoints; undefined when the field isn't set. The
   * blocked badge renders only when this is > 0. */
  blocked_by_count?: number;
  /** Task 115: status the task held before an auto-block flipped it to
   * `blocked` (a blocker was added). Non-empty only while status is
   * `blocked`; cleared when the task auto-unblocks or is moved
   * manually. */
  blocked_prev_status?: string;
  /** Task 115: unfinished blockers (id/number/title) for the kanban
   * card tooltip. Populated by the list endpoints next to
   * blocked_by_count; undefined when the field isn't set. */
  blockers?: Array<{ id: string; number: number; title: string }>;
  due_at?: string;
  started_at?: string;
  claimed_at?: string;
  completed_at?: string;
  time_estimate_s?: number;
  time_spent_s: number;
  position: number;
  created_at: string;
  updated_at: string;
}

/**
 * Phase 19: a task awaiting human action, joined with its project name
 * and colour so the /review page can render a single-row layout without
 * extra fetches. Project fields are empty strings for Inbox tasks.
 */
export interface ReviewQueueItem {
  task: Task;
  project_name: string;
  project_color: string;
}

/**
 * T336: a task with awaiting='agent' stranded in a project no agent
 * can reach (agents_allowed=false, zero grant rows). `agent_grants`
 * is always 0 for returned rows — kept on the wire so the payload is
 * self-describing.
 */
export interface AgentStarvedItem {
  task: Task;
  project_name: string;
  agent_grants: number;
}

/**
 * Phase 15: one blocker in a task's dependency graph. `done` is true
 * when the blocker has reached status='done' (or has completed_at set);
 * the UI uses it to grey out satisfied dependencies on the task page.
 */
export interface BlockerRow {
  blocker_id: string;
  title: string;
  status: string;
  done: boolean;
}

/**
 * Phase 17: bundle of per-task counters attached to the list
 * endpoints. Comments/attachments are direct row counts; children
 * and checklist carry done/total so the card can render a progress
 * fraction.
 */
interface TaskCounters {
  comments: number;
  attachments: number;
  children_total: number;
  children_done: number;
  checklist_total: number;
  checklist_done: number;
}

export interface Comment {
  id: string;
  target_type: string;
  target_id: string;
  author_type: 'user' | 'agent';
  author_id: string;
  body_md: string;
  created_at: string;
  edited_at?: string;
}

export interface ChildTaskProgress {
  total: number;
  done: number;
}

export interface TaskAttachment {
  id: string;
  target_type: string;
  target_id: string;
  filename: string;
  mime: string;
  size: number;
  uploaded_by_type: string;
  uploaded_by_id: string;
  created_at: string;
  sha256?: string;
  /** Only populated by GET /projects/{id}/attachments — title of the
   * task this attachment belongs to (empty for project-level rows). */
  task_title?: string;
}

export interface Checklist {
  id: string;
  task_id: string;
  title: string;
  position: number;
}

export interface ChecklistItem {
  id: string;
  checklist_id: string;
  title: string;
  done: boolean;
  position: number;
}

export interface TaskActivity {
  id: string;
  task_id: string;
  actor_type: string;
  actor_id: string;
  action: string;
  payload: string;
  created_at: string;
}

/**
 * Task detail/sub-resource endpoints: comments, child tasks,
 * attachments, checklists, activity, dependencies, timers.
 * Merged onto the ApiClient prototype in client.ts.
 */
export const taskDetailsEndpoints = {
  // ---- Task detail / Phase 3 ----

  getTask(taskId: string): Promise<Task> {
    return this.http.get<Task>(`/api/v1/tasks/${taskId}`).then((r) => r.data);
  },

  listTaskComments(taskId: string): Promise<{ comments: Comment[] }> {
    return this.http
      .get<{ comments: Comment[] }>(`/api/v1/tasks/${taskId}/comments`)
      .then((r) => r.data);
  },

  createTaskComment(taskId: string, body_md: string): Promise<Comment> {
    return this.http
      .post<Comment>(`/api/v1/tasks/${taskId}/comments`, { body_md })
      .then((r) => r.data);
  },

  updateTaskComment(taskId: string, commentId: string, body_md: string): Promise<Comment> {
    return this.http
      .patch<Comment>(`/api/v1/tasks/${taskId}/comments/${commentId}`, { body_md })
      .then((r) => r.data);
  },

  // ---- Child tasks (Phase 14: subtasks → child tasks) ----

  listChildTasks(taskId: string): Promise<{ tasks: Task[]; progress: ChildTaskProgress }> {
    return this.http
      .get<{ tasks: Task[]; progress: ChildTaskProgress }>(`/api/v1/tasks/${taskId}/children`)
      .then((r) => r.data);
  },

  createChildTask(
    projectId: string,
    input: {
      title: string;
      parent_task_id: string;
      status?: string;
      priority?: string;
    },
  ): Promise<Task> {
    return this.http.post<Task>(`/api/v1/projects/${projectId}/tasks`, input).then((r) => r.data);
  },

  updateChildTaskStatus(id: string, status: string): Promise<Task> {
    return this.http.patch<Task>(`/api/v1/tasks/${id}`, { status }).then((r) => r.data);
  },

  deleteChildTask(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/tasks/${id}`).then(() => undefined);
  },

  // ---- Attachments ----

  listTaskAttachments(taskId: string): Promise<{ attachments: TaskAttachment[] }> {
    return this.http
      .get<{ attachments: TaskAttachment[] }>(`/api/v1/tasks/${taskId}/attachments`)
      .then((r) => r.data);
  },

  uploadTaskAttachment(taskId: string, file: File): Promise<TaskAttachment> {
    const form = new FormData();
    form.append('file', file);
    return this.http
      .post<TaskAttachment>(`/api/v1/tasks/${taskId}/attachments`, form, {
        headers: { 'Content-Type': 'multipart/form-data' },
      })
      .then((r) => r.data);
  },

  deleteTaskAttachment(attachmentId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/attachments/${attachmentId}`).then(() => undefined);
  },

  /** Build the absolute URL the browser should hit to download a file.
   * Uses withCredentials so the session cookie is sent. */
  taskAttachmentDownloadUrl(attachmentId: string): string {
    return `/api/v1/attachments/${attachmentId}/download`;
  },

  // ---- Checklists ----

  listChecklists(taskId: string): Promise<{ checklists: Checklist[] }> {
    return this.http
      .get<{ checklists: Checklist[] }>(`/api/v1/tasks/${taskId}/checklists`)
      .then((r) => r.data);
  },

  addChecklist(taskId: string, input: { title: string; position?: number }): Promise<Checklist> {
    return this.http
      .post<Checklist>(`/api/v1/tasks/${taskId}/checklists`, input)
      .then((r) => r.data);
  },

  deleteChecklist(taskId: string, checklistId: string): Promise<void> {
    return this.http
      .delete<void>(`/api/v1/tasks/${taskId}/checklists/${checklistId}`)
      .then(() => undefined);
  },

  addChecklistItem(
    taskId: string,
    checklistId: string,
    input: { title: string; position?: number },
  ): Promise<ChecklistItem> {
    return this.http
      .post<ChecklistItem>(`/api/v1/tasks/${taskId}/checklists/${checklistId}/items`, input)
      .then((r) => r.data);
  },

  listChecklistItems(taskId: string, checklistId: string): Promise<{ items: ChecklistItem[] }> {
    return this.http
      .get<{ items: ChecklistItem[] }>(`/api/v1/tasks/${taskId}/checklists/${checklistId}/items`)
      .then((r) => r.data);
  },

  updateChecklistItem(
    taskId: string,
    checklistId: string,
    itemId: string,
    input: Partial<{ title: string; done: boolean; position: number }>,
  ): Promise<ChecklistItem> {
    return this.http
      .patch<ChecklistItem>(
        `/api/v1/tasks/${taskId}/checklists/${checklistId}/items/${itemId}`,
        input,
      )
      .then((r) => r.data);
  },

  deleteChecklistItem(taskId: string, checklistId: string, itemId: string): Promise<void> {
    return this.http
      .delete<void>(`/api/v1/tasks/${taskId}/checklists/${checklistId}/items/${itemId}`)
      .then(() => undefined);
  },

  // ---- Activity log ----

  listTaskActivity(taskId: string): Promise<{ activity: TaskActivity[] }> {
    return this.http
      .get<{ activity: TaskActivity[] }>(`/api/v1/tasks/${taskId}/activity`)
      .then((r) => r.data);
  },

  // ---- Task dependencies (Phase 15) ----
  //
  // PUT replaces the full blocker set in one shot — empty array clears.
  // GET returns ALL blockers (open + satisfied) so the UI can show
  // "blocked by N (M still open)" without an extra round-trip.

  listTaskBlockers(taskId: string): Promise<{ blockers: BlockerRow[] }> {
    return this.http
      .get<{ blockers: BlockerRow[] }>(`/api/v1/tasks/${taskId}/blockers`)
      .then((r) => r.data);
  },

  setTaskDependencies(taskId: string, dependsOnIds: string[]): Promise<{ blockers: BlockerRow[] }> {
    return this.http
      .put<{ blockers: BlockerRow[] }>(`/api/v1/tasks/${taskId}/dependencies`, {
        depends_on_ids: dependsOnIds,
      })
      .then((r) => r.data);
  },

  /** Task 115: add ONE blocker edge (idempotent; auto-blocks the
   * target). Accepts a UUID or `T<N>` ref for blockedBy. Returns the
   * refreshed blockers plus the task (status may now be `blocked`). */
  addTaskBlocker(
    taskId: string,
    blockedBy: string,
  ): Promise<{ blockers: BlockerRow[]; task: Task }> {
    return this.http
      .post<{ blockers: BlockerRow[]; task: Task }>(`/api/v1/tasks/${taskId}/blocks`, {
        blocked_by: blockedBy,
      })
      .then((r) => r.data);
  },

  /** Task 115: remove ONE blocker edge (unknown edge → 404;
   * auto-unblocks when no unfinished blockers remain). */
  removeTaskBlocker(
    taskId: string,
    blockedBy: string,
  ): Promise<{ blockers: BlockerRow[]; task: Task }> {
    return this.http
      .delete<{ blockers: BlockerRow[]; task: Task }>(`/api/v1/tasks/${taskId}/blocks/${blockedBy}`)
      .then((r) => r.data);
  },
} satisfies ThisType<ApiClient>;

/** Method surface contributed by this domain to the ApiClient type. */
export type TaskDetailsApi = typeof taskDetailsEndpoints;
