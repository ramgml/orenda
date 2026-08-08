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
}

export const api = new ApiClient()

// Re-export the AxiosError type so feature modules don't need their own
// axios import just for error narrowing.
export { AxiosError }