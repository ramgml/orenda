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
import { request as plRequest, expect, type APIRequestContext } from '@playwright/test';

/** Base URL shared by every call in this suite — matches playwright.config.ts. */
export const BASE_URL = 'http://127.0.0.1:21371';

export const E2E_EMAIL = 'e2e@orenda.local';
export const E2E_PASSWORD = 'testpass123';
export const E2E_AGENT_NAME = 'e2e-agent';
export const E2E_PROJECT_NAME = 'E2E project';
export const E2E_PROJECT_COLOR = '#0ea5e9';

/**
 * Log in as the seeded e2e user and return a request context that
 * carries the session cookie. Subsequent calls (createProject,
 * createAgent, etc.) reuse this context.
 */
export async function loginAsUser(): Promise<APIRequestContext> {
  const ctx = await plRequest.newContext({ baseURL: BASE_URL });
  const resp = await ctx.post('/api/v1/auth/login', {
    data: { email: E2E_EMAIL, password: E2E_PASSWORD },
  });
  expect(resp.status(), `login: ${await resp.text()}`).toBe(200);
  return ctx;
}

/**
 * Build an API context carrying the agent Bearer token. Agent
 * endpoints under /api/v1/agent/* reject cookie-only sessions.
 */
export async function loginAsAgent(plainToken: string): Promise<APIRequestContext> {
  return plRequest.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Authorization: `Bearer ${plainToken}` },
  });
}

interface ProjectResp {
  id: string;
  name: string;
  color: string;
}

/**
 * Create a fresh project so the suite never depends on the order
 * in which specs run. Returns the project's id and (by re-reading)
 * its default board's columns.
 *
 * Seeds the project OPEN to agents: since Task 140 (migration 043)
 * fresh projects are CLOSED to agents (agents_allowed=false), so a
 * later agent claim on its tasks would fail with 422 not_in_scope.
 * The PATCH mirrors a real operator granting agents access.
 */
export async function createProject(
  ctx: APIRequestContext,
  name: string = `${E2E_PROJECT_NAME} ${Date.now()}`,
): Promise<ProjectResp> {
  const resp = await ctx.post('/api/v1/projects', {
    data: { name, color: E2E_PROJECT_COLOR },
  });
  expect(resp.status(), `createProject: ${await resp.text()}`).toBe(201);
  const project = await resp.json();
  // Open the project to agents — migration 043 default is CLOSED.
  const opened = await ctx.patch(`/api/v1/projects/${project.id}`, {
    data: { agents_allowed: true },
  });
  expect(opened.status(), `createProject: ${await opened.text()}`).toBe(200);
  return project;
}

/** Fetch the default board's columns for the given project. */
export async function listColumns(
  ctx: APIRequestContext,
  projectId: string,
): Promise<Array<{ id: string; name: string; position: number }>> {
  const resp = await ctx.get(`/api/v1/projects/${projectId}/board`);
  expect(resp.status(), `listBoard: ${await resp.text()}`).toBe(200);
  const body = (await resp.json()) as {
    columns: Array<{ id: string; name: string; position: number }>;
  };
  return body.columns;
}

interface TaskResp {
  id: string;
  title: string;
  status: string;
  priority: string;
  awaiting: string;
  column_id: string | null;
  project_id: string;
  due_at: string | null;
  created_at: string;
  updated_at: string;
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
  const resp = await ctx.post(`/api/v1/projects/${projectId}/tasks`, { data: input });
  expect(resp.status(), `createTask: ${await resp.text()}`).toBe(201);
  return resp.json();
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
  const resp = await ctx.patch(`/api/v1/tasks/${taskId}`, { data: input });
  expect(resp.status(), `patchTask: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Phase 27.7: read the task activity feed via REST. */
export async function listTaskActivity(
  ctx: APIRequestContext,
  taskId: string,
): Promise<{ activity: { action: string; payload?: string }[] }> {
  const resp = await ctx.get(`/api/v1/tasks/${taskId}/activity`);
  expect(resp.status(), `listTaskActivity: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Phase 27.7: list agents so the Assignee dropdown can be exercised. */
export async function listAgents(
  ctx: APIRequestContext,
): Promise<{ agents: { id: string; name: string }[] }> {
  const resp = await ctx.get('/api/v1/agents');
  expect(resp.status(), `listAgents: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Tag payload as the API returns it (id + name + optional color). */
export interface TagResp {
  id: string;
  name: string;
  color?: string;
}

/** Create a tag with a hex colour (Phase 13 endpoint, used by E2E setup). */
export async function createTag(
  ctx: APIRequestContext,
  input: { name: string; color: string },
): Promise<TagResp> {
  const resp = await ctx.post('/api/v1/tags', { data: input });
  expect(resp.status(), `createTag: ${await resp.text()}`).toBeLessThan(400);
  return resp.json();
}

// ---------------------------------------------------------------------------
// Course fixtures (Phase 27.4 E2E).
// ---------------------------------------------------------------------------

export interface CourseResp {
  id: string;
  title: string;
  intent_md: string;
  level: string;
  pace: string;
  status: string;
  owner_id: string;
  generator_task_id: string;
  created_at: string;
  updated_at: string;
}

/** Create a draft course on the user side. */
export async function createCourse(
  ctx: APIRequestContext,
  input: { title: string; intent_md?: string; skip_generator?: boolean },
): Promise<CourseResp> {
  const resp = await ctx.post('/api/v1/courses', { data: input });
  expect(resp.status(), `createCourse: ${await resp.text()}`).toBe(201);
  return resp.json();
}

/** Approve a course submitted by the tutor (review → active). */
export async function approveCourse(ctx: APIRequestContext, courseId: string): Promise<CourseResp> {
  const resp = await ctx.post(`/api/v1/courses/${courseId}/approve`, {});
  expect(resp.status(), `approveCourse: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Submit a draft curriculum (agent-side). The course is the tutor's
 * job, so the caller must be an agent with a Bearer token. Use
 * `loginAsAgent(plainToken)` to mint the context.
 */
export async function submitCurriculum(
  ctx: APIRequestContext,
  courseId: string,
  modules: { title: string; position: number; lessons: { title: string; position: number }[] }[],
): Promise<void> {
  const resp = await ctx.put(`/api/v1/agent/courses/${courseId}/curriculum`, {
    data: {
      modules: modules.map((m) => ({
        title: m.title,
        position: m.position,
        lessons: m.lessons.map((l) => ({
          title: l.title,
          position: l.position,
        })),
      })),
    },
  });
  expect(resp.status(), `submitCurriculum: ${await resp.text()}`).toBe(200);
}

/**
 * submitCurriculumAsOwner — Phase 27.6 owner-side swap. Same body
 * shape as the agent endpoint, plus quizzes per lesson and
 * description per module. Returns when the server reports review.
 */
export async function submitCurriculumAsOwner(
  ctx: APIRequestContext,
  courseId: string,
  modules: {
    title: string;
    description?: string;
    position: number;
    lessons: {
      title: string;
      position: number;
      content_md?: string;
      quizzes?: {
        position: number;
        question_md: string;
        expected_md?: string;
        kind: 'exact' | 'open';
      }[];
    }[];
  }[],
): Promise<void> {
  const resp = await ctx.put(`/api/v1/courses/${courseId}/curriculum`, {
    data: {
      modules: modules.map((m) => ({
        title: m.title,
        description: m.description ?? '',
        position: m.position,
        lessons: m.lessons.map((l) => ({
          title: l.title,
          position: l.position,
          content_md: l.content_md ?? '',
          quizzes: (l.quizzes ?? []).map((q) => ({
            position: q.position,
            question_md: q.question_md,
            expected_md: q.expected_md ?? '',
            kind: q.kind,
          })),
        })),
      })),
    },
  });
  expect(resp.status(), `submitCurriculumAsOwner: ${await resp.text()}`).toBe(200);
}

/** addQuizAsOwner — append a single quiz to a lesson (Phase 27.6). */
export async function addQuizAsOwner(
  ctx: APIRequestContext,
  lessonId: string,
  q: { question_md: string; expected_md?: string; kind: 'exact' | 'open' },
): Promise<{ id: string; position: number }> {
  const resp = await ctx.post(`/api/v1/lessons/${lessonId}/quizzes`, {
    data: {
      question_md: q.question_md,
      expected_md: q.expected_md ?? '',
      kind: q.kind,
    },
  });
  expect(resp.status(), `addQuizAsOwner: ${await resp.text()}`).toBe(201);
  return resp.json();
}

/** Fetch the full course tree (course + modules + lessons + quizzes). */
export async function getCourseTree(
  ctx: APIRequestContext,
  courseId: string,
): Promise<{
  course: CourseResp;
  modules: { id: string; course_id: string; title: string; position: number }[];
  lessons: {
    id: string;
    module_id: string;
    title: string;
    position: number;
    status: string;
    content_md: string;
    task_id: string;
  }[];
  quizzes: {
    id: string;
    lesson_id: string;
    question_md: string;
    expected_md: string;
    kind: string;
    position: number;
  }[];
  progress: { lessons_total: number; lessons_done: number };
}> {
  const resp = await ctx.get(`/api/v1/courses/${courseId}`);
  expect(resp.status(), `getCourseTree: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Tutor-side: write content_md + flip a lesson from locked → open.
 * Bearer-auth (agent) context required.
 */
export async function materializeLesson(
  ctx: APIRequestContext,
  lessonId: string,
  contentMd: string,
): Promise<void> {
  const resp = await ctx.post(`/api/v1/agent/lessons/${lessonId}/materialize`, {
    data: { content_md: contentMd },
  });
  expect(resp.status(), `materializeLesson: ${await resp.text()}`).toBe(200);
}

/** Student-side: submit a quiz answer. */
export async function answerQuiz(
  ctx: APIRequestContext,
  lessonId: string,
  quizId: string,
  answer: string,
): Promise<{ correct: boolean; feedback_md?: string; review_task_id?: string }> {
  const resp = await ctx.post(`/api/v1/lessons/${lessonId}/quizzes/${quizId}/answer`, {
    data: { answer },
  });
  expect(resp.status(), `answerQuiz: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Student-side: mark a lesson done (unlocks the next). */
export async function completeLesson(ctx: APIRequestContext, lessonId: string): Promise<void> {
  const resp = await ctx.post(`/api/v1/lessons/${lessonId}/complete`, {});
  expect(resp.status(), `completeLesson: ${await resp.text()}`).toBe(200);
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
  });
  expect(resp.status(), `setTaskTags: ${await resp.text()}`).toBe(200);
}

/**
 * Create a task in the Inbox (project_id IS NULL). The endpoint
 * accepts title + a few optional fields.
 */
export async function createInboxTask(
  ctx: APIRequestContext,
  input: { title: string; description?: string },
): Promise<TaskResp> {
  const resp = await ctx.post('/api/v1/inbox/tasks', { data: input });
  expect(resp.status(), `createInboxTask: ${await resp.text()}`).toBe(201);
  return resp.json();
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
    data: { name, type: ['qwen'], description: 'E2E test agent' },
  });
  expect(resp.status(), `createAgent: ${await resp.text()}`).toBe(201);
  return resp.json();
}

/** Claim a task as the given agent. */
export async function claimTask(agentCtx: APIRequestContext, taskId: string): Promise<TaskResp> {
  const resp = await agentCtx.post(`/api/v1/agent/tasks/${taskId}/claim`, { data: {} });
  expect(resp.status(), `claimTask: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Submit a claimed task for review. */
export async function submitTask(
  agentCtx: APIRequestContext,
  taskId: string,
  note: string,
): Promise<TaskResp> {
  const resp = await agentCtx.post(`/api/v1/agent/tasks/${taskId}/submit`, { data: { note } });
  expect(resp.status(), `submitTask: ${await resp.text()}`).toBe(200);
  return resp.json();
}

/** Approve or reject a task from the user side. */
export async function reviewTask(
  userCtx: APIRequestContext,
  taskId: string,
  decision: 'approve' | 'reject',
  comment = '',
): Promise<void> {
  const resp = await userCtx.post(`/api/v1/tasks/${taskId}/review`, {
    data: { decision, comment },
  });
  expect(resp.status(), `reviewTask: ${await resp.text()}`).toBe(200);
}
