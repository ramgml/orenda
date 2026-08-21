import { FormEvent, useState } from 'react';
import { Link } from 'react-router-dom';

import { useAuth } from '@/features/auth/AuthContext';
import { api, type Project } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Checkbox } from '@/shared/ui/checkbox';
import { Input } from '@/shared/ui/input';

/**
 * /projects — list of projects owned by the authenticated user.
 *
 * Phase 1 ships list + create. Phase 6+ adds the show-archived toggle.
 */
export function ProjectsPage(): JSX.Element {
  const { user } = useAuth();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [showArchived, setShowArchived] = useState(false);

  async function load(): Promise<void> {
    try {
      setProjects(await api.listProjects());
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  if (projects === null && !error) {
    load();
  }

  async function onCreate(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (!name.trim()) return;
    try {
      const p = await api.createProject({ name: name.trim() });
      setProjects((prev) => (prev ? [p, ...prev] : [p]));
      setName('');
      setCreating(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section>
      <header className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-semibold">Projects</h1>
          <p className="text-sm text-slate-500">
            Signed in as <span className="font-mono">{user?.email ?? '…'}</span>
          </p>
        </div>
        <Button type="button" onClick={() => setCreating((v) => !v)} variant="default" size="sm">
          {creating ? 'Cancel' : 'New project'}
        </Button>
      </header>

      {error && (
        <div className="mb-4 rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      {creating && (
        <form
          onSubmit={onCreate}
          className="mb-4 p-4 rounded border border-border bg-white dark:bg-slate-950 flex gap-2"
        >
          <Input
            type="text"
            placeholder="Project name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="flex-1"
            autoFocus
          />
          <Button type="submit" variant="default" size="sm">
            Create
          </Button>
        </form>
      )}

      {projects === null ? (
        <p className="text-slate-500">Loading…</p>
      ) : projects.length === 0 ? (
        <p className="text-slate-500">No projects yet. Create your first one above.</p>
      ) : (
        <>
          <div className="mb-3 text-xs text-slate-500 flex items-center gap-2">
            <label className="flex items-center gap-1">
              <Checkbox
                checked={showArchived}
                onCheckedChange={(v) => setShowArchived(v === true)}
              />
              Show archived
            </label>
            <span className="ml-auto">
              {projects.filter((p) => showArchived || !p.archived).length} shown · {projects.length}{' '}
              total
            </span>
          </div>
          <ul className="grid sm:grid-cols-2 lg:grid-cols-3 gap-3">
            {projects
              .filter((p) => showArchived || !p.archived)
              .map((p) => (
                <li key={p.id}>
                  <Link
                    to={`/projects/${p.id}`}
                    className={`block rounded-lg border p-4 transition ${
                      p.archived
                        ? 'border-border bg-slate-50 dark:bg-slate-900 opacity-70 hover:opacity-100'
                        : 'border-border bg-white dark:bg-slate-950 hover:border-orenda-500'
                    }`}
                  >
                    <div className="flex items-center gap-2 mb-1">
                      <span
                        aria-hidden
                        className="inline-block h-3 w-3 rounded"
                        style={{ backgroundColor: p.color }}
                      />
                      <span className="font-medium">{p.name}</span>
                      {p.archived && (
                        <span className="ml-auto text-[10px] uppercase tracking-wide text-slate-500 border border-slate-300 dark:border-slate-700 rounded px-1.5 py-0.5">
                          archived
                        </span>
                      )}
                    </div>
                    {p.description && (
                      <p className="text-sm text-slate-600 dark:text-slate-300">{p.description}</p>
                    )}
                    <p className="text-xs text-slate-400 mt-2 font-mono">{p.id.slice(0, 8)}</p>
                  </Link>
                </li>
              ))}
          </ul>
        </>
      )}
    </section>
  );
}
