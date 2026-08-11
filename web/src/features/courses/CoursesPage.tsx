import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'

import { api, type Course } from '@/shared/api/client'

/**
 * Phase 18.7: Courses list + create wizard.
 *
 * Two affordances on one page:
 *   1. List of existing courses (one row per course).
 *   2. Inline create form: title + intent text → submit.
 *
 * The intent text is the raw prompt the tutor agent will read. The
 * UI doesn't try to structure it — the agent's job is to ask
 * clarifying questions, not ours.
 */
export function CoursesPage(): JSX.Element {
  const [courses, setCourses] = useState<Course[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [title, setTitle] = useState('')
  const [intent, setIntent] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const r = await api.listCourses()
      setCourses(r.courses ?? [])
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onCreate(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    if (!title.trim() || busy) return
    setBusy(true)
    setError(null)
    try {
      await api.createCourse({ title: title.trim(), intent_md: intent })
      setTitle('')
      setIntent('')
      void load()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="p-6 max-w-3xl mx-auto space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Courses</h1>
        <p className="text-sm text-slate-500 mt-1">
          Personal learning paths built by an AI tutor. State the
          intent; the tutor proposes a curriculum; approve to start.
        </p>
      </header>

      <form
        onSubmit={(e) => void onCreate(e)}
        className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950 space-y-2"
      >
        <h2 className="text-sm font-semibold text-slate-700 dark:text-slate-200">
          Create a course
        </h2>
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="e.g. Learn Rust in a month"
          data-testid="course-title"
          className="w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
        />
        <textarea
          value={intent}
          onChange={(e) => setIntent(e.target.value)}
          rows={3}
          placeholder="What do you want to learn? What level? How much time per week?"
          data-testid="course-intent"
          className="w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
        />
        <div className="flex justify-end">
          <button
            type="submit"
            disabled={busy || !title.trim()}
            data-testid="course-create"
            className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
          >
            Create
          </button>
        </div>
      </form>

      {error && <p className="text-sm text-red-600">{error}</p>}

      {loading ? (
        <p className="text-sm text-slate-400 italic">Loading…</p>
      ) : courses.length === 0 ? (
        <p className="text-sm text-slate-400 italic">
          No courses yet. Create one above to start.
        </p>
      ) : (
        <ul className="space-y-2">
          {courses.map((c) => (
            <li
              key={c.id}
              data-testid="course-row"
              className="rounded border border-slate-200 dark:border-slate-800 p-3 bg-white dark:bg-slate-950"
            >
              <Link to={`/courses/${c.id}`} className="text-slate-800 dark:text-slate-100 hover:underline">
                {c.title}
              </Link>
              <span className="ml-2 text-[10px] text-slate-400 font-mono">
                {c.status}
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}