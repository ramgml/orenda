import type { ApiClient } from './core';
import type { Task } from './taskDetails';
import type { ReviewQueueItem, AgentStarvedItem } from './taskDetails';

/**
 * Agent entity (Phase 3 + Phase 28.19 labels).
 */
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

// StudyProposalView — Phase 31.9: the lightweight projection the
// Dashboard tray renders. The full Proposal entity (with body_md,
// accepted_task_id, resolved_at) stays in the agent namespace.
// Task 18: one due spaced-repetition row on the Today dashboard.
// overdue is informational (due_at before today's UTC midnight); the
// row stays in due_reviews and is never rendered red.
export interface TodayReviewView {
  id: string;
  lesson_id: string;
  lesson_title: string;
  course_id: string;
  course_title: string;
  step: number;
  due_at: string;
  overdue: boolean;
}

// Task 18: GET /api/v1/reviews/due row (the standalone due queue).
export interface ReviewView {
  id: string;
  lesson_id: string;
  lesson_title: string;
  course_id: string;
  course_title: string;
  step: number;
  due_at: string;
  last_result: 'pass' | 'fail' | null;
}

// Task 18: POST /api/v1/reviews/{id}/result response.
export interface ReviewResultResponse {
  id: string;
  lesson_id: string;
  step: number;
  due_at: string;
  last_result: 'pass' | 'fail';
  completed_at: string | null;
}

export interface StudyProposalView {
  id: string;
  course_id?: string;
  title: string;
  body_md?: string;
  target_date: string; // YYYY-MM-DD
  agent_id: string;
  created_at: string;
}

// TodayCourseView — Task 30: the lightweight per-course projection
// the Today page renders. Drift is computed server-side over the
// same 14-day window as the agent-side course pace, so the
// dashboard never flags a course the planner wouldn't. Only active
// courses of the session user appear.
export interface TodayCourseView {
  id: string;
  title: string;
  drift: 'ahead' | 'on_track' | 'behind';
}

// StudyProposalFull — returned by the accept/dismiss endpoints
// because the user tray may want to confirm the title / agent after
// the action. Phase 31.9.
interface StudyProposalFull {
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

// OverviewResponse — Task 107: wire shape of GET /api/v1/overview,
// the Dashboard screen's system-level readings.
export interface OverviewResponse {
  projects: number;
  tasks_by_status: Record<string, number>;
  wiki_pages: number;
  events: number;
  activity: { date: string; created: number; completed: number }[];
}

/**
 * Agent domain endpoints (`/api/v1/agents`, the agent-token
 * namespace `/api/v1/agent/*`, review, study proposals, overview).
 * Merged onto the ApiClient prototype in client.ts.
 */
export const agentsEndpoints = {
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
  },

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
  },

  deleteAgent(id: string): Promise<void> {
    return this.http.delete<void>(`/api/v1/agents/${id}`).then(() => undefined);
  },

  /**
   * Task 165: mint a new API token for an existing agent. The
   * previous plaintext stops working immediately; the new one is
   * returned exactly once (the server never persists it).
   */
  regenerateAgentToken(id: string): Promise<{ agent: Agent; plain_token: string }> {
    return this.http
      .post<{ agent: Agent; plain_token: string }>(`/api/v1/agents/${id}/regenerate-token`)
      .then((r) => r.data);
  },

  // ---- Agent-token namespace (/api/v1/agent/*) ----

  agentMe(): Promise<Agent> {
    return this.http.get<Agent>('/api/v1/agent/me').then((r) => r.data);
  },

  agentHeartbeat(): Promise<Agent> {
    return this.http.post<Agent>('/api/v1/agent/heartbeat', {}).then((r) => r.data);
  },

  agentClaimTask(taskId: string): Promise<Task> {
    return this.http.post<Task>(`/api/v1/agent/tasks/${taskId}/claim`, {}).then((r) => r.data);
  },

  agentSubmitTask(taskId: string, note: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/agent/tasks/${taskId}/submit`, { note })
      .then((r) => r.data);
  },

  agentReleaseTask(taskId: string): Promise<Task> {
    return this.http.post<Task>(`/api/v1/agent/tasks/${taskId}/release`, {}).then((r) => r.data);
  },

  // ---- Review (cookie-authenticated user side) ----

  reviewTask(taskId: string, decision: 'approve' | 'reject', comment?: string): Promise<Task> {
    return this.http
      .post<Task>(`/api/v1/tasks/${taskId}/review`, { decision, comment: comment ?? '' })
      .then((r) => r.data);
  },

  // ---- Review queue (Phase 19) ----
  //
  // One endpoint surfaces the union of "awaiting=human" and
  // "status=review" tasks, joined with their project name + colour.
  // The same handler powers the /review page and the sidebar badge.

  listReviewQueue(): Promise<{ tasks: ReviewQueueItem[]; count: number }> {
    return this.http
      .get<{ tasks: ReviewQueueItem[]; count: number }>(`/api/v1/review-queue`)
      .then((r) => r.data);
  },

  getReviewQueueCount(): Promise<{ count: number }> {
    return this.http.get<{ count: number }>(`/api/v1/review-queue/count`).then((r) => r.data);
  },

  // ---- Agent-starved queue (T336) ----
  //
  // awaiting='agent' work in projects no agent can reach (Task 140
  // scope filter). The kanban board surfaces a warning banner from
  // this; the task rows deep-link into the project settings where
  // the fix lives (open the project or grant the agent).

  listAgentStarved(): Promise<{ tasks: AgentStarvedItem[]; count: number }> {
    return this.http
      .get<{ tasks: AgentStarvedItem[]; count: number }>(`/api/v1/agent-starved`)
      .then((r) => r.data);
  },

  getAgentStarvedCount(): Promise<{ count: number }> {
    return this.http.get<{ count: number }>(`/api/v1/agent-starved/count`).then((r) => r.data);
  },

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
    // Task 30: active courses of the session user with the
    // server-computed drift marker. Empty array when none.
    courses: TodayCourseView[];
    // Task 18: due spaced-repetition reviews. Missed reviews stay in
    // the list with overdue=true — informational only (never red,
    // never merged into the overdue task list). Empty when none.
    due_reviews: TodayReviewView[];
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
        courses: TodayCourseView[];
        due_reviews: TodayReviewView[];
      }>(`/api/v1/today`)
      .then((r) => r.data);
  },

  // ---- Reviews (Task 18: spaced repetition) ----

  // The signed-in user's due review queue (due_at <= now,
  // completed_at IS NULL), with lesson/course titles joined.
  listDueReviews(): Promise<{ reviews: ReviewView[] }> {
    return this.http.get<{ reviews: ReviewView[] }>(`/api/v1/reviews/due`).then((r) => r.data);
  },

  // Record the aggregated repeat-session result. pass advances the
  // ladder; fail resets it. Never mutates lesson progress.
  postReviewResult(id: string, result: 'pass' | 'fail'): Promise<ReviewResultResponse> {
    return this.http
      .post<ReviewResultResponse>(`/api/v1/reviews/${id}/result`, { result })
      .then((r) => r.data);
  },

  // ---- Overview (Task 107) ----

  // System-level readings for the Dashboard screen: entity counts
  // plus a 30-day created/completed activity series. Distinct from
  // getToday, which is the personal daily slice.
  getOverview(): Promise<OverviewResponse> {
    return this.http.get<OverviewResponse>(`/api/v1/overview`).then((r) => r.data);
  },

  // ---- Study proposals (Phase 31.9) ----

  // Lightweight projection of a pending study proposal as rendered
  // on the Dashboard tray. The full Proposal entity (with body_md,
  // accepted_task_id, etc.) stays in the agent namespace.
  listStudyProposals(): Promise<{ proposals: StudyProposalView[] }> {
    return this.http
      .get<{ proposals: StudyProposalView[] }>(`/api/v1/study-proposals`)
      .then((r) => r.data);
  },

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
  },

  dismissStudyProposal(id: string): Promise<{ proposal: StudyProposalFull }> {
    return this.http
      .post<{ proposal: StudyProposalFull }>(`/api/v1/study-proposals/${id}/dismiss`)
      .then((r) => r.data);
  },
} satisfies ThisType<ApiClient>;

/** Method surface contributed by this domain to the ApiClient type. */
export type AgentsApi = typeof agentsEndpoints;
