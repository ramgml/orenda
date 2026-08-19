import { useEffect, useRef, useState } from 'react';
import { NavLink, Outlet, useNavigate, useParams } from 'react-router-dom';

import { api, type Project } from '@/shared/api/client';

/**
 * /projects/:id — header + tab nav. The actual tab content is rendered
 * by nested routes (Phase 11):
 *   index        → Kanban (ProjectKanbanTab)
 *   activity     → ProjectActivityTab
 *   attachments  → ProjectAttachmentsTab
 *   settings     → ProjectSettingsTab
 *
 * Header UX:
 *   • The project name is inline-editable on click. Enter / blur saves,
 *     Escape cancels. Empty names are rejected client-side and the field
 *     snaps back to the previous value.
 *   • The Archive button used to live here in Phase 2.6 — it has
 *     moved into Settings; this header now only carries the badges.
 *
 * Phase 16: every project here is a real user project. The old system
 * Inbox project is gone; unfiled tasks live at /inbox. There's no
 * special-casing in this page — rename / archive / delete are
 * uniform.
 *
 * Phase pre-11 sidebar refactor lives in AppLayout; this page only
 * owns the project header + tab strip.
 */
export function ProjectDetailPage(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [project, setProject] = useState<Project | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    api
      .getProject(id)
      .then((p) => {
        if (!cancelled) setProject(p);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (error) return <p className="text-red-700">{error}</p>;

  return (
    <section>
      <div className="flex items-center justify-between mb-4 gap-4">
        <h1 className="text-2xl font-semibold flex items-center gap-2 min-w-0">
          <InlineProjectName project={project} onRename={(updated) => setProject(updated)} />
          {project?.archived && (
            <span className="text-xs uppercase tracking-wide text-slate-500 border border-slate-300 dark:border-slate-700 rounded px-2 py-1 flex-shrink-0">
              archived
            </span>
          )}
        </h1>
        {project?.wiki_slug && (
          <a
            href={`/pages/${encodeURIComponent(project.wiki_slug)}`}
            className="text-xs px-2 py-1 rounded border border-slate-300 dark:border-slate-700 hover:bg-slate-100 dark:hover:bg-slate-800 flex-shrink-0"
            title="Open the linked wiki page"
          >
            Wiki page ↗
          </a>
        )}
      </div>

      <ProjectTabs projectId={project?.id ?? id ?? ''} />

      <div className="mt-4">
        <Outlet />
      </div>

      {/* Backstop: if the route matched /projects/:id but the nested
          tab did not render anything (e.g. project not yet loaded and
          tabs need an id), navigate back to the list. */}
      {!project && !error && (
        <p className="text-slate-500 mt-4">
          Loading…
          <button
            type="button"
            onClick={() => navigate('/projects')}
            className="ml-3 text-xs underline"
          >
            Back to projects
          </button>
        </p>
      )}
    </section>
  );
}

/** The project-level tab strip. Sits directly under the header. */
function ProjectTabs({ projectId }: { projectId: string }): JSX.Element | null {
  if (!projectId) return null;
  const base = `/projects/${projectId}`;
  const tab = (to: string, label: string): JSX.Element => (
    <NavLink
      to={to}
      end={to === base}
      className={({ isActive }) =>
        `px-3 py-1.5 text-sm rounded-t border-b-2 -mb-px transition ${
          isActive
            ? 'border-orenda-500 text-orenda-700 dark:text-orenda-300 font-medium'
            : 'border-transparent text-slate-500 hover:text-slate-800 dark:hover:text-slate-200'
        }`
      }
    >
      {label}
    </NavLink>
  );
  return (
    <nav className="flex items-center gap-1 border-b border-slate-200 dark:border-slate-800">
      {tab(base, 'Kanban')}
      {tab(`${base}/activity`, 'Activity')}
      {tab(`${base}/attachments`, 'Attachments')}
      {tab(`${base}/settings`, 'Settings')}
    </nav>
  );
}

/**
 * Inline-editable project name.
 *
 * Click → swap the static text for an `<input>` with auto-focus and
 * the current name pre-selected. Enter / blur saves via PATCH; Escape
 * reverts. Empty input is treated as cancel so the field never
 * disappears into a blank state.
 */
function InlineProjectName({
  project,
  onRename,
}: {
  project: Project | null;
  onRename: (p: Project) => void;
}): JSX.Element {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [saving, setSaving] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // editingRef mirrors `editing` so onBlur can tell "user actually
  // blurred" apart from "Escape just cancelled us, which also blurs
  // the input as a side-effect". Without this guard, pressing Escape
  // would commit the (unchanged) draft via the blur handler.
  const editingRef = useRef(editing);
  editingRef.current = editing;

  function beginEdit(): void {
    if (!project) return;
    setDraft(project.name);
    setLocalError(null);
    setEditing(true);
    // Auto-focus + select happens on the next tick once the input is
    // mounted; a microtask is enough for React 18.
    queueMicrotask(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    });
  }

  async function commit(): Promise<void> {
    if (!project) return;
    const trimmed = draft.trim();
    if (trimmed === '' || trimmed === project.name) {
      setEditing(false);
      return;
    }
    setSaving(true);
    setLocalError(null);
    try {
      const updated = await api.updateProject(project.id, { name: trimmed });
      onRename(updated);
      setEditing(false);
    } catch (e) {
      setLocalError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  function cancel(): void {
    setEditing(false);
    setLocalError(null);
  }

  if (!project) {
    return <span className="font-mono text-base text-slate-500">Project …</span>;
  }

  if (editing) {
    return (
      <span className="inline-flex flex-col gap-1 min-w-0 flex-1">
        <input
          ref={inputRef}
          type="text"
          value={draft}
          disabled={saving}
          onChange={(e) => setDraft(e.target.value)}
          onBlur={() => {
            // Only commit when we're still in the editing state —
            // Escape flips `editing` to false before focus leaves, and
            // blur would otherwise commit the cancelled draft.
            if (editingRef.current) {
              void commit();
            }
          }}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              void commit();
            } else if (e.key === 'Escape') {
              e.preventDefault();
              cancel();
            }
          }}
          aria-label="Project name"
          className="text-2xl font-semibold px-2 py-1 rounded border border-orenda-500 bg-white dark:bg-slate-950 outline-none min-w-0"
        />
        {localError && <span className="text-xs text-red-700">{localError}</span>}
      </span>
    );
  }

  return (
    <button
      type="button"
      onClick={beginEdit}
      title="Click to rename"
      className="text-left truncate cursor-text hover:bg-slate-100 dark:hover:bg-slate-800 px-2 py-1 -mx-2 -my-1 rounded"
    >
      {project.name}
    </button>
  );
}
