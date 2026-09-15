import type { ApiClient } from './core';

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

/**
 * Course domain endpoints (`/api/v1/courses`, lessons, quizzes).
 * Merged onto the ApiClient prototype in client.ts.
 */
export const coursesEndpoints = {
  listCourses(): Promise<{ courses: Course[]; count: number }> {
    return this.http
      .get<{ courses: Course[]; count: number }>('/api/v1/courses')
      .then((r) => r.data);
  },

  createCourse(input: {
    title: string;
    intent_md?: string;
    // Phase 27.6: when true, the owner intends to build the
    // curriculum themselves; the server skips the agent generator
    // task so a sleeping tutor can't overwrite manual work.
    skip_generator?: boolean;
  }): Promise<Course> {
    return this.http.post<Course>('/api/v1/courses', input).then((r) => r.data);
  },

  getCourse(id: string): Promise<CourseTree> {
    return this.http.get<CourseTree>(`/api/v1/courses/${id}`).then((r) => r.data);
  },

  approveCourse(id: string): Promise<Course> {
    return this.http.post<Course>(`/api/v1/courses/${id}/approve`, {}).then((r) => r.data);
  },

  requestCourseChanges(id: string): Promise<Course> {
    return this.http.post<Course>(`/api/v1/courses/${id}/request-changes`, {}).then((r) => r.data);
  },

  completeLesson(id: string): Promise<unknown> {
    return this.http.post<unknown>(`/api/v1/lessons/${id}/complete`, {}).then((r) => r.data);
  },

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
  },

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
  },

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
  },

  updateLessonContent(
    lessonId: string,
    input: { content_md: string; task_id?: string },
  ): Promise<unknown> {
    return this.http.put(`/api/v1/lessons/${lessonId}/content`, input).then((r) => r.data);
  },

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
  },

  updateModule(id: string, input: { title: string; description?: string }): Promise<CourseModule> {
    return this.http.patch<CourseModule>(`/api/v1/modules/${id}`, input).then((r) => r.data);
  },

  deleteModule(id: string): Promise<void> {
    return this.http.delete(`/api/v1/modules/${id}`).then(() => undefined);
  },

  createModuleLesson(moduleId: string, input: { title: string }): Promise<CourseLesson> {
    return this.http
      .post<CourseLesson>(`/api/v1/modules/${moduleId}/lessons`, input)
      .then((r) => r.data);
  },

  renameLesson(id: string, title: string): Promise<CourseLesson> {
    return this.http.patch<CourseLesson>(`/api/v1/lessons/${id}`, { title }).then((r) => r.data);
  },

  deleteLesson(id: string): Promise<void> {
    return this.http.delete(`/api/v1/lessons/${id}`).then(() => undefined);
  },

  updateQuiz(
    qid: string,
    input: { question_md: string; expected_md?: string; kind?: 'exact' | 'open' },
  ): Promise<CourseQuiz> {
    return this.http.patch<CourseQuiz>(`/api/v1/quizzes/${qid}`, input).then((r) => r.data);
  },

  deleteQuiz(qid: string): Promise<void> {
    return this.http.delete(`/api/v1/quizzes/${qid}`).then(() => undefined);
  },

  applyCourseStructure(
    courseId: string,
    modules: { module_id: string; lesson_ids: string[] }[],
  ): Promise<CourseTree> {
    return this.http
      .put<CourseTree>(`/api/v1/courses/${courseId}/structure`, { modules })
      .then((r) => r.data);
  },
} satisfies ThisType<ApiClient>;

/** Method surface contributed by this domain to the ApiClient type. */
export type CoursesApi = typeof coursesEndpoints;
