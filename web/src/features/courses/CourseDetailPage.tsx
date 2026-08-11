import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { api } from '@/shared/api/client'
import { useWebSocketTopic } from '@/shared/ws'

/**
 * Phase 18.7: Course tree view.
 *
 * Renders the full course = modules + lessons + progress. The
 * lifecycle state machine is small:
 *   - draft:     tutor hasn't submitted yet
 *   - review:    tutor submitted; owner can approve / request changes
 *   - active:    approved, lessons open sequentially
 *   - done:      all lessons completed
 */
export function CourseDetailPage(): JSX.Element {
  const { id } = useParams<{ id: string }>()
  const [data, setData] = useState<CourseDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    if (!id) return
    try {
      const r = await api.getCourse(id)
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

  // Re-fetch on task events (the agent may submit a curriculum while
  // we're staring at the page).
  useWebSocketTopic('tasks', () => {
    void load()
  })

  async function onApprove(): Promise<void> {
    if (!id || busy) return
    setBusy(true)
    try {
      await api.approveCourse(id)
      void load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  async function onRequestChanges(): Promise<void> {
    if (!id || busy) return
    setBusy(true)
    try {
      await api.requestCourseChanges(id)
      void load()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  if (loading) {
    return <p className="p-6 text-sm text-slate-400 italic">Loading…</p>
  }
  if (error) {
    return <p className="p-6 text-sm text-red-600">{error}</p>
  }
  if (!data) return <></>

  const { course, modules, lessons, progress } = data

  // Bucket lessons by module.
  const lessonsByModule = new Map<string, typeof lessons>()
  for (const l of lessons) {
    const arr = lessonsByModule.get(l.module_id) ?? []
    arr.push(l)
    lessonsByModule.set(l.module_id, arr)
  }
  for (const arr of lessonsByModule.values()) {
    arr.sort((a, b) => a.position - b.position)
  }

  return (
    <section className="p-6 max-w-3xl mx-auto space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">{course.title}</h1>
        <p className="text-sm text-slate-500 mt-1">
          Status: <span className="font-mono">{course.status}</span> · Level:{' '}
          <span className="font-mono">{course.level}</span> · Pace:{' '}
          <span className="font-mono">{course.pace}</span>
        </p>
        {course.intent_md && (
          <p className="text-sm text-slate-600 dark:text-slate-300 mt-3 italic">
            "{course.intent_md}"
          </p>
        )}
      </header>

      {/* Lifecycle actions */}
      {course.status === 'review' && (
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => void onApprove()}
            disabled={busy}
            data-testid="course-approve"
            className="px-3 py-1.5 rounded bg-emerald-600 hover:bg-emerald-700 disabled:opacity-50 text-white text-sm"
          >
            Approve curriculum
          </button>
          <button
            type="button"
            onClick={() => void onRequestChanges()}
            disabled={busy}
            className="px-3 py-1.5 rounded border border-amber-300 text-amber-700 hover:bg-amber-50 text-sm"
          >
            Request changes
          </button>
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

      {/* Modules + lessons */}
      {modules.length === 0 ? (
        <p className="text-sm text-slate-400 italic">
          No modules yet. The tutor agent will submit a curriculum
          when ready.
        </p>
      ) : (
        <ul className="space-y-4">
          {modules.map((m) => {
            const ls = lessonsByModule.get(m.id) ?? []
            return (
              <li
                key={m.id}
                className="rounded border border-slate-200 dark:border-slate-800 p-3 bg-white dark:bg-slate-950"
              >
                <h3 className="text-sm font-semibold text-slate-700 dark:text-slate-200">
                  {m.title}
                </h3>
                {m.description && (
                  <p className="text-xs text-slate-500 mt-1">{m.description}</p>
                )}
                <ul className="mt-2 space-y-1">
                  {ls.map((l) => (
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
                            : 'text-slate-800 dark:text-slate-100 hover:underline'
                        }
                      >
                        {l.status === 'locked' && '🔒 '}
                        {l.title}
                      </Link>
                      <span className="text-[10px] text-slate-400 font-mono">
                        {l.status}
                      </span>
                    </li>
                  ))}
                </ul>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

type CourseDetail = Awaited<ReturnType<typeof api.getCourse>>