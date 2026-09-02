import { useEffect, useMemo, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';

import { api, type Project, type WikiTreeNode } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Textarea } from '@/shared/ui/textarea';

/**
 * /projects/:id/settings — color, description, archive, delete.
 *
 * The project name is intentionally NOT editable here — it is the
 * inline-edited `<h1>` on the project header (Phase 11 UX). That
 * keeps the title discoverable on every tab without forcing the user
 * to remember to come back here.
 *
 * Phase 16: every project here is a real user project. The old system
 * Inbox project is gone (unfiled tasks live at /inbox), so archive
 * and delete are uniform — no special-casing for a "system" project.
 *
 * wiki:project-wiki-link — the Wiki-page field. Autocomplete pulls
 * from GET /api/v1/pages (the wiki tree) and lets the user pick a
 * slug; an explicit empty input clears the link. The save round-trips
 * the slug through PATCH /api/v1/projects/{id}; an unknown slug
 * surfaces as 422 wiki_slug_not_found and we render it inline.
 */
export function ProjectSettingsTab(): JSX.Element {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [project, setProject] = useState<Project | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [color, setColor] = useState('#3b82f6');
  const [description, setDescription] = useState('');
  const [wikiSlug, setWikiSlug] = useState('');
  const [wikiSuggestions, setWikiSuggestions] = useState<WikiTreeNode[]>([]);
  const [confirmDelete, setConfirmDelete] = useState(false);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    Promise.all([api.getProject(id), api.listPages()])
      .then(([p, pages]) => {
        if (cancelled) return;
        setProject(p);
        setColor(p.color || '#3b82f6');
        setDescription(p.description || '');
        setWikiSlug(p.wiki_slug || '');
        setWikiSuggestions(pages.tree);
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  // Flatten the wiki tree into a slug → title list for the datalist.
  // Recursive because /api/v1/pages returns a TreeNode[] with
  // nested children — top-level only would miss deep pages.
  const wikiOptions = useMemo(() => flattenWikiTree(wikiSuggestions), [wikiSuggestions]);

  async function saveBasics(): Promise<void> {
    if (!project) return;
    setBusy(true);
    setError(null);
    try {
      const updated = await api.updateProject(project.id, {
        color,
        description,
        wiki_slug: wikiSlug,
      });
      setProject(updated);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function toggleArchive(): Promise<void> {
    if (!project) return;
    setBusy(true);
    setError(null);
    try {
      const updated = await api.updateProject(project.id, {
        archived: !project.archived,
      });
      setProject(updated);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function deleteProject(): Promise<void> {
    if (!project) return;
    setBusy(true);
    setError(null);
    try {
      await api.deleteProject(project.id);
      navigate('/projects', { replace: true });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  }

  if (!project && !error) return <p className="text-slate-500">Loading…</p>;
  if (!project) return <p className="text-red-700">{error}</p>;

  return (
    <div className="space-y-6 max-w-2xl">
      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      <section className="rounded border border-border bg-background p-4 space-y-3">
        <h2 className="text-base font-semibold">Appearance</h2>
        <div className="flex items-center gap-3">
          <input
            type="color"
            value={color}
            onChange={(e) => setColor(e.target.value)}
            className="h-9 w-12 rounded border border-border cursor-pointer"
            aria-label="Project color"
          />
          <Input
            type="text"
            value={color}
            onChange={(e) => setColor(e.target.value)}
            className="flex-1 font-mono text-sm"
            placeholder="#3b82f6"
          />
        </div>
      </section>

      <section className="rounded border border-border bg-background p-4 space-y-3">
        <h2 className="text-base font-semibold">Description</h2>
        <Textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={4}
          className="text-sm"
          placeholder="What is this project about?"
        />
        <div className="flex justify-end">
          <Button type="button" onClick={saveBasics} disabled={busy} variant="default" size="sm">
            {busy ? 'Saving…' : 'Save changes'}
          </Button>
        </div>
      </section>

      <section className="rounded border border-border bg-background p-4 space-y-3">
        <h2 className="text-base font-semibold">Wiki page</h2>
        <p className="text-sm text-slate-500">
          Link this project to its wiki page (постановка, decision log, roadmap slice). Leave empty
          to unlink.
        </p>
        <Input
          type="text"
          list="wiki-slug-options"
          value={wikiSlug}
          onChange={(e) => setWikiSlug(e.target.value)}
          placeholder="page-slug"
          className="text-sm font-mono"
          aria-label="Wiki page slug"
        />
        <datalist id="wiki-slug-options">
          {wikiOptions.map((o) => (
            <option key={o.slug} value={o.slug}>
              {o.title}
            </option>
          ))}
        </datalist>
        {wikiSlug && (
          <p className="text-xs text-slate-500">
            Save the page as{' '}
            <a
              href={`/wiki/${encodeURIComponent(wikiSlug)}`}
              className="underline text-orenda-600 dark:text-orenda-300"
              target="_blank"
              rel="noreferrer"
            >
              /wiki/{wikiSlug}
            </a>
            .
          </p>
        )}
        <div className="flex justify-end">
          <Button type="button" onClick={saveBasics} disabled={busy} variant="default" size="sm">
            {busy ? 'Saving…' : 'Save changes'}
          </Button>
        </div>
      </section>

      <section className="rounded border border-border bg-background p-4 space-y-3">
        <h2 className="text-base font-semibold">Archive</h2>
        <p className="text-sm text-slate-500">
          Archived projects stay in the list but are hidden from the Kanban view. You can restore
          them later.
        </p>
        <Button type="button" onClick={toggleArchive} disabled={busy} variant="outline" size="sm">
          {project.archived ? 'Unarchive' : 'Archive'}
        </Button>
      </section>

      <section className="rounded border border-red-300 bg-red-50/40 dark:bg-red-900/10 dark:border-red-800 p-4 space-y-3">
        <h2 className="text-base font-semibold text-red-800 dark:text-red-300">Danger zone</h2>
        <p className="text-sm text-muted-foreground">
          Deleting a project removes its tasks, columns, comments, and attachments permanently. This
          cannot be undone.
        </p>
        {confirmDelete ? (
          <div className="flex items-center gap-2">
            <Button
              type="button"
              onClick={deleteProject}
              disabled={busy}
              variant="destructive"
              size="sm"
            >
              {busy ? 'Deleting…' : 'Yes, delete project'}
            </Button>
            <Button
              type="button"
              onClick={() => setConfirmDelete(false)}
              disabled={busy}
              variant="outline"
              size="sm"
            >
              Cancel
            </Button>
          </div>
        ) : (
          <Button
            type="button"
            onClick={() => setConfirmDelete(true)}
            variant="outline"
            size="sm"
            className="border-red-300 text-red-700 hover:bg-red-50"
          >
            Delete project…
          </Button>
        )}
      </section>
    </div>
  );
}

/** flattenWikiTree walks the WikiTreeNode list depth-first, returning a
 * flat {slug, title} array for the slug autocomplete datalist. */
function flattenWikiTree(tree: WikiTreeNode[]): { slug: string; title: string }[] {
  const out: { slug: string; title: string }[] = [];
  const walk = (nodes: WikiTreeNode[] | undefined): void => {
    if (!nodes) return;
    for (const n of nodes) {
      out.push({ slug: n.page.slug, title: n.page.title });
      walk(n.children);
    }
  };
  walk(tree);
  return out;
}
