// @ts-check
/**
 * Shared E2E helpers.
 *
 * The auth smoke spec already proved the basic harness works
 * (login cookie, /healthz, etc.). For richer flows (today sections,
 * kanban dnd, review queue) we need to seed additional fixtures —
 * projects, agents with bearer tokens, tasks in specific states —
 * via the public REST API rather than touching the database directly.
 *
 * Why API and not SQL:
 *   - Avoids coupling tests to schema details that change between
 *     migrations.
 *   - Mirrors what a real operator would do; if the seed breaks,
 *     there's no mystery about how to reproduce it.
 *
 * Auth model:
 *   - User-side calls (create project, accept review, etc.) ride
 *     a cookie session set by POST /auth/login. We use Playwright's
 *     `request.newContext()` and store the resulting storage state
 *     so the browser context inherits it.
 *   - Agent-side calls (claim, submit) ride an opaque Bearer token
 *     issued by POST /agents. We pass it via the Authorization
 *     header on a fresh context.
 */
import { request as plRequest, expect, type APIRequestContext } from '@playwright/test'

/** Base URL shared by every call in this suite — matches playwright.config.ts. */
export const BASE_URL = 'http://127.0.0.1:21371'

export const E2E_EMAIL = 'e2e@orenda.local'
export const E2E_PASSWORD = 'testpass123'
export const E2E_AGENT_NAME = 'e2e-agent'
export const E2E_PROJECT_NAME = 'E2E project'
export const E2E_PROJECT_COLOR = '#0ea5e9'

/**
 * Log in as the seeded e2e user and return a request context that
 * carries the session cookie. Subsequent calls (createProject,
 * createAgent, etc.) reuse this context.
 */
export async function loginAsUser(): Promise<APIRequestContext> {
  const ctx = await plRequest.newContext({ baseURL: BASE_URL })
  const resp = await ctx.post('/api/v1/auth/login', {
    data: { email: E2E_EMAIL, password: E2E_PASSWORD },
  })
  expect(resp.status(), `login: ${await resp.text()}`).toBe(200)
  return ctx
}

/**
 * Build an API context carrying the agent Bearer token. Agent
 * endpoints under /api/v1/agent/* reject cookie-only sessions.
 */
export async function loginAsAgent(plainToken: string): Promise<APIRequestContext> {
  return plRequest.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Authorization: `Bearer ${plainToken}` },
  })
}

interface ProjectResp {
  id: string
  name: string
  color: string
}

/**
 * Create a fresh project so the suite never depends on the order
 * in which specs run. Returns the project's id and (by re-reading)
 * its default board's columns.
 */
export async function createProject(
  ctx: APIRequestContext,
  name: string = `${E2E_PROJECT_NAME} ${Date.now()}`,
): Promise<ProjectResp> {
  const resp = await ctx.post('/api/v1/projects', {
    data: { name, color: E2E_PROJECT_COLOR },
  })
  expect(resp.status(), `createProject: ${await resp.text()}`).toBe(201)
  return resp.json()
}

/** Fetch the default board's columns for the given project. */
export async function listColumns(
  ctx: APIRequestContext,
  projectId: string,
): Promise<Array<{ id: string; name: string; position: number }>> {
  const resp = await ctx.get(`/api/v1/projects/${projectId}/board`)
  expect(resp.status(), `listBoard: ${await resp.text()}`).toBe(200)
  const body = (await resp.json()) as { columns: Array<{ id: string; name: string; position: number }> }
  return body.columns
}

interface TaskResp {
  id: string
  title: string
  status: string
  priority: string
  awaiting: string
  column_id: string | null
  project_id: string
  due_at: string | null
  created_at: string
  updated_at: string
}

/**
 * Create a task with the given overrides. Pass project_id to file
 * it on a project; omit it to file in the Inbox (project_id IS NULL).
 */
/**
 * Create a task on a project. The endpoint only takes {title,
 * column_id?, description?} — status, priority, due_at must be
 * applied afterwards via PATCH.
 */
export async function createTask(
  ctx: APIRequestContext,
  projectId: string,
  input: { title: string; column_id?: string; description?: string },
): Promise<TaskResp> {
  const resp = await ctx.post(`/api/v1/projects/${projectId}/tasks`, { data: input })
  expect(resp.status(), `createTask: ${await resp.text()}`).toBe(201)
  return resp.json()
}

/**
 * Patch a task with any subset of mutable fields. Used after
 * createTask to set due_at, priority, etc.
 */
export async function patchTask(
  ctx: APIRequestContext,
  taskId: string,
  input: Partial<TaskResp>,
): Promise<TaskResp> {
  const resp = await ctx.patch(`/api/v1/tasks/${taskId}`, { data: input })
  expect(resp.status(), `patchTask: ${await resp.text()}`).toBe(200)
  return resp.json()
}

/** Tag payload as the API returns it (id + name + optional color). */
export interface TagResp {
  id: string
  name: string
  color?: string
}

/** Create a tag with a hex colour (Phase 13 endpoint, used by E2E setup). */
export async function createTag(
  ctx: APIRequestContext,
  input: { name: string; color: string },
): Promise<TagResp> {
  const resp = await ctx.post('/api/v1/tags', { data: input })
  expect(resp.status(), `createTag: ${await resp.text()}`).toBeLessThan(400)
  return resp.json()
}

/**
 * Attach a list of tags to a task (replace semantics, Phase 13).
 * The endpoint accepts tag_ids; ordering follows the API contract.
 */
export async function setTaskTags(
  ctx: APIRequestContext,
  taskId: string,
  tagIds: string[],
): Promise<void> {
  const resp = await ctx.put(`/api/v1/tasks/${taskId}/tags`, {
    data: { tag_ids: tagIds },
  })
  expect(resp.status(), `setTaskTags: ${await resp.text()}`).toBe(200)
}

/**
 * Create a task in the Inbox (project_id IS NULL). The endpoint
 * accepts title + a few optional fields.
 */
export async function createInboxTask(
  ctx: APIRequestContext,
  input: { title: string; description?: string },
): Promise<TaskResp> {
  const resp = await ctx.post('/api/v1/inbox/tasks', { data: input })
  expect(resp.status(), `createInboxTask: ${await resp.text()}`).toBe(201)
  return resp.json()
}

/**
 * Create an agent and return both the agent record (id, name) and
 * the plaintext API token (only shown once at creation time).
 */
export async function createAgent(
  ctx: APIRequestContext,
  name: string = `${E2E_AGENT_NAME} ${Date.now()}`,
): Promise<{ agent: { id: string; name: string }; plain_token: string }> {
  const resp = await ctx.post('/api/v1/agents', {
    data: { name, type: 'qwen', description: 'E2E test agent' },
  })
  expect(resp.status(), `createAgent: ${await resp.text()}`).toBe(201)
  return resp.json()
}

/** Claim a task as the given agent. */
export async function claimTask(
  agentCtx: APIRequestContext,
  taskId: string,
): Promise<TaskResp> {
  const resp = await agentCtx.post(`/api/v1/agent/tasks/${taskId}/claim`, { data: {} })
  expect(resp.status(), `claimTask: ${await resp.text()}`).toBe(200)
  return resp.json()
}

/** Submit a claimed task for review. */
export async function submitTask(
  agentCtx: APIRequestContext,
  taskId: string,
  note: string,
): Promise<TaskResp> {
  const resp = await agentCtx.post(`/api/v1/agent/tasks/${taskId}/submit`, { data: { note } })
  expect(resp.status(), `submitTask: ${await resp.text()}`).toBe(200)
  return resp.json()
}

/** Approve or reject a task from the user side. */
export async function reviewTask(
  userCtx: APIRequestContext,
  taskId: string,
  decision: 'approve' | 'reject',
  comment = '',
): Promise<void> {
  const resp = await userCtx.post(`/api/v1/tasks/${taskId}/review`, { data: { decision, comment } })
  expect(resp.status(), `reviewTask: ${await resp.text()}`).toBe(200)
}