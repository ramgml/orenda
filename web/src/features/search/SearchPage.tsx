import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';

import { TaskLink } from '@/features/tasks/TaskModal';
import { api, type SearchHit } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';

/**
 * /search — full-text search results across pages, tasks, comments.
 *
 * Phase 5 ships a simple list view grouped by type. Phase 6 will add
 * Cmd+K hotkey + a modal launcher.
 */
export function SearchPage(): JSX.Element {
  const [q, setQ] = useState('');
  const [hits, setHits] = useState<SearchHit[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        const input = document.querySelector<HTMLInputElement>('input[name="search"]');
        input?.focus();
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  async function run(): Promise<void> {
    if (!q.trim()) return;
    setBusy(true);
    try {
      const res = await api.search({ q: q.trim(), limit: 50 });
      setHits(res.hits);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section>
      <h1 className="text-2xl font-semibold mb-2">Search</h1>
      <p className="text-xs text-slate-500 mb-4">
        Press <kbd className="px-1 py-0.5 rounded border border-slate-300">Cmd+K</kbd> to focus.
      </p>

      <form
        onSubmit={(e) => {
          e.preventDefault();
          run();
        }}
        className="flex gap-2 mb-4"
      >
        <Input
          name="search"
          type="text"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search pages, tasks, comments…"
          className="flex-1"
        />
        <Button type="submit" disabled={busy || !q.trim()}>
          {busy ? 'Searching…' : 'Search'}
        </Button>
      </form>

      {error && (
        <div className="mb-4 rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      {hits === null ? (
        <p className="text-slate-500">Run a search to see results.</p>
      ) : hits.length === 0 ? (
        <p className="text-slate-500">No matches.</p>
      ) : (
        <ul className="space-y-3">
          {hits.map((h) => (
            <li key={`${h.type}:${h.id}`} className="rounded border border-border p-3">
              <div className="flex items-center justify-between mb-1">
                {h.type === 'page' ? (
                  <Link to={hitHref(h)} className="font-medium text-orenda-600 hover:underline">
                    {h.title || h.id.slice(0, 12)}
                  </Link>
                ) : (
                  <TaskLink taskId={h.id} className="font-medium text-orenda-600 hover:underline">
                    {h.title || h.id.slice(0, 12)}
                  </TaskLink>
                )}
                <span className="text-xs uppercase text-slate-500 tracking-wide">
                  {h.type} · {h.score.toFixed(2)}
                </span>
              </div>
              <p
                className="text-sm text-foreground"
                dangerouslySetInnerHTML={{ __html: h.snippet }}
              />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function hitHref(h: SearchHit): string {
  switch (h.type) {
    case 'page':
      return `/wiki/${h.id}`; // slug-vs-id: the backend returns the id for FTS rows
    case 'task':
      return `/tasks/${h.id}`;
    case 'comment':
      // Comments don't have a dedicated route; link to the parent task.
      return `/tasks/${h.id}`;
    default:
      return '/';
  }
}
