/**
 * Inline form to create a new project from inside the sidebar.
 *
 * Designed to fit in 240px: a single text input + Enter to submit,
 * Escape to cancel. After successful creation the form closes itself
 * and the new project appears at the top of the Active section once
 * the invalidation lands. The collapsed-mode equivalent is a small
 * "+" button that toggles a transient form slide-down.
 */
import { FormEvent, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'

import { api, type Project } from '@/shared/api/client'
import { projectsQueryKey } from '@/shared/hooks/useProjects'

interface NewProjectInlineProps {
  /** Hide the form (default true). The component manages its own open state. */
  collapsed?: boolean
}

export function NewProjectInline({ collapsed = false }: NewProjectInlineProps): JSX.Element {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const qc = useQueryClient()

  const mutation = useMutation({
    mutationFn: (n: string) => api.createProject({ name: n }),
    onSuccess: (created: Project) => {
      // Optimistic prepend so the new project appears immediately.
      qc.setQueryData<Project[]>(projectsQueryKey, (prev) =>
        prev ? [created, ...prev] : [created],
      )
      // Re-sync from the server to pick up boards/columns and any
      // server-assigned timestamps we might have missed.
      void qc.invalidateQueries({ queryKey: projectsQueryKey })
      setName('')
      setError(null)
      setOpen(false)
    },
    onError: (e: Error) => setError(e.message),
  })

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) return
    mutation.mutate(trimmed)
  }

  if (collapsed) {
    return (
      <div className="px-2 py-1 flex justify-center">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          title="New project"
          aria-label="New project"
          className="h-7 w-7 rounded flex items-center justify-center text-slate-500 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-700 dark:hover:text-slate-300"
        >
          {open ? '×' : '+'}
        </button>
        {open && (
          <form
            onSubmit={submit}
            className="absolute left-12 ml-1 z-20 flex gap-1 p-1.5 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-950 shadow"
          >
            <input
              autoFocus
              type="text"
              placeholder="Project name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Escape') setOpen(false)
              }}
              className="px-2 py-1 text-sm w-40 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            />
            <button
              type="submit"
              disabled={mutation.isPending || !name.trim()}
              className="px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
            >
              Add
            </button>
          </form>
        )}
      </div>
    )
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="mx-2 mb-2 mt-1 w-[calc(100%-1rem)] flex items-center gap-2 px-2 py-1.5 rounded text-xs text-slate-500 hover:text-slate-800 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800 border border-dashed border-slate-300 dark:border-slate-700"
      >
        <span aria-hidden>+</span>
        <span>New project</span>
      </button>
    )
  }

  return (
    <form
      onSubmit={submit}
      className="mx-2 mb-2 mt-1 p-2 rounded border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-950"
    >
      <input
        autoFocus
        type="text"
        placeholder="Project name"
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') setOpen(false)
        }}
        className="w-full px-2 py-1.5 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
      />
      {error && <p className="mt-1 text-[11px] text-red-600">{error}</p>}
      <div className="mt-2 flex gap-2">
        <button
          type="submit"
          disabled={mutation.isPending || !name.trim()}
          className="flex-1 px-2 py-1 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-xs"
        >
          {mutation.isPending ? 'Creating…' : 'Create'}
        </button>
        <button
          type="button"
          onClick={() => {
            setOpen(false)
            setError(null)
            setName('')
          }}
          className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 text-xs"
        >
          Cancel
        </button>
      </div>
    </form>
  )
}
