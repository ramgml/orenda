import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

import { api } from '@/shared/api/client'

/**
 * /lessons/:id — single lesson view (Phase 27.4).
 *
 * The route lives inside an optional modal flow (the kanban TaskModal
 * uses a parallel absolute path); the URL works either way. The page
 * shows the lesson content, quizzes, and a "complete" button.
 *
 * Three lesson states:
 *   - locked:    the tutor hasn't materialised the lesson yet.
 *                Show a placeholder; quizzes are hidden.
 *   - open:      the lesson is ready. Render content + quizzes, allow
 *                answering. The "complete" button is enabled only
 *                when all quizzes have been answered (so the student
 *                has actually engaged with the material).
 *   - done:      show the content read-only with a "Done" badge;
 *                the complete button is gone (Phase 18: completion
 *                is one-way).
 *
 * Quiz rendering accepts two kinds:
 *   - exact:    the backend normalises and compares server-side; we
 *               render the verdict inline (`Correct` / `Try again`).
 *   - open:     we POST the answer and the server returns a
 *               `review_task_id`; the UI shows "Pending review" and
 *               the lesson complete button stays enabled.
 */
export function LessonPage(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [data, setData] = useState<LessonLoad | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [completing, setCompleting] = useState(false)
  // Per-quiz answer state — keyed by quiz id so the student can
  // fill out multiple questions without losing their input.
  const [answers, setAnswers] = useState<Record<string, string>>({})
  // Per-quiz result state — backend verdict after submit.
  const [results, setResults] = useState<
    Record<string, { correct: boolean; feedback_md?: string; review_task_id?: string }>
  >({})

  const load = useCallback(async () => {
    if (!id) return
    try {
      const r = await loadLesson(id)
      setData(r)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [id])

  useEffect(() => {
    void load()
  }, [load])

  async function onAnswer(quizId: string): Promise<void> {
    if (!id || submitting) return
    const answer = answers[quizId] ?? ''
    setSubmitting(true)
    try {
      const r = await api.answerQuiz(id, quizId, answer)
      setResults((prev) => ({ ...prev, [quizId]: r }))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSubmitting(false)
    }
  }

  async function onComplete(): Promise<void> {
    if (!id || completing) return
    setCompleting(true)
    try {
      await api.completeLesson(id)
      // Navigate back to the course so the user sees the next
      // unlock. The page itself would 200 yet the lesson status
      // would say "done" — the user needs the course tree to
      // see the next lesson appear.
      if (data?.course) {
        navigate(`/courses/${data.course.id}`)
      } else {
        void load()
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setCompleting(false)
    }
  }

  if (loading) {
    return <p className="p-6 text-sm text-slate-400 italic">Loading…</p>
  }
  if (error) {
    return <p className="p-6 text-sm text-red-600">{error}</p>
  }
  if (!data) return <></>

  const { lesson, course, quizzes } = data
  const isLocked = lesson.status === 'locked'
  const isDone = lesson.status === 'done'
  const allAnswered = quizzes.length === 0 || quizzes.every((q) => results[q.id])
  const canComplete = !isDone && !isLocked && allAnswered

  return (
    <section className="p-6 max-w-3xl mx-auto space-y-6">
      <header className="space-y-2">
        <Link
          to={`/courses/${course.id}`}
          className="text-xs text-slate-500 hover:text-slate-700"
        >
          ← {course.title}
        </Link>
        <div className="flex items-baseline gap-2">
          <h1 className="text-2xl font-semibold">{lesson.title}</h1>
          <span
            className={
              'text-[10px] uppercase tracking-wide font-mono px-2 py-0.5 rounded ' +
              (isDone
                ? 'bg-emerald-100 text-emerald-700'
                : isLocked
                  ? 'bg-slate-200 text-slate-600'
                  : 'bg-blue-100 text-blue-700')
            }
          >
            {lesson.status}
          </span>
        </div>
      </header>

      {isLocked && (
        <div
          data-testid="lesson-locked"
          className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-slate-50 dark:bg-slate-900 text-sm text-slate-600"
        >
          🔒 This lesson is locked. The tutor hasn't written the
          content yet — check back once the agent has materialised it.
        </div>
      )}

      {!isLocked && (
        <article
          data-testid="lesson-content"
          className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 px-6 py-5 prose dark:prose-invert max-w-none text-sm"
        >
          {lesson.content_md ? (
            <ReactMarkdown remarkPlugins={[remarkGfm]}>
              {lesson.content_md}
            </ReactMarkdown>
          ) : (
            <p className="text-slate-400 italic">No content yet.</p>
          )}
        </article>
      )}

      {lesson.task_id && (
        <div className="text-xs text-slate-500">
          Exercise:{' '}
          <Link
            to={`/tasks/${lesson.task_id}`}
            className="text-slate-700 dark:text-slate-200 hover:underline"
          >
            Open task
          </Link>
        </div>
      )}

      {!isLocked && quizzes.length > 0 && (
        <div className="space-y-4">
          <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">
            Quizzes
          </h2>
          {quizzes.map((q) => {
            const result = results[q.id]
            return (
              <div
                key={q.id}
                data-testid="quiz-row"
                className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950 space-y-2"
              >
                <div className="prose dark:prose-invert max-w-none text-sm">
                  <ReactMarkdown remarkPlugins={[remarkGfm]}>
                    {q.question_md}
                  </ReactMarkdown>
                </div>
                {isDone ? (
                  <p className="text-xs text-slate-400 italic">
                    (lesson completed — answers not editable)
                  </p>
                ) : (
                  <>
                    <textarea
                      data-testid="quiz-answer-input"
                      value={answers[q.id] ?? ''}
                      onChange={(e) =>
                        setAnswers((prev) => ({
                          ...prev,
                          [q.id]: e.target.value,
                        }))
                      }
                      rows={3}
                      className="w-full rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-950 px-3 py-2 text-sm"
                      placeholder="Type your answer…"
                    />
                    <div className="flex items-center gap-3">
                      <button
                        type="button"
                        data-testid="quiz-submit"
                        onClick={() => void onAnswer(q.id)}
                        disabled={submitting || !(answers[q.id] ?? '').trim()}
                        className="px-3 py-1.5 rounded bg-slate-700 hover:bg-slate-800 disabled:opacity-50 text-white text-sm"
                      >
                        Submit
                      </button>
                      {result && (
                        <span
                          data-testid="quiz-result"
                          className={
                            'text-xs ' +
                            (result.correct
                              ? 'text-emerald-700'
                              : 'text-amber-700')
                          }
                        >
                          {result.correct
                            ? '✓ Correct'
                            : result.review_task_id
                              ? '⏳ Pending tutor review'
                              : '✗ Try again'}
                          {result.feedback_md && !result.correct && !result.review_task_id && (
                            <span className="text-slate-400 ml-2">
                              ({result.feedback_md})
                            </span>
                          )}
                        </span>
                      )}
                    </div>
                  </>
                )}
              </div>
            )
          })}
        </div>
      )}

      <div className="flex items-center gap-3 pt-2">
        <button
          type="button"
          data-testid="lesson-complete"
          onClick={() => void onComplete()}
          disabled={!canComplete || completing}
          title={
            isLocked
              ? 'Lesson is locked'
              : !allAnswered
                ? 'Answer all quizzes first'
                : isDone
                  ? 'Already completed'
                  : 'Mark this lesson done'
          }
          className="px-3 py-1.5 rounded bg-emerald-600 hover:bg-emerald-700 disabled:opacity-50 text-white text-sm"
        >
          {isDone ? 'Completed' : 'Complete lesson'}
        </button>
        {!isLocked && !isDone && quizzes.length > 0 && !allAnswered && (
          <span className="text-xs text-slate-500">
            Answer all quizzes to enable completion.
          </span>
        )}
      </div>
    </section>
  )
}

// ---------------------------------------------------------------------------
// Data plumbing
// ---------------------------------------------------------------------------

interface LessonLoad {
  lesson: {
    id: string
    title: string
    status: string
    content_md: string
    task_id?: string
  }
  course: { id: string; title: string }
  quizzes: {
    id: string
    question_md: string
    expected_md?: string
    kind: 'open' | 'exact'
  }[]
}

/**
 * loadLesson fetches a lesson + its parent course + its quizzes.
 *
 * The backend tree endpoint `GET /courses/{id}` already returns
 * every lesson (with `content_md` and `task_id`) and every quiz
 * for the course. We iterate the course list to find the parent
 * course, then filter the tree. The single-owner case has tens of
 * courses at most — N+1 is fine. A future enhancement could add
 * `GET /lessons/{id}` for direct fetch.
 */
async function loadLesson(lessonId: string): Promise<LessonLoad> {
  const list = await api.listCourses()
  for (const c of list.courses) {
    const tree = await api.getCourse(c.id)
    const found = tree.lessons.find((l) => l.id === lessonId)
    if (!found) continue
    const ourQuizzes = (tree.quizzes ?? [])
      .filter((q) => q.lesson_id === lessonId)
      .map((q) => ({
        id: q.id,
        question_md: q.question_md,
        expected_md: q.expected_md,
        kind: q.kind,
      }))
    return {
      lesson: {
        id: found.id,
        title: found.title,
        status: found.status,
        content_md: found.content_md ?? '',
        task_id: found.task_id,
      },
      course: { id: c.id, title: c.title },
      quizzes: ourQuizzes,
    }
  }
  throw new Error('lesson not found')
}
