import { useState } from 'react';
import { Link, useParams } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { api } from '@/shared/api/client';
import { useWebSocketTopic } from '@/shared/ws';

import { Button } from '@/shared/ui/button';
import {
  CourseCurriculumEditor,
  type EditorModule,
  type EditorQuiz,
} from './CourseCurriculumEditor';
import { CourseNumberChip } from './CourseNumberChip';

/**
 * Phase 18.7: Course tree view.
 *
 * Renders the full course = modules + lessons + progress. The
 * lifecycle state machine is small:
 *   - draft:     tutor hasn't submitted yet
 *   - review:    tutor submitted; owner can approve / request changes
 *   - active:    approved, lessons open sequentially
 *   - done:      all lessons completed
 *
 * Phase 27.6: in draft/review the owner can switch into the inline
 * editor and rebuild the program themselves. In active, structural
 * edits are disabled — the owner edits a single lesson's content
 * Phase 30.13: the editor is also available in active, but saving
 * goes through the granular endpoints (create/rename/delete +
 * IDs-only reorder) instead of the destructive swap, so lesson
 * status/progress and task links survive the edit.
 *
 * Task 268: the tree is a TanStack Query (`['courses', id]`).
 * The backend publishes no `courses` WS topic; curriculum changes
 * from the generator agent arrive over the `tasks` topic, so that's
 * what invalidates the cache. Owner-side mutations invalidate the
 * same key in onSettled.
 */
const courseQueryKey = (id: string) => ['courses', id] as const;

export function CourseDetailPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);

  const courseQ = useQuery({
    queryKey: courseQueryKey(id ?? ''),
    queryFn: () => api.getCourse(id as string),
    enabled: !!id,
  });

  // Re-fetch on task events (the agent may submit a curriculum while
  // we're staring at the page). Invalidate the whole `courses` shape
  // so CoursesPage and LessonPage pick up lifecycle changes too.
  useWebSocketTopic('tasks', () => {
    void queryClient.invalidateQueries({ queryKey: ['courses'] });
  });

  const approveQ = useMutation({
    mutationFn: () => api.approveCourse(id as string),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['courses'] }),
  });
  const requestChangesQ = useMutation({
    mutationFn: () => api.requestCourseChanges(id as string),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['courses'] }),
  });

  if (courseQ.isLoading) {
    return <p className="p-6 text-sm text-slate-400 italic">Loading…</p>;
  }
  if (courseQ.error) {
    return (
      <div className="p-6 space-y-2">
        <p className="text-sm text-red-600">
          {courseQ.error instanceof Error ? courseQ.error.message : String(courseQ.error)}
        </p>
        <Button type="button" variant="outline" size="sm" onClick={() => void courseQ.refetch()}>
          Retry
        </Button>
      </div>
    );
  }
  if (!courseQ.data) return <></>;
  const data = courseQ.data;

  const busy = approveQ.isPending || requestChangesQ.isPending;
  const { course, modules, lessons, quizzes, progress } = data;

  // Bucket lessons and quizzes by module so the editor can hydrate
  // from the tree shape without losing quizzes.
  const lessonsByModule = new Map<string, typeof lessons>();
  const quizzesByLesson = new Map<string, NonNullable<typeof quizzes>>();
  for (const l of lessons) {
    const arr = lessonsByModule.get(l.module_id) ?? [];
    arr.push(l);
    lessonsByModule.set(l.module_id, arr);
  }
  for (const arr of lessonsByModule.values()) {
    arr.sort((a, b) => a.position - b.position);
  }
  if (quizzes) {
    for (const q of quizzes) {
      const arr = quizzesByLesson.get(q.lesson_id) ?? [];
      arr.push(q);
      quizzesByLesson.set(q.lesson_id, arr);
    }
  }

  const initialModules: EditorModule[] = modules.map((m, mi) => ({
    id: m.id,
    title: m.title,
    description: m.description ?? '',
    position: mi,
    lessons: (lessonsByModule.get(m.id) ?? []).map((l, li) => {
      const qs = (quizzesByLesson.get(l.id) ?? []).slice().sort((a, b) => a.position - b.position);
      return {
        id: l.id,
        title: l.title,
        position: li,
        content_md: l.content_md ?? '',
        quizzes: qs.map<EditorQuiz>((q, qi) => ({
          id: q.id,
          position: qi,
          question_md: q.question_md,
          expected_md: q.expected_md ?? '',
          kind: q.kind,
        })),
      };
    }),
  }));

  // Phase 30.13: active courses edit granularly (progress-safe);
  // draft/review keep the atomic swap; done/archived are frozen.
  const editable =
    course.status === 'draft' || course.status === 'review' || course.status === 'active';

  return (
    <section className="p-6 max-w-3xl mx-auto space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">
          {course.title}
          <CourseNumberChip number={course.number} />
        </h1>
        <p className="text-sm text-slate-500 mt-1">
          Status: <span className="font-mono">{course.status}</span> · Level:{' '}
          <span className="font-mono">{course.level}</span> · Pace:{' '}
          <span className="font-mono">{course.pace}</span>
        </p>
        {course.intent_md && (
          <p className="text-sm text-muted-foreground mt-3 italic">"{course.intent_md}"</p>
        )}
      </header>

      {/* Phase 31.9: pace notes — read-only display; the agent-side
          planner writes these (PATCH /api/v1/agent/courses/{id}) and
          they drive study-proposal generation. The user can read
          them here so they know what the planner thinks the cadence
          should be. Editing from this UI is deferred — see PLAN 31.9
          "за скобкой" notes. */}
      {course.pace_notes_md && (
        <details
          data-testid="course-pace-notes"
          className="rounded border border-border p-3 text-sm bg-background"
        >
          <summary className="cursor-pointer text-foreground font-medium">
            Pace notes (from the planner)
          </summary>
          <pre className="mt-2 whitespace-pre-wrap text-xs text-muted-foreground font-sans">
            {course.pace_notes_md}
          </pre>
        </details>
      )}

      {/* Lifecycle actions */}
      {course.status === 'review' && (
        <div className="flex gap-2">
          <Button
            type="button"
            onClick={() => approveQ.mutate()}
            disabled={busy}
            data-testid="course-approve"
            size="sm"
            className="bg-emerald-600 hover:bg-emerald-700"
          >
            Approve curriculum
          </Button>
          <Button
            type="button"
            onClick={() => requestChangesQ.mutate()}
            disabled={busy}
            variant="outline"
            size="sm"
            className="border-amber-300 text-amber-700 hover:bg-amber-50"
          >
            Request changes
          </Button>
        </div>
      )}

      {/* Progress bar */}
      <div data-testid="course-progress">
        <div className="flex justify-between mb-1">
          <span className="text-sm text-slate-600">Progress</span>
          <span className="text-sm text-slate-600 font-mono">
            {progress.lessons_done} / {progress.lessons_total}
          </span>
        </div>
        <div className="h-2 rounded bg-slate-200 dark:bg-slate-700 overflow-hidden">
          <div
            className="h-full bg-emerald-500"
            style={{
              width:
                progress.lessons_total > 0
                  ? `${(progress.lessons_done / progress.lessons_total) * 100}%`
                  : '0%',
            }}
          />
        </div>
      </div>

      {editable && (
        <div className="flex gap-2 items-center">
          <Button
            type="button"
            onClick={() => setEditing((v) => !v)}
            data-testid="course-edit-toggle"
            variant="outline"
            size="sm"
          >
            {editing ? 'Done editing' : 'Edit curriculum'}
          </Button>
          <span className="text-xs text-slate-500">
            {course.status === 'active'
              ? 'Granular edits — student progress is preserved.'
              : 'Modules, lessons, quizzes — atomic swap.'}
          </span>
        </div>
      )}

      {editing ? (
        <CourseCurriculumEditor
          course={course}
          initialModules={initialModules}
          onCancel={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            void queryClient.invalidateQueries({ queryKey: ['courses'] });
          }}
        />
      ) : modules.length === 0 ? (
        <p className="text-sm text-slate-400 italic">
          {editable
            ? 'No modules yet. Click "Edit curriculum" to add the first one.'
            : 'No modules yet.'}
        </p>
      ) : (
        <ul className="space-y-4">
          {modules.map((m) => {
            const ls = lessonsByModule.get(m.id) ?? [];
            return (
              <li key={m.id} className="rounded border border-border p-3 bg-background">
                <h3 className="text-sm font-semibold text-foreground">{m.title}</h3>
                {m.description && <p className="text-xs text-slate-500 mt-1">{m.description}</p>}
                <ul className="mt-2 space-y-1">
                  {ls.map((l) => {
                    const lessonQuizzes = quizzesByLesson.get(l.id) ?? [];
                    return (
                      <li
                        key={l.id}
                        data-testid="lesson-row"
                        className="flex justify-between items-center text-sm px-2 py-1 rounded hover:bg-slate-50 dark:hover:bg-slate-900"
                      >
                        <Link
                          to={`/lessons/${l.id}`}
                          className={
                            l.status === 'locked'
                              ? 'text-slate-400'
                              : 'text-foreground hover:underline'
                          }
                        >
                          {l.status === 'locked' && '🔒 '}
                          {l.title}
                          {lessonQuizzes.length > 0 && (
                            <span className="ml-2 text-[10px] text-slate-400">
                              {lessonQuizzes.length} q
                            </span>
                          )}
                        </Link>
                        <span className="text-[10px] text-slate-400 font-mono">{l.status}</span>
                      </li>
                    );
                  })}
                </ul>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
