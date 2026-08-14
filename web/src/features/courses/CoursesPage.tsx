import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';

import { api, type Course } from '@/shared/api/client';

/**
 * Phase 18.7: Courses list + create wizard.
 *
 * Two affordances on one page:
 *   1. List of existing courses (one row per course).
 *   2. Inline create form: title + intent text → submit.
 *
 * Phase 27.6 adds a "Build it myself" toggle: when checked, the
 * server skips the agent generator task so a sleeping tutor can't
 * overwrite the manual curriculum. The wizard still creates a draft;
 * the owner builds the program via the editor on /courses/:id.
 */
export function CoursesPage(): JSX.Element {
  const navigate = useNavigate();
  const [courses, setCourses] = useState<Course[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [title, setTitle] = useState('');
  const [intent, setIntent] = useState('');
  const [skipGenerator, setSkipGenerator] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    try {
      const r = await api.listCourses();
      setCourses(r.courses ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function onCreate(e: React.FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (!title.trim() || busy) return;
    setBusy(true);
    setError(null);
    try {
      const created = await api.createCourse({
        title: title.trim(),
        intent_md: intent,
        skip_generator: skipGenerator,
      });
      setTitle('');
      setIntent('');
      setSkipGenerator(false);
      // Jump straight into the editor for "I'll build it myself"
      // courses — the empty tree on the detail page is unhelpful.
      navigate(`/courses/${created.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="p-6 max-w-3xl mx-auto space-y-6">
      <header>
        <h1 className="text-2xl font-semibold">Courses</h1>
        <p className="text-sm text-slate-500 mt-1">
          Personal learning paths built by an AI tutor. State the intent; the tutor proposes a
          curriculum; approve to start.
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
        <label className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
          <input
            type="checkbox"
            checked={skipGenerator}
            onChange={(e) => setSkipGenerator(e.target.checked)}
            data-testid="course-skip-generator"
            className="h-4 w-4 rounded border-slate-300"
          />
          <span>I'll build the curriculum myself (skip tutor agent)</span>
        </label>
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
        <p className="text-sm text-slate-400 italic">No courses yet. Create one above to start.</p>
      ) : (
        <ul className="space-y-2">
          {courses.map((c) => (
            <li
              key={c.id}
              data-testid="course-row"
              className="rounded border border-slate-200 dark:border-slate-800 p-3 bg-white dark:bg-slate-950"
            >
              <Link
                to={`/courses/${c.id}`}
                className="text-slate-800 dark:text-slate-100 hover:underline"
              >
                {c.title}
              </Link>
              <span className="ml-2 text-[10px] text-slate-400 font-mono">{c.status}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
