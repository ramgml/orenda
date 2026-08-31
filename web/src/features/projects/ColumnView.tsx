import { FormEvent, useState } from 'react';
import { useDroppable } from '@dnd-kit/core';
import { useLocation, useNavigate } from 'react-router-dom';

import { TaskCard } from './TaskCard';
import { openTaskModal } from '@/features/tasks/TaskModal';
import { api, type Column, type Task } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Checkbox } from '@/shared/ui/checkbox';
import { Dialog, DialogContent } from '@/shared/ui/dialog';
import { Input } from '@/shared/ui/input';
import { cn } from '@/shared/util/cn';
import { queueCreateTask } from '@/shared/offline/outbox';

/**
 * One kanban column with its droppable zone and inline task creation.
 *
 * Offline-aware: when the browser is offline, the create call is queued
 * into the IndexedDB outbox and flushed by syncNow() on reconnect.
 */
export function ColumnView({
  columnId,
  name,
  projectId,
  tasks,
  color,
  wipLimit,
  status,
  onCreate,
  onColumnUpdated,
  onColumnDeleted,
  dragHandleProps,
  selectedTaskIds,
  onToggleTask,
  filterActive,
}: {
  columnId: string;
  name: string;
  projectId: string;
  tasks: Task[];
  /** Phase 27.10: saved colour from the column row. Rendered as a
   *  small dot left of the column header; null/undefined falls back
   *  to a neutral slate dot so the header layout stays stable. */
  color?: string;
  /** Saved WIP limit. Used to initialise the EditColumnModal so the
   *  field doesn't reset to "unlimited" on every reopen. */
  wipLimit?: number;
  /** Phase 30.14: machine key used as task.status on this column. */
  status?: string;
  onCreate: (title: string) => Promise<void>;
  onColumnUpdated?: (col: Column) => void;
  /**
   * Optional callback fired after the backend confirms a column
   * delete. Phase 12.6 uses it to drop the column from local state
   * without a full board refetch; the WS broadcast will also remove
   * the column from every other tab.
   */
  onColumnDeleted?: (colId: string) => void;
  /**
   * Optional dnd-kit props for the column-as-a-whole drag handle (the
   * header area). When present, the header becomes draggable so the
   * user can reorder columns. Phase 12 wires this in via
   * KanbanBoard's horizontal SortableContext; default is undefined so
   // this component stays usable standalone (tests, future board
   // layouts that don't support column reordering).
   */
  dragHandleProps?: Record<string, unknown>;
  selectedTaskIds?: ReadonlySet<string>;
  onToggleTask?: (taskId: string) => void;
  /** T106: the board's search filter is active. Gates the
   *  filtered-empty hint so a naturally empty column (no tasks at
   *  all, or after a drag moved the last card out) doesn't claim
   *  "Ничего не найдено" — the hint is ONLY about the filter hiding
   *  cards. */
  filterActive?: boolean;
}): JSX.Element {
  const { setNodeRef, isOver } = useDroppable({ id: columnId });
  const navigate = useNavigate();
  const location = useLocation();
  const [creating, setCreating] = useState(false);
  const [title, setTitle] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (!title.trim()) return;
    try {
      if (navigator.onLine) {
        await onCreate(title.trim());
      } else {
        await queueCreateTask(projectId, { title: title.trim(), column_id: columnId });
      }
      setTitle('');
      setCreating(false);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  function openTask(taskId: string): void {
    openTaskModal(navigate, location, taskId);
  }

  // Phase 30.11: a column is "at-limit" when the saved WIP limit is
  // non-null and the live card count has reached it. We visualise
  // this with a red ring so the operator sees the bottleneck
  // without opening the column header. (A column that's *over* the
  // limit isn't possible — Move rejects — but we render the
  // at-limit state anyway because dnd-kit can momentarily show the
  // optimistic position before the 422 arrives.)
  const atLimit = wipLimit != null && wipLimit > 0 && tasks.length >= wipLimit;

  return (
    <div
      ref={setNodeRef}
      className={`rounded-lg border bg-muted p-3 flex flex-col min-h-[200px] transition-colors ${
        isOver
          ? 'border-orenda-500 bg-orenda-50 dark:bg-orenda-900/20'
          : atLimit
            ? 'border-amber-400 dark:border-amber-500 ring-1 ring-amber-300/40 dark:ring-amber-500/30'
            : 'border-border'
      }`}
    >
      <div
        className="flex items-center justify-between mb-2 cursor-grab active:cursor-grabbing select-none"
        // Drag the whole column by its header. dnd-kit's useSortable
        // (wired in KanbanBoard via dragHandleProps) gives us listeners;
        // spread them on the header so the body remains free for task
        // drops. Double-click opens the same edit modal as the ⚙ button
        // — discoverability was the point of Phase 12.4.
        {...(dragHandleProps ?? {})}
        onDoubleClick={() => setEditing(true)}
      >
        <div className="flex items-center gap-2">
          {/*
            Phase 27.10: render the column's saved colour as a dot left
            of the header. Sized to coexist with the wip-limit badge
            on the right side without overlapping (header is flexed
            with `justify-between`).
          */}
          <span
            aria-hidden="true"
            data-testid="column-color-dot"
            data-column-color={color ?? ''}
            className="inline-block w-2.5 h-2.5 rounded-full border border-border/60"
            style={{ backgroundColor: color || '#94a3b8' }}
          />
          <h2 className="font-medium text-sm uppercase tracking-wide text-muted-foreground">
            {name}
          </h2>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-400">{tasks.length}</span>
          <button
            type="button"
            onClick={(e) => {
              // Clicks on ⚙ shouldn't start a column drag.
              e.stopPropagation();
              setEditing(true);
            }}
            title="Edit column"
            className="text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 text-sm leading-none"
          >
            ⚙
          </button>
        </div>
      </div>
      <ul className="space-y-2 flex-1">
        {filterActive && tasks.length === 0 && (
          <li className="text-xs text-slate-400 italic py-2 select-none" data-testid="column-empty">
            Ничего не найдено
          </li>
        )}
        {tasks.map((t) => (
          <li key={t.id} className="flex items-start gap-1 min-w-0">
            {onToggleTask && (
              <Checkbox
                aria-label={`Select ${t.title}`}
                checked={selectedTaskIds?.has(t.id) ?? false}
                onCheckedChange={() => onToggleTask(t.id)}
                onClick={(e) => e.stopPropagation()}
                className="mt-3 ml-1"
              />
            )}
            <TaskCard task={t} onOpen={openTask} />
          </li>
        ))}
      </ul>

      {creating ? (
        <form onSubmit={submit} className="mt-2 flex gap-1">
          <Input
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="New task"
            className="flex-1 text-sm"
          />
          <Button type="submit" variant="default" size="sm" className="px-2 py-1 text-xs">
            Add
          </Button>
          {error && <span className="text-xs text-red-600">{error}</span>}
        </form>
      ) : (
        <Button
          type="button"
          onClick={() => setCreating(true)}
          variant="ghost"
          size="sm"
          className="mt-2 text-xs text-slate-500 hover:text-orenda-600 self-start"
        >
          + Add task
        </Button>
      )}

      {editing && (
        <EditColumnModal
          columnId={columnId}
          initialName={name}
          initialColor={color}
          initialWipLimit={wipLimit}
          initialStatus={status}
          currentTaskCount={tasks.length}
          onClose={() => setEditing(false)}
          onSaved={(col) => {
            setEditing(false);
            onColumnUpdated?.(col);
          }}
          onDeleted={() => {
            setEditing(false);
            onColumnDeleted?.(columnId);
          }}
        />
      )}
    </div>
  );
}

interface EditColumnModalProps {
  columnId: string;
  initialName: string;
  /** Phase 27.10: saved colour from the column row. Falls back to a
   *  neutral slate so the picker still has a sane initial value for
   *  legacy rows without a colour set. */
  initialColor?: string;
  /** Saved WIP limit (number) or undefined for "no limit". The empty
   *  string in the input represents the same state. */
  initialWipLimit?: number;
  initialStatus?: string;
  /** Number of tasks currently in the column — shown as a hint before
   * the user confirms the delete. Mirrors what the server will
   * enforce (422 when non-zero). */
  currentTaskCount: number;
  onClose: () => void;
  onSaved: (col: Column) => void;
  onDeleted: () => void;
}

/** Small inline form to rename a column, change its color, set WIP
 *  limit, and — since Phase 12.6 — delete the column when it's empty.
 *  Delete is gated by a two-step confirmation because it's
 *  irreversible: a stray click shouldn't be able to wipe a column. */
function EditColumnModal({
  columnId,
  initialName,
  initialColor,
  initialWipLimit,
  initialStatus,
  currentTaskCount,
  onClose,
  onSaved,
  onDeleted,
}: EditColumnModalProps): JSX.Element {
  const [name, setName] = useState(initialName);
  // Phase 27.10: previously hardcoded to '#94a3b8' — the bug was that
  // re-opening the modal after picking a colour reset the field to
  // slate, and any subsequent Save (e.g. just to rename the column)
  // then overwrote the saved colour on the server with slate.
  const [color, setColor] = useState<string>(initialColor ?? '#94a3b8');
  const [wip, setWip] = useState<string>(
    initialWipLimit === undefined ? '' : String(initialWipLimit),
  );
  const [machineKey, setMachineKey] = useState(initialStatus ?? '');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Two-step confirm: first click arms the button ("Delete column"),
  // second click actually fires. Resets if the user changes their mind
  // or types in the form fields.
  const [confirmDelete, setConfirmDelete] = useState(false);

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (!name.trim()) {
      setError('Name is required');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      // Parse WIP. Empty input = "no limit" (server treats null as
      // clear; the API client also accepts undefined for "unchanged"
      // — here we always send an explicit value because the user is
      // editing the row).
      const wipNum = wip === '' ? null : parseInt(wip, 10);
      if (wip !== '' && Number.isNaN(wipNum)) {
        setError('WIP limit must be a number');
        setBusy(false);
        return;
      }
      // Send the colour only when the user changed it from the
      // saved value — this preserves the saved colour across a
      // rename (Phase 27.10 bug). The picker is uncontrolled from
      // the moment it opens, so we compare the live state against
      // the prop.
      const payload: Parameters<typeof api.updateColumn>[1] = {
        name: name.trim(),
        wip_limit: wipNum,
      };
      const normalizedKey = machineKey.trim().toLowerCase();
      if (normalizedKey !== '' && !/^[a-z][a-z0-9_]*$/.test(normalizedKey)) {
        setError(
          'Machine key must start with a letter and contain only lowercase letters, numbers, and underscores',
        );
        setBusy(false);
        return;
      }
      if (normalizedKey !== '' && normalizedKey !== (initialStatus ?? '')) {
        payload.status = normalizedKey;
      }
      const savedColor = initialColor ?? '';
      if (color !== savedColor) {
        payload.color = color;
      }
      const col = await api.updateColumn(columnId, payload);
      onSaved(col);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      // axios 422: extract server message if present
      setError(
        /wip_limit_too_small/.test(msg) ? 'WIP limit is below the current task count.' : msg,
      );
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(): Promise<void> {
    if (currentTaskCount > 0) {
      // Defence in depth — the server is the source of truth, but
      // failing fast here saves a round-trip and makes the UX clearer.
      setError(
        `Move the ${currentTaskCount} task${currentTaskCount === 1 ? '' : 's'} in this column to another column first.`,
      );
      setConfirmDelete(false);
      return;
    }
    if (!confirmDelete) {
      setConfirmDelete(true);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.deleteColumn(columnId);
      onDeleted();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(
        /column_not_empty/.test(msg)
          ? 'Tasks were added to this column while you were editing. Move them out first.'
          : msg,
      );
      setConfirmDelete(false);
    } finally {
      setBusy(false);
    }
  }

  // Phase 32.13 (shadcn/ui migration): the modal overlay is now a
  // Dialog primitive. Esc / click-outside / focus return are
  // handled by @radix-ui/react-dialog; we only wire `onOpenChange`
  // to the existing `onClose` callback. The two-step delete
  // confirm stays as in-component state because it has to
  // survive an Esc/click-outside — opening the Dialog in
  // `controlled` mode means Esc / click-outside invoke `onClose`
  // without losing the column from the board.
  return (
    <Dialog
      open
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <DialogContent aria-label="Edit column" className="max-w-sm gap-3 p-5 sm:rounded-lg">
        <h3 className="font-semibold">Edit column</h3>
        <form onSubmit={submit} className="space-y-2">
          <label className="block text-sm">
            Name
            <Input
              name="column-name"
              autoFocus
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setConfirmDelete(false);
              }}
              className="mt-1"
            />
          </label>
          <label className="block text-sm">
            Machine key
            <Input
              value={machineKey}
              onChange={(e) => setMachineKey(e.target.value)}
              data-testid="column-status"
              placeholder="e.g. in_review"
              className="mt-1 font-mono"
            />
            <span className="mt-1 block text-xs text-slate-500">
              Lowercase machine key; changing it updates tasks in this column.
            </span>
          </label>
          <label className="block text-sm">
            Color
            <input
              type="color"
              value={color}
              onChange={(e) => setColor(e.target.value)}
              className="mt-1 block w-12 h-8 rounded border border-border bg-transparent"
            />
          </label>
          <label className="block text-sm">
            WIP limit (empty = no limit)
            <Input
              type="number"
              min="0"
              value={wip}
              onChange={(e) => setWip(e.target.value)}
              placeholder="unlimited"
              className="mt-1"
            />
          </label>
          {error && <p className="text-xs text-red-600">{error}</p>}
          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" onClick={onClose} variant="outline" size="sm">
              Cancel
            </Button>
            <Button type="submit" disabled={busy} variant="default" size="sm">
              {busy ? 'Saving…' : 'Save'}
            </Button>
          </div>
        </form>
        <div className="border-t border-border pt-3 space-y-1">
          <p className="text-xs text-slate-500">
            {currentTaskCount === 0
              ? 'This column is empty — safe to delete.'
              : `This column holds ${currentTaskCount} task${
                  currentTaskCount === 1 ? '' : 's'
                }. Move them out first.`}
          </p>
          <Button
            type="button"
            onClick={onDelete}
            disabled={busy}
            data-testid="delete-column-button"
            variant={confirmDelete ? 'destructive' : 'outline'}
            size="sm"
            className={cn(
              'w-full',
              !confirmDelete &&
                'border-red-300 dark:border-red-800 text-red-700 dark:text-red-300 hover:bg-red-50 dark:hover:bg-red-900/30',
            )}
          >
            {confirmDelete ? `Click again to delete "${initialName}"` : 'Delete column'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
