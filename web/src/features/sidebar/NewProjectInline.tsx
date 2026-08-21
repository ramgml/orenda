/**
 * Inline form to create a new project from inside the sidebar.
 *
 * Designed to fit in 240px: a single text input + Enter to submit,
 * Escape to cancel. After successful creation the form closes itself
 * and the new project appears at the top of the Active section once
 * the invalidation lands. The collapsed-mode equivalent is a small
 * "+" button that toggles a transient form slide-down.
 */
import { FormEvent, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';

import { api, type Project } from '@/shared/api/client';
import { projectsQueryKey } from '@/shared/hooks/useProjects';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';

interface NewProjectInlineProps {
  /** Hide the form (default true). The component manages its own open state. */
  collapsed?: boolean;
}

export function NewProjectInline({ collapsed = false }: NewProjectInlineProps): JSX.Element {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const qc = useQueryClient();

  const mutation = useMutation({
    mutationFn: (n: string) => api.createProject({ name: n }),
    onSuccess: (created: Project) => {
      // Optimistic prepend so the new project appears immediately.
      qc.setQueryData<Project[]>(projectsQueryKey, (prev) =>
        prev ? [created, ...prev] : [created],
      );
      // Re-sync from the server to pick up boards/columns and any
      // server-assigned timestamps we might have missed.
      void qc.invalidateQueries({ queryKey: projectsQueryKey });
      setName('');
      setError(null);
      setOpen(false);
    },
    onError: (e: Error) => setError(e.message),
  });

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    mutation.mutate(trimmed);
  }

  if (collapsed) {
    return (
      <div className="px-2 py-1 flex justify-center">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setOpen((v) => !v)}
          title="New project"
          aria-label="New project"
          className="h-7 w-7 text-slate-500 hover:text-slate-700 dark:hover:text-slate-300"
        >
          {open ? '×' : '+'}
        </Button>
        {open && (
          <form
            onSubmit={submit}
            className="absolute left-12 ml-1 z-20 flex gap-1 p-1.5 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-950 shadow"
          >
            <Input
              autoFocus
              type="text"
              placeholder="Project name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setOpen(false);
              }}
              className="h-8 text-sm w-40 px-2 py-1"
            />
            <Button
              type="submit"
              disabled={mutation.isPending || !name.trim()}
              size="sm"
              className="px-2 py-1 h-auto text-xs"
            >
              Add
            </Button>
          </form>
        )}
      </div>
    );
  }

  if (!open) {
    return (
      <Button
        variant="outline"
        onClick={() => setOpen(true)}
        className="mx-2 mb-2 mt-1 w-[calc(100%-1rem)] justify-start gap-2 px-2 py-1.5 h-auto text-xs text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 border-dashed border-slate-300 dark:border-slate-700"
      >
        <span aria-hidden>+</span>
        <span>New project</span>
      </Button>
    );
  }

  return (
    <form
      onSubmit={submit}
      className="mx-2 mb-2 mt-1 p-2 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-950"
    >
      <Input
        autoFocus
        type="text"
        placeholder="Project name"
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') setOpen(false);
        }}
        className="text-sm"
      />
      {error && <p className="mt-1 text-[11px] text-red-600">{error}</p>}
      <div className="mt-2 flex gap-2">
        <Button
          type="submit"
          disabled={mutation.isPending || !name.trim()}
          size="sm"
          className="flex-1 px-2 py-1 h-auto text-xs"
        >
          {mutation.isPending ? 'Creating…' : 'Create'}
        </Button>
        <Button
          variant="outline"
          type="button"
          onClick={() => {
            setOpen(false);
            setError(null);
            setName('');
          }}
          size="sm"
          className="px-2 py-1 h-auto text-xs"
        >
          Cancel
        </Button>
      </div>
    </form>
  );
}
