import axios, { AxiosError, type AxiosInstance } from 'axios'

/**
 * Capabilities advertised by the server (mirrors Go's api.Capabilities).
 */
export interface Capabilities {
  auth: boolean
  rest_tasks: boolean
  websocket: boolean
  backup: boolean
  bots: boolean
  fts: boolean
  pwa: boolean
}

export interface InfoResponse {
  version: string
  name: string
  capabilities: Capabilities
}

export interface HealthResponse {
  status: string
  version: string
}

/**
 * Auth profile returned by GET /api/v1/me and embedded in LoginResponse.
 */
export interface UserProfile {
  user_id: string
  email: string
  display_name?: string
  role?: string
  scopes?: string[]
}

export interface LoginResponse extends UserProfile {
  token: string
}

export interface Project {
  id: string
  name: string
  color: string
  description?: string
  owner_id: string
  archived: boolean
  created_at: string
  updated_at: string
}

export interface BoardColumn {
  id: string
  board_id: string
  name: string
  position: number
  wip_limit?: number
  color?: string
}

export interface Board {
  id: string
  project_id: string
  name: string
  position: number
  created_at: string
}

export interface ProjectBoard {
  board: Board
  columns: BoardColumn[]
}

export interface Task {
  id: string
  project_id: string
  parent_task_id?: string
  column_id?: string
  title: string
  description?: string
  status: string
  priority: string
  assignee_type?: string
  assignee_id?: string
  awaiting: string
  context_md?: string
  agent_notes?: string
  due_at?: string
  started_at?: string
  claimed_at?: string
  completed_at?: string
  time_estimate_s?: number
  time_spent_s: number
  position: number
  created_at: string
  updated_at: string
}

export interface Agent {
  id: string
  name: string
  type: string
  description?: string
  token_id: string
  last_seen_at?: string
  status: 'online' | 'offline' | 'disabled'
  max_concurrent: number
  created_at: string
}

export interface Comment {
  id: string
  target_type: string
  target_id: string
  author_type: 'user' | 'agent'
  author_id: string
  body_md: string
  created_at: string
}

/**
 * Typed wrapper around axios for the Orenda REST API.
 *
 * Cookie-based auth is configured via `withCredentials: true`. A 401 response
 * triggers the onUnauthorized callback so the AuthProvider can drop state and
 * redirect to /login.
 */
class ApiClient {
  private http: AxiosInstance
  private onUnauthorized: (() => void) | null = null

  constructor(baseURL: string = '') {
    this.http = axios.create({
      baseURL,
      withCredentials: true,
      timeout: 15_000,
      headers: { 'Content-Type': 'application/json' },
    })
    this.http.interceptors.response.use(
      (r) => r,
      (err: unknown) => {
        if (axios.isAxiosError(err) && err.response?.status === 401) {
          this.onUnauthorized?.()
        }
        return Promise.reject(err)
      },
    )
  }

  /** Register a callback invoked on every 401 response. */
  onAuthFailure(cb: () => void): void {
    this.onUnauthorized = cb
  }

  health(): Promise<HealthResponse> {
    return this.http.get<HealthResponse>('/healthz').then((r) => r.data)
  }

  info(): Promise<InfoResponse> {
    return this.http.get<InfoResponse>('/api/v1/info').then((r) => r.data)
  }

  me(): Promise<UserProfile> {
    return this.http.get<UserProfile>('/api/v1/me').then((r) => r.data)
  }

  login(email: string, password: string): Promise<LoginResponse> {
    return this.http
      .post<LoginResponse>('/api/v1/auth/login', { email, password })
      .then((r) => r.data)
  }

  logout(): Promise<void> {
    return this.http.post<void>('/api/v1/auth/logout').then(() => undefined)
  }

  listProjects(): Promise<Project[]> {
    return this.http
      .get<{ projects: Project[] }>('/api/v1/projects')
      .then((r) => r.data.projects)
  }

  createProject(input: { name: string; color?: string; description?: string }): Promise<Project> {
    return this.http.post<Project>('/api/v1/projects', input).then((r) => r.data)
  }

  getBoard(projectId: string): Promise<ProjectBoard> {
    return this.http
      .get<ProjectBoard>(`/api/v1/projects/${projectId}/board`)
      .then((r) => r.data)
  }

  listProjectTasks(projectId: string, params?: { status?: string; column_id?: string }): Promise<Task[]> {
    return this.http
      .get<{ tasks: Task[] }>(`/api/v1/projects/${projectId}/tasks`, { params })
      .then((r) => r.data.tasks)
  }

  // ---- Task detail / Phase 3 ----

  getTask(taskId: string): Promise<Task> {
    return this.http.get<Task>(`/api/v1/tasks/${taskId}`).then((r) => r.data)
  }

  listTaskComments(taskId: string): Promise<{ comments: Comment[] }> {
    return this.http
      .get<{ comments: Comment[] }>(`/api/v1/tasks/${taskId}/comments`)
      .then((r) => r.data)
  }

  createTaskComment(taskId: string, body_md: string): Promise<Comment> {
    return this.http
      .post<Comment>(`/api/v1/tasks/${taskId}/comments`, { body_md })
      .then((r) => r.data)
  }

  createTask(projectId: string, input: { title: string; column_id?: string; description?: string }): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/projects/${projectId}/tasks`, input)
      .then((r) => r.data)
  }

  patchTask(taskId: string, input: Partial<Task>): Promise<Task> {
    return this.http.patch<Task>(`/api/v1/tasks/${taskId}`, input).then((r) => r.data)
  }

  deleteTask(taskId: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/tasks/${taskId}`).then(() => undefined)
  }

  /** Phase 2: relocate a task into a column (kanban move). */
  moveTask(taskId: string, columnId: string, position?: number): Promise<Task> {
    const body: Record<string, unknown> = { column_id: columnId }
    if (typeof position === 'number') body.position = position
    return this.http
      .post<Task>(`/api/v1/tasks/${taskId}/move`, body)
      .then((r) => r.data)
  }

  // ---- Agents (Phase 3) ----

  listAgents(): Promise<Agent[]> {
    return this.http
      .get<{ agents: Agent[] }>('/api/v1/agents')
      .then((r) => r.data.agents)
  }

  createAgent(input: { name: string; type?: string; description?: string }): Promise<{ agent: Agent; plain_token: string }> {
    return this.http
      .post<{ agent: Agent; plain_token: string }>('/api/v1/agents', input)
      .then((r) => r.data)
  }

  deleteAgent(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/agents/${id}`).then(() => undefined)
  }

  // ---- Agent-token namespace (/api/v1/agent/*) ----

  agentMe(): Promise<Agent> {
    return this.http.get<Agent>('/api/v1/agent/me').then((r) => r.data)
  }

  agentHeartbeat(): Promise<Agent> {
    return this.http.post<Agent>('/api/v1/agent/heartbeat', {}).then((r) => r.data)
  }

  agentClaimTask(taskId: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/agent/tasks/${taskId}/claim`, {})
      .then((r) => r.data)
  }

  agentSubmitTask(taskId: string, note: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/agent/tasks/${taskId}/submit`, { note })
      .then((r) => r.data)
  }

  agentReleaseTask(taskId: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/agent/tasks/${taskId}/release`, {})
      .then((r) => r.data)
  }

  // ---- Review (cookie-authenticated user side) ----

  reviewTask(taskId: string, decision: 'approve' | 'reject', comment?: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/tasks/${taskId}/review`, { decision, comment: comment ?? '' })
      .then((r) => r.data)
  }

  // ---- Events (Phase 4) ----

  listEvents(params: { from: string; to: string; project_id?: string }): Promise<CalendarEvent[]> {
    return this.http
      .get<{ events: CalendarEvent[] }>('/api/v1/events', { params })
      .then((r) => r.data.events)
  }

  createEvent(input: {
    title: string
    description?: string
    start_at: string
    end_at: string
    all_day?: boolean
    color?: string
    project_id?: string
    recurrence?: string
  }): Promise<CalendarEvent> {
    return this.http
      .post<CalendarEvent>('/api/v1/events', input)
      .then((r) => r.data)
  }

  patchEvent(id: string, input: Partial<{
    title: string
    description: string
    start_at: string
    end_at: string
    all_day: boolean
    color: string
    project_id: string
    recurrence: string
  }>): Promise<CalendarEvent> {
    return this.http.patch<CalendarEvent>(`/api/v1/events/${id}`, input).then((r) => r.data)
  }

  deleteEvent(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/events/${id}`).then(() => undefined)
  }

  // ---- Time tracking (Phase 4) ----

  startTimer(taskId: string): Promise<TimeEntry> {
    return this.http
      .post<TimeEntry>(`/api/v1/tasks/${taskId}/timer/start`, {})
      .then((r) => r.data)
  }

  stopTimer(taskId: string): Promise<TimeEntry> {
    return this.http
      .post<TimeEntry>(`/api/v1/tasks/${taskId}/timer/stop`, {})
      .then((r) => r.data)
  }

  addManualTime(taskId: string, input: { start_at: string; end_at: string }): Promise<TimeEntry> {
    return this.http
      .post<TimeEntry>(`/api/v1/tasks/${taskId}/time`, input)
      .then((r) => r.data)
  }

  getTimeReport(params: { agent_id?: string; from?: string; to?: string }): Promise<TimeReport> {
    return this.http
      .get<TimeReport>('/api/v1/reports/time', { params })
      .then((r) => r.data)
  }
}

export interface CalendarEvent {
  id: string
  title: string
  description?: string
  start_at: string
  end_at: string
  all_day: boolean
  color?: string
  project_id?: string
  recurrence?: string
  created_at: string
  updated_at: string
}

export interface TimeEntry {
  id: string
  task_id: string
  agent_id: string
  started_at: string
  ended_at?: string
  duration_s?: number
  source: 'timer' | 'manual'
}

export interface TimeReport {
  agent_id: string
  from: string
  to: string
  tasks: { task_id: string; total_sec: number; title?: string }[]
  total_sec: number
}

export const api = new ApiClient()

// Re-export the AxiosError type so feature modules don't need their own
// axios import just for error narrowing.
export { AxiosError }