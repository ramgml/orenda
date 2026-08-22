import axios, { AxiosError, type AxiosInstance } from 'axios';

/**
 * Capabilities advertised by the server (mirrors Go's api.Capabilities).
 */
export interface Capabilities {
  auth: boolean;
  rest_tasks: boolean;
  websocket: boolean;
  backup: boolean;
  bots: boolean;
  fts: boolean;
  pwa: boolean;
}

export interface InfoResponse {
  version: string;
  name: string;
  capabilities: Capabilities;
}

export interface HealthResponse {
  status: string;
  version: string;
}

/**
 * Phase 24: GET /api/v1/stats. Mirrors `statsResponse` in
 * `internal/api/handlers_stats.go`. Optional fields (last_backup_unix)
 * are only emitted when the server has a backup timestamp; the UI
 * treats them as "not yet".
 */
export interface StatsResponse {
  uptime_seconds: number;
  requests_total: number;
  requests_2xx: number;
  requests_3xx: number;
  requests_4xx: number;
  requests_5xx: number;
  slow_requests: number;
  ws_connections: number;
  db_bytes: number;
  db_path: string;
  last_backup_unix?: number;
}

/**
 * Auth profile returned by GET /api/v1/me and embedded in LoginResponse.
 */
export interface UserProfile {
  user_id: string;
  email: string;
  display_name?: string;
  role?: string;
  scopes?: string[];
}

export type LoginResponse = UserProfile;

/**
 * Phase 18: Course mirrors the server's Course entity. Top-level
 * status lifecycle: draft → review → active → done (+ archived).
 */
export interface Course {
  id: string;
  title: string;
  intent_md?: string;
  level: string;
  pace: string;
  status: 'draft' | 'review' | 'active' | 'done' | 'archived';
  owner_id: string;
  /**
   * Human-readable sequential course number ("C7"). Assigned by the
   * storage layer on CreateCourse from the course_number_seq high-watermark.
   */
  number: number;
  /**
   * Phase 31: free-form pace notes. Set by the agent-planner
   * through PATCH /api/v1/agent/courses/{id} and read by the
   * Dashboard tray / Today suggestions. omitempty so the field
   * doesn't pollute legacy payloads.
   */
  pace_notes_md?: string;
  created_at: string;
  updated_at: string;
}

/**
 * Phase 30.13: named row types for the course tree. These mirror the
 * server's course.Module / course.Lesson / course.Quiz entities and
 * are shared by getCourse, the granular structure endpoints, and the
 * structure-reorder response (all emit the same tree shape).
 */
export interface CourseModule {
  id: string;
  course_id: string;
  title: string;
  description?: string;
  position: number;
}

export interface CourseLesson {
  id: string;
  module_id: string;
  title: string;
  position: number;
  status: string;
  /**
   * Human-readable sequential lesson number ("L10"). Assigned by the
   * storage layer on CreateLesson from the lesson_number_seq high-watermark.
   */
  number: number;
  // Phase 27.4: lesson body and exercise link. The backend emits
  // these on every tree response (listLessonsInCourse scans
  // `content_md` and `task_id`), so the frontend can resolve a
  // single lesson without an extra round-trip.
  content_md?: string;
  task_id?: string;
}

export interface CourseQuiz {
  id: string;
  lesson_id: string;
  position: number;
  question_md: string;
  expected_md?: string;
  kind: 'open' | 'exact';
}

/** Full course tree — the shape of GET /api/v1/courses/{id} and of
 *  PUT /api/v1/courses/{id}/structure (Phase 30.13). */
export interface CourseTree {
  course: Course;
  modules: CourseModule[];
  lessons: CourseLesson[];
  quizzes?: CourseQuiz[];
  progress: { lessons_total: number; lessons_done: number };
}

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

export interface Board {
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

/** Phase 13: a global label that can be attached to tasks. */
export interface Tag {
  id: string;
  name: string;
  color?: string;
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
export interface TaskCounters {
  comments: number;
  attachments: number;
  children_total: number;
  children_done: number;
  checklist_total: number;
  checklist_done: number;
}

// StudyProposalView — Phase 31.9: the lightweight projection the
// Dashboard tray renders. The full Proposal entity (with body_md,
// accepted_task_id, resolved_at) stays in the agent namespace.
export interface StudyProposalView {
  id: string;
  course_id?: string;
  title: string;
  body_md?: string;
  target_date: string; // YYYY-MM-DD
  agent_id: string;
  created_at: string;
}

// StudyProposalFull — returned by the accept/dismiss endpoints
// because the user tray may want to confirm the title / agent after
// the action. Phase 31.9.
export interface StudyProposalFull {
  id: string;
  course_id?: string;
  title: string;
  body_md?: string;
  target_date: string;
  status: 'pending' | 'accepted' | 'dismissed';
  created_by_agent: string;
  accepted_task_id?: string;
  created_at: string;
  resolved_at?: string;
}

export interface Agent {
  id: string;
  name: string;
  /**
   * Phase 28.19: free-form label set, normalised on the server
   * (trimmed, lowercased, deduped, sorted). Empty arrays are valid.
   * Use `agent.type.join(', ')` for compact display; for filtering,
   * see `listAgents({ type: [...] })` below.
   */
  type: string[];
  description?: string;
  token_id: string;
  last_seen_at?: string;
  status: 'online' | 'offline' | 'disabled';
  max_concurrent: number;
  created_at: string;
}

export interface Comment {
  id: string;
  target_type: string;
  target_id: string;
  author_type: 'user' | 'agent';
  author_id: string;
  body_md: string;
  created_at: string;
}

/**
 * Typed wrapper around axios for the Orenda REST API.
 *
 * Cookie-based auth is configured via `withCredentials: true`. A 401 response
 * triggers the onUnauthorized callback so the AuthProvider can drop state and
 * redirect to /login.
 */
class ApiClient {
  private http: AxiosInstance;
  private onUnauthorized: (() => void) | null = null;

  constructor(baseURL: string = '') {
    this.http = axios.create({
      baseURL,
      withCredentials: true,
      timeout: 15_000,
      headers: { 'Content-Type': 'application/json' },
    });
    this.http.interceptors.response.use(
      (r) => r,
      (err: unknown) => {
        if (axios.isAxiosError(err) && err.response?.status === 401) {
          this.onUnauthorized?.();
        }
        return Promise.reject(err);
      },
    );
  }

  /** Register a callback invoked on every 401 response. */
  onAuthFailure(cb: () => void): void {
    this.onUnauthorized = cb;
  }

  health(): Promise<HealthResponse> {
    return this.http.get<HealthResponse>('/healthz').then((r) => r.data);
  }

  info(): Promise<InfoResponse> {
    return this.http.get<InfoResponse>('/api/v1/info').then((r) => r.data);
  }

  me(): Promise<UserProfile> {
    return this.http.get<UserProfile>('/api/v1/me').then((r) => r.data);
  }

  /**
   * Phase 24 observable snapshot. The Settings index (Phase 28.2)
   * renders this in the About panel; uptime + DB size are the two
   * fields humans actually look at. No auth required — the endpoint
   * is intentionally public.
   */
  getStats(): Promise<StatsResponse> {
    return this.http.get<StatsResponse>('/api/v1/stats').then((r) => r.data);
  }

  login(email: string, password: string): Promise<LoginResponse> {
    return this.http
      .post<LoginResponse>('/api/v1/auth/login', { email, password })
      .then((r) => r.data);
  }

  logout(): Promise<void> {
    return this.http.post<void>('/api/v1/auth/logout').then(() => undefined);
  }

  listProjects(): Promise<Project[]> {
    return this.http.get<{ projects: Project[] }>('/api/v1/projects').then((r) => r.data.projects);
  }

  createProject(input: { name: string; color?: string; description?: string }): Promise<Project> {
    return this.http.post<Project>('/api/v1/projects', input).then((r) => r.data);
  }

  updateProject(
    projectId: string,
    input: Partial<{
      name: string;
      color: string;
      description: string;
      wiki_slug: string;
      archived: boolean;
    }>,
  ): Promise<Project> {
    return this.http.patch<Project>(`/api/v1/projects/${projectId}`, input).then((r) => r.data);
  }

  getProject(projectId: string): Promise<Project> {
    return this.http.get<Project>(`/api/v1/projects/${projectId}`).then((r) => r.data);
  }

  deleteProject(projectId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/projects/${projectId}`).then(() => undefined);
  }

  getBoard(projectId: string): Promise<ProjectBoard> {
    return this.http.get<ProjectBoard>(`/api/v1/projects/${projectId}/board`).then((r) => r.data);
  }

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
  }

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
  }

  /** Remove a column. Throws AxiosError with response.status === 422
   *  (and response.data.current = N) when the column still holds N
   *  tasks — the UI uses that count to render a helpful hint. 404
   *  means the column is already gone (idempotent from the user's
   *  POV). */
  deleteColumn(columnId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/columns/${columnId}`).then(() => undefined);
  }

  listProjectTasks(
    projectId: string,
    params?: { status?: string; column_id?: string },
  ): Promise<Task[]> {
    return this.http
      .get<{ tasks: Task[] }>(`/api/v1/projects/${projectId}/tasks`, { params })
      .then((r) => r.data.tasks);
  }

  // ---- Task detail / Phase 3 ----

  getTask(taskId: string): Promise<Task> {
    return this.http.get<Task>(`/api/v1/tasks/${taskId}`).then((r) => r.data);
  }

  listTaskComments(taskId: string): Promise<{ comments: Comment[] }> {
    return this.http
      .get<{ comments: Comment[] }>(`/api/v1/tasks/${taskId}/comments`)
      .then((r) => r.data);
  }

  createTaskComment(taskId: string, body_md: string): Promise<Comment> {
    return this.http
      .post<Comment>(`/api/v1/tasks/${taskId}/comments`, { body_md })
      .then((r) => r.data);
  }

  // ---- Child tasks (Phase 14: subtasks → child tasks) ----

  listChildTasks(taskId: string): Promise<{ tasks: Task[]; progress: ChildTaskProgress }> {
    return this.http
      .get<{ tasks: Task[]; progress: ChildTaskProgress }>(`/api/v1/tasks/${taskId}/children`)
      .then((r) => r.data);
  }

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
  }

  updateChildTaskStatus(id: string, status: string): Promise<Task> {
    return this.http.patch<Task>(`/api/v1/tasks/${id}`, { status }).then((r) => r.data);
  }

  deleteChildTask(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/tasks/${id}`).then(() => undefined);
  }

  // ---- Attachments ----

  listTaskAttachments(taskId: string): Promise<{ attachments: TaskAttachment[] }> {
    return this.http
      .get<{ attachments: TaskAttachment[] }>(`/api/v1/tasks/${taskId}/attachments`)
      .then((r) => r.data);
  }

  uploadTaskAttachment(taskId: string, file: File): Promise<TaskAttachment> {
    const form = new FormData();
    form.append('file', file);
    return this.http
      .post<TaskAttachment>(`/api/v1/tasks/${taskId}/attachments`, form, {
        headers: { 'Content-Type': 'multipart/form-data' },
      })
      .then((r) => r.data);
  }

  deleteTaskAttachment(attachmentId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/attachments/${attachmentId}`).then(() => undefined);
  }

  /** Build the absolute URL the browser should hit to download a file.
   * Uses withCredentials so the session cookie is sent. */
  taskAttachmentDownloadUrl(attachmentId: string): string {
    return `/api/v1/attachments/${attachmentId}/download`;
  }

  // ---- Project attachments (Phase 11) ----

  listProjectAttachments(projectId: string): Promise<{ attachments: TaskAttachment[] }> {
    return this.http
      .get<{ attachments: TaskAttachment[] }>(`/api/v1/projects/${projectId}/attachments`)
      .then((r) => r.data);
  }

  uploadProjectAttachment(projectId: string, file: File): Promise<TaskAttachment> {
    const form = new FormData();
    form.append('file', file);
    return this.http
      .post<TaskAttachment>(`/api/v1/projects/${projectId}/attachments`, form, {
        headers: { 'Content-Type': 'multipart/form-data' },
      })
      .then((r) => r.data);
  }

  // ---- Checklists ----

  listChecklists(taskId: string): Promise<{ checklists: Checklist[] }> {
    return this.http
      .get<{ checklists: Checklist[] }>(`/api/v1/tasks/${taskId}/checklists`)
      .then((r) => r.data);
  }

  addChecklist(taskId: string, input: { title: string; position?: number }): Promise<Checklist> {
    return this.http
      .post<Checklist>(`/api/v1/tasks/${taskId}/checklists`, input)
      .then((r) => r.data);
  }

  deleteChecklist(taskId: string, checklistId: string): Promise<void> {
    return this.http
      .delete<void>(`/api/v1/tasks/${taskId}/checklists/${checklistId}`)
      .then(() => undefined);
  }

  addChecklistItem(
    taskId: string,
    checklistId: string,
    input: { title: string; position?: number },
  ): Promise<ChecklistItem> {
    return this.http
      .post<ChecklistItem>(`/api/v1/tasks/${taskId}/checklists/${checklistId}/items`, input)
      .then((r) => r.data);
  }

  listChecklistItems(taskId: string, checklistId: string): Promise<{ items: ChecklistItem[] }> {
    return this.http
      .get<{ items: ChecklistItem[] }>(`/api/v1/tasks/${taskId}/checklists/${checklistId}/items`)
      .then((r) => r.data);
  }

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
  }

  deleteChecklistItem(taskId: string, checklistId: string, itemId: string): Promise<void> {
    return this.http
      .delete<void>(`/api/v1/tasks/${taskId}/checklists/${checklistId}/items/${itemId}`)
      .then(() => undefined);
  }

  // ---- Tags (Phase 13) ----

  listTags(): Promise<{ tags: Tag[] }> {
    return this.http.get<{ tags: Tag[] }>('/api/v1/tags').then((r) => r.data);
  }

  createTag(input: { name: string; color?: string }): Promise<Tag> {
    return this.http.post<Tag>('/api/v1/tags', input).then((r) => r.data);
  }

  updateTag(id: string, input: { name?: string; color?: string }): Promise<Tag> {
    return this.http.patch<Tag>(`/api/v1/tags/${id}`, input).then((r) => r.data);
  }

  deleteTag(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/tags/${id}`).then(() => undefined);
  }

  listTaskTags(taskId: string): Promise<{ tags: Tag[] }> {
    return this.http.get<{ tags: Tag[] }>(`/api/v1/tasks/${taskId}/tags`).then((r) => r.data);
  }

  /** Replace the task's tag set atomically. Empty array = clear. */
  setTaskTags(taskId: string, tagIds: string[]): Promise<{ tags: Tag[] }> {
    return this.http
      .put<{ tags: Tag[] }>(`/api/v1/tasks/${taskId}/tags`, { tag_ids: tagIds })
      .then((r) => r.data);
  }

  // ---- Activity log ----

  listTaskActivity(taskId: string): Promise<{ activity: TaskActivity[] }> {
    return this.http
      .get<{ activity: TaskActivity[] }>(`/api/v1/tasks/${taskId}/activity`)
      .then((r) => r.data);
  }

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
  }

  createTask(
    projectId: string,
    input: { title: string; column_id?: string; description?: string },
  ): Promise<Task> {
    return this.http.post<Task>(`/api/v1/projects/${projectId}/tasks`, input).then((r) => r.data);
  }

  patchTask(taskId: string, input: Partial<Task>): Promise<Task> {
    return this.http.patch<Task>(`/api/v1/tasks/${taskId}`, input).then((r) => r.data);
  }

  bulkPatchTasks(input: {
    task_ids: string[];
    patch: Partial<Task>;
  }): Promise<{ tasks: Task[]; errors?: Record<string, string> }> {
    return this.http
      .post<{ tasks: Task[]; errors?: Record<string, string> }>('/api/v1/tasks/bulk-edit', input)
      .then((r) => r.data);
  }

  deleteTask(taskId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/tasks/${taskId}`).then(() => undefined);
  }

  // ---- Inbox (Phase 16) ----
  //
  // The Inbox is the set of tasks with project_id IS NULL. It's a
  // flat list (no board, no columns) — the user files these cards
  // onto a real project via PATCH /tasks/{id} {project_id: "..."}.

  /** List tasks in the Inbox (project_id IS NULL), newest first. */
  listInboxTasks(params?: { status?: string }): Promise<{ tasks: Task[] }> {
    return this.http.get<{ tasks: Task[] }>('/api/v1/inbox/tasks', { params }).then((r) => r.data);
  }

  /**
   * Phase 30.8: tasks with a due_at in [from, to]. The calendar uses
   * this to render deadlines alongside timed events. The returned
   * tasks are unordered by the API; the calendar sorts by date.
   */
  tasksWithDue(params: { from: string; to: string }): Promise<{ tasks: Task[] }> {
    return this.http
      .get<{ tasks: Task[] }>('/api/v1/tasks/with-due', { params })
      .then((r) => r.data);
  }

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
  }

  /** Phase 2: relocate a task into a column (kanban move). */
  moveTask(taskId: string, columnId: string, position?: number): Promise<Task> {
    const body: Record<string, unknown> = { column_id: columnId };
    if (typeof position === 'number') body.position = position;
    return this.http.post<Task>(`/api/v1/tasks/${taskId}/move`, body).then((r) => r.data);
  }

  // ---- Agents (Phase 3 + Phase 28.19) ----

  /**
   * List agents, optionally filtered by label. Phase 28.19: `type`
   * is a free-form label set; the OR filter returns every agent that
   * carries at least one of the requested labels. Without a filter
   * every agent is returned.
   */
  listAgents(filter?: { type?: string[] }): Promise<Agent[]> {
    // Manually build the query string so repeated `type=` params
    // render as `?type=a&type=b` (server reads via r.URL.Query()["type"]).
    // `new URLSearchParams({type: ['a','b']})` would emit a comma-joined
    // single value instead, which the backend's OR-filter would treat as
    // one literal label and never match.
    let qs = '';
    if (filter?.type && filter.type.length > 0) {
      const params = new URLSearchParams();
      for (const t of filter.type) params.append('type', t);
      qs = `?${params.toString()}`;
    }
    return this.http.get<{ agents: Agent[] }>(`/api/v1/agents${qs}`).then((r) => r.data.agents);
  }

  createAgent(input: {
    name: string;
    /**
     * Phase 28.19: free-form labels, e.g. ["qwen"] or
     * ["qwen","installer"]. The server normalises the set
     * (trim/lowercase/dedupe/sort). Empty arrays are valid.
     */
    type?: string[];
    description?: string;
  }): Promise<{ agent: Agent; plain_token: string }> {
    return this.http
      .post<{ agent: Agent; plain_token: string }>('/api/v1/agents', input)
      .then((r) => r.data);
  }

  deleteAgent(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/agents/${id}`).then(() => undefined);
  }

  // ---- Agent-token namespace (/api/v1/agent/*) ----

  agentMe(): Promise<Agent> {
    return this.http.get<Agent>('/api/v1/agent/me').then((r) => r.data);
  }

  agentHeartbeat(): Promise<Agent> {
    return this.http.post<Agent>('/api/v1/agent/heartbeat', {}).then((r) => r.data);
  }

  agentClaimTask(taskId: string): Promise<Task> {
    return this.http.post<Task>(`/api/v1/agent/tasks/${taskId}/claim`, {}).then((r) => r.data);
  }

  agentSubmitTask(taskId: string, note: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/agent/tasks/${taskId}/submit`, { note })
      .then((r) => r.data);
  }

  agentReleaseTask(taskId: string): Promise<Task> {
    return this.http.post<Task>(`/api/v1/agent/tasks/${taskId}/release`, {}).then((r) => r.data);
  }

  // ---- Review (cookie-authenticated user side) ----

  reviewTask(taskId: string, decision: 'approve' | 'reject', comment?: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/tasks/${taskId}/review`, { decision, comment: comment ?? '' })
      .then((r) => r.data);
  }

  // ---- Review queue (Phase 19) ----
  //
  // One endpoint surfaces the union of "awaiting=human" and
  // "status=review" tasks, joined with their project name + colour.
  // The same handler powers the /review page and the sidebar badge.

  listReviewQueue(): Promise<{ tasks: ReviewQueueItem[]; count: number }> {
    return this.http
      .get<{ tasks: ReviewQueueItem[]; count: number }>(`/api/v1/review-queue`)
      .then((r) => r.data);
  }

  getReviewQueueCount(): Promise<{ count: number }> {
    return this.http.get<{ count: number }>(`/api/v1/review-queue/count`).then((r) => r.data);
  }

  // ---- Today (Phase 20) ----

  getToday(): Promise<{
    overdue: Task[];
    due_today: Task[];
    scheduled_today: Task[];
    upcoming_week: { date: string; count: number }[];
    awaiting_count: number;
    active_timer?: { task_id: string; started_at: string };
    // Phase 31.9: pending study proposals for the Dashboard tray.
    // Empty array when none; never null.
    proposals: StudyProposalView[];
  }> {
    return this.http
      .get<{
        overdue: Task[];
        due_today: Task[];
        scheduled_today: Task[];
        upcoming_week: { date: string; count: number }[];
        awaiting_count: number;
        active_timer?: { task_id: string; started_at: string };
        proposals: StudyProposalView[];
      }>(`/api/v1/today`)
      .then((r) => r.data);
  }

  // ---- Study proposals (Phase 31.9) ----

  // Lightweight projection of a pending study proposal as rendered
  // on the Dashboard tray. The full Proposal entity (with body_md,
  // accepted_task_id, etc.) stays in the agent namespace.
  listStudyProposals(): Promise<{ proposals: StudyProposalView[] }> {
    return this.http
      .get<{ proposals: StudyProposalView[] }>(`/api/v1/study-proposals`)
      .then((r) => r.data);
  }

  acceptStudyProposal(id: string): Promise<{
    proposal: StudyProposalFull;
    task: Task;
    already_accepted: boolean;
  }> {
    return this.http
      .post<{
        proposal: StudyProposalFull;
        task: Task;
        already_accepted: boolean;
      }>(`/api/v1/study-proposals/${id}/accept`)
      .then((r) => r.data);
  }

  dismissStudyProposal(id: string): Promise<{ proposal: StudyProposalFull }> {
    return this.http
      .post<{ proposal: StudyProposalFull }>(`/api/v1/study-proposals/${id}/dismiss`)
      .then((r) => r.data);
  }

  // ---- Task dependencies (Phase 15) ----
  //
  // PUT replaces the full blocker set in one shot — empty array clears.
  // GET returns ALL blockers (open + satisfied) so the UI can show
  // "blocked by N (M still open)" without an extra round-trip.

  listTaskBlockers(taskId: string): Promise<{ blockers: BlockerRow[] }> {
    return this.http
      .get<{ blockers: BlockerRow[] }>(`/api/v1/tasks/${taskId}/blockers`)
      .then((r) => r.data);
  }

  setTaskDependencies(taskId: string, dependsOnIds: string[]): Promise<{ blockers: BlockerRow[] }> {
    return this.http
      .put<{ blockers: BlockerRow[] }>(`/api/v1/tasks/${taskId}/dependencies`, {
        depends_on_ids: dependsOnIds,
      })
      .then((r) => r.data);
  }

  // ---- Events (Phase 4) ----

  listEvents(params: { from: string; to: string; project_id?: string }): Promise<CalendarEvent[]> {
    return this.http
      .get<{ events: CalendarEvent[] | null }>('/api/v1/events', { params })
      .then((r) => r.data.events ?? []);
  }

  createEvent(input: {
    title: string;
    description?: string;
    start_at: string;
    end_at: string;
    all_day?: boolean;
    color?: string;
    project_id?: string;
    recurrence?: string;
  }): Promise<CalendarEvent> {
    return this.http.post<CalendarEvent>('/api/v1/events', input).then((r) => r.data);
  }

  patchEvent(
    id: string,
    input: Partial<{
      title: string;
      description: string;
      start_at: string;
      end_at: string;
      all_day: boolean;
      color: string;
      project_id: string;
      recurrence: string;
    }>,
  ): Promise<CalendarEvent> {
    return this.http.patch<CalendarEvent>(`/api/v1/events/${id}`, input).then((r) => r.data);
  }

  deleteEvent(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/events/${id}`).then(() => undefined);
  }

  // ---- Time tracking (Phase 4) ----

  startTimer(taskId: string): Promise<TimeEntry> {
    return this.http.post<TimeEntry>(`/api/v1/tasks/${taskId}/timer/start`, {}).then((r) => r.data);
  }

  stopTimer(taskId: string): Promise<TimeEntry> {
    return this.http.post<TimeEntry>(`/api/v1/tasks/${taskId}/timer/stop`, {}).then((r) => r.data);
  }

  addManualTime(taskId: string, input: { start_at: string; end_at: string }): Promise<TimeEntry> {
    return this.http.post<TimeEntry>(`/api/v1/tasks/${taskId}/time`, input).then((r) => r.data);
  }

  getTimeReport(params: { agent_id?: string; from?: string; to?: string }): Promise<TimeReport> {
    return this.http.get<TimeReport>('/api/v1/reports/time', { params }).then((r) => r.data);
  }

  // ---- Courses (Phase 18) ----
  //
  // Phase 18 ships the courses API; the frontend mirrors two screens:
  // a list (with create wizard) and a tree view (modules + lessons +
  // progress).

  listCourses(): Promise<{ courses: Course[]; count: number }> {
    return this.http
      .get<{ courses: Course[]; count: number }>('/api/v1/courses')
      .then((r) => r.data);
  }

  createCourse(input: {
    title: string;
    intent_md?: string;
    // Phase 27.6: when true, the owner intends to build the
    // curriculum themselves; the server skips the agent generator
    // task so a sleeping tutor can't overwrite manual work.
    skip_generator?: boolean;
  }): Promise<Course> {
    return this.http.post<Course>('/api/v1/courses', input).then((r) => r.data);
  }

  getCourse(id: string): Promise<CourseTree> {
    return this.http.get<CourseTree>(`/api/v1/courses/${id}`).then((r) => r.data);
  }

  approveCourse(id: string): Promise<Course> {
    return this.http.post<Course>(`/api/v1/courses/${id}/approve`, {}).then((r) => r.data);
  }

  requestCourseChanges(id: string): Promise<Course> {
    return this.http.post<Course>(`/api/v1/courses/${id}/request-changes`, {}).then((r) => r.data);
  }

  completeLesson(id: string): Promise<unknown> {
    return this.http.post<unknown>(`/api/v1/lessons/${id}/complete`, {}).then((r) => r.data);
  }

  // Phase 27.4: submit a quiz answer. Returns the grading result:
  // exact quizzes come back with `correct` set; open quizzes come
  // back with `review_task_id` for the tutor agent to claim.
  answerQuiz(
    lessonId: string,
    quizId: string,
    answer: string,
  ): Promise<{ correct: boolean; feedback_md?: string; review_task_id?: string }> {
    return this.http
      .post<{
        correct: boolean;
        feedback_md?: string;
        review_task_id?: string;
      }>(`/api/v1/lessons/${lessonId}/quizzes/${quizId}/answer`, { answer })
      .then((r) => r.data);
  }

  // ---- Phase 27.6: owner-side curriculum editor + quiz surface ----
  //
  // The owner can build the program themselves instead of waiting on
  // a tutor. submitCurriculum is the atomic swap the tutor already
  // uses — the service detects the user-side path and retires the
  // generator task so a sleeping tutor can't overwrite manual work.
  // addQuiz appends a single quiz to a lesson (no swap required);
  // updateLessonContent edits a lesson's body without touching its
  // lifecycle. The owner of an active course uses this to fix typos
  // and re-wordings once the program is live.

  submitCurriculum(
    courseId: string,
    payload: {
      modules: {
        id?: string;
        title: string;
        description?: string;
        position: number;
        lessons: {
          id?: string;
          title: string;
          position: number;
          content_md?: string;
          quizzes?: {
            id?: string;
            position: number;
            question_md: string;
            expected_md?: string;
            kind: 'exact' | 'open';
          }[];
        }[];
      }[];
    },
  ): Promise<{ status: string }> {
    return this.http
      .put<{ status: string }>(`/api/v1/courses/${courseId}/curriculum`, payload)
      .then((r) => r.data);
  }

  addQuiz(
    lessonId: string,
    input: {
      position?: number;
      question_md: string;
      expected_md?: string;
      kind: 'exact' | 'open';
    },
  ): Promise<CourseQuiz> {
    return this.http
      .post<CourseQuiz>(`/api/v1/lessons/${lessonId}/quizzes`, input)
      .then((r) => r.data);
  }

  updateLessonContent(
    lessonId: string,
    input: { content_md: string; task_id?: string },
  ): Promise<unknown> {
    return this.http.put(`/api/v1/lessons/${lessonId}/content`, input).then((r) => r.data);
  }

  // ---- Phase 30.13: granular structure edits (stable IDs) ----
  //
  // The atomic swap above is destructive (rows are reinserted, so
  // lesson status/progress is lost). For active courses the UI edits
  // via these surgical endpoints so student progress survives.

  createCourseModule(
    courseId: string,
    input: { title: string; description?: string },
  ): Promise<CourseModule> {
    return this.http
      .post<CourseModule>(`/api/v1/courses/${courseId}/modules`, input)
      .then((r) => r.data);
  }

  updateModule(id: string, input: { title: string; description?: string }): Promise<CourseModule> {
    return this.http.patch<CourseModule>(`/api/v1/modules/${id}`, input).then((r) => r.data);
  }

  deleteModule(id: string): Promise<void> {
    return this.http.delete(`/api/v1/modules/${id}`).then(() => undefined);
  }

  createModuleLesson(moduleId: string, input: { title: string }): Promise<CourseLesson> {
    return this.http
      .post<CourseLesson>(`/api/v1/modules/${moduleId}/lessons`, input)
      .then((r) => r.data);
  }

  renameLesson(id: string, title: string): Promise<CourseLesson> {
    return this.http.patch<CourseLesson>(`/api/v1/lessons/${id}`, { title }).then((r) => r.data);
  }

  deleteLesson(id: string): Promise<void> {
    return this.http.delete(`/api/v1/lessons/${id}`).then(() => undefined);
  }

  updateQuiz(
    qid: string,
    input: { question_md: string; expected_md?: string; kind?: 'exact' | 'open' },
  ): Promise<CourseQuiz> {
    return this.http.patch<CourseQuiz>(`/api/v1/quizzes/${qid}`, input).then((r) => r.data);
  }

  deleteQuiz(qid: string): Promise<void> {
    return this.http.delete(`/api/v1/quizzes/${qid}`).then(() => undefined);
  }

  applyCourseStructure(
    courseId: string,
    modules: { module_id: string; lesson_ids: string[] }[],
  ): Promise<CourseTree> {
    return this.http
      .put<CourseTree>(`/api/v1/courses/${courseId}/structure`, { modules })
      .then((r) => r.data);
  }

  // ---- Wiki (Phase 5) ----

  listPages(): Promise<{ tree: WikiTreeNode[] }> {
    return this.http.get<{ tree: WikiTreeNode[] }>('/api/v1/pages').then((r) => r.data);
  }

  getPageBySlug(slug: string): Promise<WikiPage> {
    return this.http.get<WikiPage>(`/api/v1/pages/${slug}`).then((r) => r.data);
  }

  savePage(input: {
    slug: string;
    title: string;
    content_md?: string;
    parent_id?: string;
  }): Promise<WikiPage> {
    // POST creates; PUT on /pages/{slug} updates the same record.
    return this.http.post<WikiPage>('/api/v1/pages', input).then((r) => r.data);
  }

  updatePage(
    slug: string,
    input: Partial<{
      slug: string;
      title: string;
      content_md: string;
      parent_id: string;
      position: number;
    }>,
  ): Promise<WikiPage> {
    return this.http.put<WikiPage>(`/api/v1/pages/${slug}`, input).then((r) => r.data);
  }

  getPageBacklinks(slug: string): Promise<{ backlinks: WikiPage[] }> {
    return this.http
      .get<{ backlinks: WikiPage[] }>(`/api/v1/pages/${slug}/backlinks`)
      .then((r) => r.data);
  }

  deletePage(slug: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/pages/${slug}`).then(() => undefined);
  }

  /** Move a page under a new parent. Empty parent_id → root. */
  movePage(slug: string, parent_id: string): Promise<void> {
    return this.http.patch<void>(`/api/v1/pages/${slug}/move`, { parent_id }).then(() => undefined);
  }

  // ---- Search (Phase 5) ----

  search(params: {
    q: string;
    type?: string;
    limit?: number;
  }): Promise<{ hits: SearchHit[]; total: number }> {
    return this.http
      .get<{ hits: SearchHit[]; total: number }>('/api/v1/search', { params })
      .then((r) => r.data);
  }

  // ---- Notifications (Phase 6) ----

  listNotifications(params?: {
    limit?: number;
  }): Promise<{ notifications: Notification[]; unread: number }> {
    return this.http
      .get<{ notifications: Notification[]; unread: number }>('/api/v1/notifications', { params })
      .then((r) => r.data);
  }

  markNotificationRead(id: string): Promise<void> {
    return this.http.post<void>(`/api/v1/notifications/${id}/read`).then(() => undefined);
  }

  // ---- Backups (Phase 7) ----

  getBackupSettings(): Promise<BackupSettings> {
    return this.http.get<BackupSettings>('/api/v1/backups/settings').then((r) => r.data);
  }

  /**
   * Phase 28.1 (polish.1): PUT /api/v1/backups/settings. Persists
   * the (enabled, remote_url, remote_auth) tuple to the
   * backup_settings table; the response body mirrors GET so the UI
   * can update from the response without an extra round-trip.
   *
   * Settings take effect on the next process restart (the
   * `*backup.Service` is wired from cfg at startup). The UI
   * surfaces this via the `source_hint` field on GET — when it
   * reads "ui_override_restart_to_apply", the form shows a
   * banner.
   */
  setBackupSettings(body: BackupSettingsInput): Promise<BackupSettings> {
    return this.http.put<BackupSettings>('/api/v1/backups/settings', body).then((r) => r.data);
  }

  testBackupPush(): Promise<{ status: string }> {
    return this.http.post<{ status: string }>('/api/v1/backups/test', {}).then((r) => r.data);
  }

  /**
   * Phase 30.9: read-only backup status (snapshot count + latest
   * path/size). No side-effects; safe to poll.
   */
  getBackupStatus(): Promise<{
    scheduler_disabled: boolean;
    snapshot_count?: number;
    latest_snapshot?: string;
    latest_snapshot_size?: number;
    latest_snapshot_unix?: number;
    snapshot_error?: string;
  }> {
    return this.http.get('/api/v1/backups/status').then((r) => r.data);
  }

  createSnapshot(): Promise<{ path: string }> {
    return this.http.post<{ path: string }>('/api/v1/backups/snapshot', {}).then((r) => r.data);
  }

  listBackupSnapshots(): Promise<{ snapshots: BackupSnapshot[] }> {
    return this.http
      .get<{ snapshots: BackupSnapshot[] }>('/api/v1/backups/snapshots')
      .then((r) => r.data);
  }

  listBackupLog(params?: { limit?: number }): Promise<{ log: BackupLogEntry[] }> {
    return this.http
      .get<{ log: BackupLogEntry[] }>('/api/v1/backups/log', { params })
      .then((r) => r.data);
  }

  /** Trigger a restore from a snapshot. Phase 22.3 added the
   * `force=true` path that, combined with maintenance mode, runs
   * the swap in-process. The default call (no `force`) still
   * returns the CLI hint for backward compat.
   *
   * The UI wraps this with maintenance-mode on/off so the
   * in-process path stays one click away. */
  restoreBackup(
    path: string,
    opts?: { force?: boolean },
  ): Promise<{ snapshot: string; hint?: string; status?: string }> {
    return this.http
      .post<{
        snapshot: string;
        hint?: string;
        status?: string;
      }>('/api/v1/backups/restore', { path, force: !!opts?.force })
      .then((r) => r.data);
  }

  // ---- Maintenance mode (Phase 22.3) ----
  //
  // While maintenance is on the API blocks non-GET methods
  // (except /maintenance/off itself). The UI flips it on before
  // a restore and off once the operator is ready.

  maintenanceOn(): Promise<{ maintenance: true }> {
    return this.http.post<{ maintenance: true }>('/api/v1/maintenance/on', {}).then((r) => r.data);
  }

  maintenanceOff(): Promise<{ maintenance: false }> {
    return this.http
      .post<{ maintenance: false }>('/api/v1/maintenance/off', {})
      .then((r) => r.data);
  }

  maintenanceStatus(): Promise<{ maintenance: boolean }> {
    return this.http.get<{ maintenance: boolean }>('/api/v1/maintenance').then((r) => r.data);
  }

  // ---- Bot subscriptions (Phase 10) ----

  listSubscriptions(): Promise<{ subscriptions: BotSubscription[] }> {
    return this.http
      .get<{ subscriptions: BotSubscription[] }>('/api/v1/notifications/subscriptions')
      .then((r) => r.data);
  }

  createSubscription(input: {
    bot_type: string;
    target_address: string;
    events: string[];
    enabled: boolean;
  }): Promise<BotSubscription> {
    return this.http
      .post<BotSubscription>('/api/v1/notifications/subscriptions', input)
      .then((r) => r.data);
  }

  deleteSubscription(id: string): Promise<void> {
    return this.http
      .delete<void>(`/api/v1/notifications/subscriptions/${id}`)
      .then(() => undefined);
  }

  // Phase 22.3 follow-up: resolve a `/start`-issued Telegram bind
  // code into a chat id and create the default subscription
  // server-side. Returns the resolved chat id + the new
  // subscription id so the UI can show a success banner and
  // refresh the list.
  bindTelegram(input: { code: string }): Promise<{
    chat_id: number;
    username?: string;
    subscription_id: string;
  }> {
    return this.http
      .post<{
        chat_id: number;
        username?: string;
        subscription_id: string;
      }>('/api/v1/bots/telegram/bind', input)
      .then((r) => r.data);
  }

  /**
   * Phase 10 Test send UI: deliver a one-off message through any
   * configured bot. The handler ignores the subscription store —
   * the address is whatever the user types in the form, even if
   * no subscription exists yet. Returns the server's
   * `ok: true` payload on success.
   *
   * On failure the server returns one of:
   *   - 400 `invalid_input`        (missing bot_type / target_address)
   *   - 400 `unknown_bot_type`     (bot_type not in knownTestBotTypes)
   *   - 400 per-bot target pre-check (e.g. webhook must be http(s))
   *   - 503 `bot_not_running`      (bot not registered in the live registry)
   *   - 502 `send_failed`          (transport-level error after registry hit)
   *
   * Errors (.catch) here surface the raw axios message — the UI
   * pattern-matches against `error.response.data.error` to render
   * a friendly hint.
   */
  testBot(input: { bot_type: string; target_address: string }): Promise<{
    ok: boolean;
    bot_type: string;
    target: string;
    sentinel: string;
    sent_at: string;
  }> {
    return this.http
      .post<{
        ok: boolean;
        bot_type: string;
        target: string;
        sentinel: string;
        sent_at: string;
      }>('/api/v1/bots/test', input)
      .then((r) => r.data);
  }
}

export interface BotSubscription {
  id: string;
  user_id: string;
  bot_type: string;
  target_address: string;
  events: string[];
  enabled: boolean;
  created_at: string;
}

export interface BackupSettings {
  enabled: boolean;
  remote_url: string;
  has_auth: boolean;
  /**
   * Phase 32.7: cron-driven snapshot fire. Operators can edit
   * from /settings/backups; the server persists and hot-reloads
   * without a restart. The default ("0 3 * * *" = daily 03:00
   * UTC) is shown by the form when the persisted value is empty.
   */
  snapshot_cron: string;
  /** Phase 32.7: 0 = keep forever; otherwise days to retain. */
  snapshot_rotation_days: number;
  updated_at?: string;
  /**
   * Phase 28.1 polish.1: when the UI has overridden the in-memory
   * config, the running `*backup.Service` is still on the old URL
   * — the operator needs to restart for the new remote to apply.
   * The form shows a banner whenever this hint is non-empty.
   */
  source_hint?: string;
}

/**
 * Body shape for `setBackupSettings`. All fields are optional so
 * the operator can change one at a time without round-tripping the
 * current state; missing fields keep the persisted value. The
 * server validates snapshot_cron (5-field cron expression) and
 * rejects negative snapshot_rotation_days.
 */
export interface BackupSettingsInput {
  enabled?: boolean;
  remote_url?: string;
  remote_auth?: string;
  snapshot_cron?: string;
  snapshot_rotation_days?: number;
}

export interface BackupSnapshot {
  path: string;
  size: number;
  mod_time: string;
}

export interface BackupLogEntry {
  id: string;
  type: string;
  status: 'success' | 'failed';
  message: string;
  snapshot_path: string;
  created_at: string;
}

export interface Notification {
  id: string;
  user_id: string;
  type: string;
  target_type?: string;
  target_id?: string;
  payload: string; // JSON: { title, body, link, meta }
  read_at?: string;
  dedup_key: string;
  created_at: string;
}

export interface WikiPage {
  id: string;
  parent_id?: string;
  slug: string;
  title: string;
  content_md?: string;
  position: number;
  created_at: string;
  updated_at: string;
}

export interface WikiTreeNode {
  page: WikiPage;
  children?: WikiTreeNode[];
}

export interface SearchHit {
  type: 'page' | 'task' | 'comment';
  id: string;
  title?: string;
  snippet: string;
  score: number;
}

export interface CalendarEvent {
  id: string;
  title: string;
  description?: string;
  start_at: string;
  end_at: string;
  all_day: boolean;
  color?: string;
  project_id?: string;
  recurrence?: string;
  created_at: string;
  updated_at: string;
}

export interface TimeEntry {
  id: string;
  task_id: string;
  agent_id: string;
  started_at: string;
  ended_at?: string;
  duration_s?: number;
  source: 'timer' | 'manual';
}

export interface TimeReport {
  agent_id: string;
  from: string;
  to: string;
  tasks: { task_id: string; total_sec: number; title?: string }[];
  total_sec: number;
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

/** One row in the project Activity tab. Extends TaskActivity with
 *  the joined task title so the UI can render "X commented on Y"
 *  without a second round-trip per row. */
export interface ProjectActivityItem extends TaskActivity {
  task_title: string;
}

export const api = new ApiClient();

// Re-export the AxiosError type so feature modules don't need their own
// axios import just for error narrowing.
export { AxiosError };
