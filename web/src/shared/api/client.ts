import { AxiosError } from 'axios';

import { ApiClient } from './core';
import { agentsEndpoints, type AgentsApi } from './agents';
import { coursesEndpoints, type CoursesApi } from './courses';
import { projectsEndpoints, type ProjectsApi } from './projects';
import { systemEndpoints, type SystemApi } from './system';
import { tasksEndpoints, type TasksApi } from './tasks';
import { taskDetailsEndpoints, type TaskDetailsApi } from './taskDetails';
import { wikiEndpoints, type WikiApi } from './wiki';

/**
 * Static method surface of the assembled client. The domain groups
 * below are merged onto ApiClient.prototype at module load (see the
 * Object.assign at the bottom); this declaration teaches the
 * compiler the same shape so `api.<method>` stays fully typed.
 */
declare module './core' {
  // eslint-disable-next-line @typescript-eslint/no-empty-interface
  interface ApiClient
    extends TasksApi, TaskDetailsApi, ProjectsApi, AgentsApi, CoursesApi, WikiApi, SystemApi {}
}

// Re-export the domain types so existing
// `import { Task } from '@/shared/api/client'` call sites keep
// resolving during the transition period. Each re-export names its
// source explicitly (no `export *`) — knip tracks them, and the
// follow-up migration task retires them one by one.
export type {
  Agent,
  ChatEventBody,
  ChatMessage,
  OverviewResponse,
  StudyProposalView,
  TodayCourseView,
} from './agents';
export type { TodayReviewView } from './agents';
export type {
  Course,
  CourseLesson,
  CourseModule,
  CourseQuiz,
  CourseTree,
  TutorMessage,
} from './courses';
export type { BoardColumn, Column, Project, ProjectActivityItem, ProjectBoard } from './projects';
export type {
  BackupLogEntry,
  BackupSettings,
  BackupSnapshot,
  CalendarEvent,
  HealthResponse,
  InfoResponse,
  StatsResponse,
  UserProfile,
} from './system';
export type {
  BlockerRow,
  Checklist,
  ChecklistItem,
  ChildTaskProgress,
  Comment,
  ReviewQueueItem,
  Task,
  TaskActivity,
  TaskAttachment,
} from './taskDetails';
export type { Tag, TimeReport } from './tasks';
export type { Notification, SearchHit, WikiBlock, WikiPage, WikiTreeNode } from './wiki';

// Transitional re-export so feature modules don't need their own
// axios import just for error narrowing.
export { AxiosError };

// Domain endpoint groups are plain objects typed against ApiClient;
// merging them onto the prototype gives every instance the same
// methods the pre-split class declared inline, with bodies moved
// verbatim.
Object.assign(
  ApiClient.prototype,
  agentsEndpoints,
  coursesEndpoints,
  projectsEndpoints,
  systemEndpoints,
  taskDetailsEndpoints,
  tasksEndpoints,
  wikiEndpoints,
);

export const api = new ApiClient();
