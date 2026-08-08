import axios, { type AxiosInstance } from 'axios'

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
 * Typed wrapper around axios for the Orenda REST API.
 *
 * Phase 0 has only /healthz and /api/v1/info; richer endpoints land in
 * later phases.
 */
class ApiClient {
  private http: AxiosInstance

  constructor(baseURL: string = '') {
    this.http = axios.create({
      baseURL,
      withCredentials: true,
      timeout: 15_000,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  health(): Promise<HealthResponse> {
    return this.http.get<HealthResponse>('/healthz').then((r) => r.data)
  }

  info(): Promise<InfoResponse> {
    return this.http.get<InfoResponse>('/api/v1/info').then((r) => r.data)
  }
}

export const api = new ApiClient()