import { useCallback, useEffect, useMemo, useState } from 'react'

import { api, type Course } from '@/shared/api/client'

/**
 * Phase 27.6: inline curriculum editor for the owner.
 *
 * The agent's curriculum swap endpoint is reused verbatim — same
 * shape, same atomicity — but the editor is rendered for the owner
 * when the course is in draft or review. The shape of the editor's
 * local state is intentionally close to the wire payload: each
 * element has a client-side id (uuid) so reorders/removals stay
 * referentially stable while the user is typing. On save we map
 * to the wire format and let the service decide which IDs are new.
 *
 * Why a separate component: the editor has its own state lifecycle
 * (dirty / saving / error) and is reused only by CourseDetailPage,
 * but pulling it out keeps that file readable. The component is
 * presentational — it does no fetching; the parent supplies the
 * initial tree and is called back on success.
 */
export interface EditorQuiz {
  id: string
  position: number
  question_md: string
  expected_md: string
  kind: 'exact' | 'open'
}

export interface EditorLesson {
  id: string
  title: string
  position: number
  content_md: string
  quizzes: EditorQuiz[]
}

export interface EditorModule {
  id: string
  title: string
  description: string
  position: number
  lessons: EditorLesson[]
}

export function CourseCurriculumEditor(props: {
  course: Course
  initialModules: EditorModule[]
  onCancel: () => void
  onSaved: () => void
}): JSX.Element {
  const { course, onCancel, onSaved } = props
  const [modules, setModules] = useState<EditorModule[]>(props.initialModules)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Re-seed when the parent reloads (e.g. after cancel-save).
  useEffect(() => {
    setModules(props.initialModules)
  }, [props.initialModules])

  const isReadOnly = course.status !== 'draft' && course.status !== 'review'

  const addModule = useCallback(() => {
    setModules((prev) => [
      ...prev,
      {
        id: crypto.randomUUID(),
        title: '',
        description: '',
        position: prev.length,
        lessons: [],
      },
    ])
  }, [])

  const updateModule = useCallback((id: string, patch: Partial<EditorModule>) => {
    setModules((prev) => prev.map((m) => (m.id === id ? { ...m, ...patch } : m)))
  }, [])

  const removeModule = useCallback((id: string) => {
    setModules((prev) => prev.filter((m) => m.id !== id))
  }, [])

  const addLesson = useCallback((moduleID: string) => {
    setModules((prev) =>
      prev.map((m) =>
        m.id === moduleID
          ? {
              ...m,
              lessons: [
                ...m.lessons,
                {
                  id: crypto.randomUUID(),
                  title: '',
                  position: m.lessons.length,
                  content_md: '',
                  quizzes: [],
                },
              ],
            }
          : m,
      ),
    )
  }, [])

  const updateLesson = useCallback(
    (moduleID: string, lessonID: string, patch: Partial<EditorLesson>) => {
      setModules((prev) =>
        prev.map((m) =>
          m.id === moduleID
            ? {
                ...m,
                lessons: m.lessons.map((l) => (l.id === lessonID ? { ...l, ...patch } : l)),
              }
            : m,
        ),
      )
    },
    [],
  )

  const removeLesson = useCallback((moduleID: string, lessonID: string) => {
    setModules((prev) =>
      prev.map((m) =>
        m.id === moduleID ? { ...m, lessons: m.lessons.filter((l) => l.id !== lessonID) } : m,
      ),
    )
  }, [])

  const addQuiz = useCallback((moduleID: string, lessonID: string) => {
    setModules((prev) =>
      prev.map((m) =>
        m.id === moduleID
          ? {
              ...m,
              lessons: m.lessons.map((l) =>
                l.id === lessonID
                  ? {
                      ...l,
                      quizzes: [
                        ...l.quizzes,
                        {
                          id: crypto.randomUUID(),
                          position: l.quizzes.length,
                          question_md: '',
                          expected_md: '',
                          kind: 'exact',
                        },
                      ],
                    }
                  : l,
              ),
            }
          : m,
      ),
    )
  }, [])

  const updateQuiz = useCallback(
    (moduleID: string, lessonID: string, quizID: string, patch: Partial<EditorQuiz>) => {
      setModules((prev) =>
        prev.map((m) =>
          m.id === moduleID
            ? {
                ...m,
                lessons: m.lessons.map((l) =>
                  l.id === lessonID
                    ? {
                        ...l,
                        quizzes: l.quizzes.map((q) => (q.id === quizID ? { ...q, ...patch } : q)),
                      }
                    : l,
                ),
              }
            : m,
        ),
      )
    },
    [],
  )

  const removeQuiz = useCallback(
    (moduleID: string, lessonID: string, quizID: string) => {
      setModules((prev) =>
        prev.map((m) =>
          m.id === moduleID
            ? {
                ...m,
                lessons: m.lessons.map((l) =>
                  l.id === lessonID
                    ? { ...l, quizzes: l.quizzes.filter((q) => q.id !== quizID) }
                    : l,
                ),
              }
            : m,
        ),
      )
    },
    [],
  )

  const onSave = useCallback(async () => {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const payload = {
        modules: modules.map((m, mi) => ({
          id: m.id,
          title: m.title,
          description: m.description,
          position: mi,
          lessons: m.lessons.map((l, li) => ({
            id: l.id,
            title: l.title,
            position: li,
            content_md: l.content_md,
            quizzes: l.quizzes.map((q, qi) => ({
              id: q.id,
              position: qi,
              question_md: q.question_md,
              expected_md: q.expected_md,
              kind: q.kind,
            })),
          })),
        })),
      }
      for (const m of payload.modules) {
        if (!m.title.trim()) {
          setError('Every module needs a title.')
          setBusy(false)
          return
        }
        for (const l of m.lessons) {
          if (!l.title.trim()) {
            setError('Every lesson needs a title.')
            setBusy(false)
            return
          }
          for (const q of l.quizzes) {
            if (!q.question_md.trim()) {
              setError('Every quiz needs a question.')
              setBusy(false)
              return
            }
            if (q.kind === 'exact' && !q.expected_md.trim()) {
              setError('Exact quizzes need an expected answer.')
              setBusy(false)
              return
            }
          }
        }
      }
      await api.submitCurriculum(course.id, payload)
      onSaved()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }, [busy, course.id, modules, onSaved])

  const moduleCount = modules.length
  const lessonCount = useMemo(
    () => modules.reduce((acc, m) => acc + m.lessons.length, 0),
    [modules],
  )
  const quizCount = useMemo(
    () => modules.reduce((acc, m) => acc + m.lessons.reduce((b, l) => b + l.quizzes.length, 0), 0),
    [modules],
  )

  if (isReadOnly) {
    return (
      <p className="text-sm text-slate-500 italic" data-testid="editor-readonly">
        Curriculum is locked once the course is active. Edit individual
        lessons from the lesson page.
      </p>
    )
  }

  return (
    <div className="space-y-3" data-testid="course-editor">
      <div className="text-xs text-slate-500 font-mono">
        {moduleCount} modules · {lessonCount} lessons · {quizCount} quizzes
      </div>

      {modules.length === 0 && (
        <p className="text-sm text-slate-400 italic">
          No modules yet. Add the first one to start shaping the
          program.
        </p>
      )}

      <ul className="space-y-3">
        {modules.map((m) => (
          <li
            key={m.id}
            data-testid="editor-module"
            className="rounded border border-slate-200 dark:border-slate-800 p-3 bg-white dark:bg-slate-950 space-y-2"
          >
            <div className="flex gap-2">
              <input
                type="text"
                value={m.title}
                onChange={(e) => updateModule(m.id, { title: e.target.value })}
                placeholder="Module title (e.g. Basics)"
                data-testid="editor-module-title"
                className="flex-1 px-2 py-1.5 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
              />
              <button
                type="button"
                onClick={() => removeModule(m.id)}
                data-testid="editor-module-remove"
                className="px-2 py-1 rounded text-red-700 hover:bg-red-50 text-sm"
              >
                Remove
              </button>
            </div>
            <textarea
              value={m.description}
              onChange={(e) => updateModule(m.id, { description: e.target.value })}
              placeholder="Optional description"
              rows={2}
              className="w-full px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-xs"
            />

            <ul className="space-y-2 pl-3 border-l border-slate-200 dark:border-slate-800">
              {m.lessons.map((l) => (
                <li
                  key={l.id}
                  data-testid="editor-lesson"
                  className="space-y-1 rounded bg-slate-50 dark:bg-slate-900 p-2"
                >
                  <div className="flex gap-2">
                    <input
                      type="text"
                      value={l.title}
                      onChange={(e) => updateLesson(m.id, l.id, { title: e.target.value })}
                      placeholder="Lesson title"
                      data-testid="editor-lesson-title"
                      className="flex-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-sm"
                    />
                    <button
                      type="button"
                      onClick={() => removeLesson(m.id, l.id)}
                      data-testid="editor-lesson-remove"
                      className="px-2 py-1 rounded text-red-700 hover:bg-red-100 text-xs"
                    >
                      Remove
                    </button>
                  </div>
                  <textarea
                    value={l.content_md}
                    onChange={(e) => updateLesson(m.id, l.id, { content_md: e.target.value })}
                    placeholder="Optional lesson body (markdown)"
                    rows={2}
                    className="w-full px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-xs"
                  />
                  <div className="pl-2 border-l border-slate-200 dark:border-slate-700">
                    <ul className="space-y-1">
                      {l.quizzes.map((q) => (
                        <li key={q.id} className="space-y-1" data-testid="editor-quiz">
                          <div className="flex gap-2 items-center">
                            <select
                              value={q.kind}
                              onChange={(e) =>
                                updateQuiz(m.id, l.id, q.id, {
                                  kind: e.target.value as 'exact' | 'open',
                                })
                              }
                              className="text-xs rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 px-1 py-0.5"
                            >
                              <option value="exact">exact</option>
                              <option value="open">open</option>
                            </select>
                            <input
                              type="text"
                              value={q.question_md}
                              onChange={(e) =>
                                updateQuiz(m.id, l.id, q.id, { question_md: e.target.value })
                              }
                              placeholder="Question"
                              data-testid="editor-quiz-question"
                              className="flex-1 px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-xs"
                            />
                            <button
                              type="button"
                              onClick={() => removeQuiz(m.id, l.id, q.id)}
                              className="px-1.5 py-0.5 rounded text-red-700 hover:bg-red-100 text-xs"
                            >
                              ×
                            </button>
                          </div>
                          {q.kind === 'exact' && (
                            <input
                              type="text"
                              value={q.expected_md}
                              onChange={(e) =>
                                updateQuiz(m.id, l.id, q.id, { expected_md: e.target.value })
                              }
                              placeholder="Expected answer"
                              className="w-full px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-950 text-xs"
                            />
                          )}
                        </li>
                      ))}
                    </ul>
                    <button
                      type="button"
                      onClick={() => addQuiz(m.id, l.id)}
                      data-testid="editor-add-quiz"
                      className="mt-1 text-xs text-orenda-700 hover:underline"
                    >
                      + Add quiz
                    </button>
                  </div>
                </li>
              ))}
            </ul>
            <button
              type="button"
              onClick={() => addLesson(m.id)}
              data-testid="editor-add-lesson"
              className="text-xs text-orenda-700 hover:underline"
            >
              + Add lesson
            </button>
          </li>
        ))}
      </ul>

      <button
        type="button"
        onClick={addModule}
        data-testid="editor-add-module"
        className="text-sm text-orenda-700 hover:underline"
      >
        + Add module
      </button>

      {error && <p className="text-sm text-red-600">{error}</p>}

      <div className="flex gap-2 pt-2">
        <button
          type="button"
          onClick={() => void onSave()}
          disabled={busy}
          data-testid="editor-save"
          className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
        >
          {busy ? 'Saving…' : 'Save curriculum'}
        </button>
        <button
          type="button"
          onClick={onCancel}
          disabled={busy}
          className="px-3 py-1.5 rounded border border-slate-300 text-slate-700 hover:bg-slate-50 text-sm"
        >
          Cancel
        </button>
      </div>
    </div>
  )
}
