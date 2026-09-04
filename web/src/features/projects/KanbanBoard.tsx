import { FormEvent, useEffect, useMemo, useState } from 'react';
import type { ClientRect as DndKitClientRect } from '@dnd-kit/core';
import {
  DndContext,
  DragEndEvent,
  DragOverlay,
  DragStartEvent,
  PointerSensor,
  closestCorners,
  pointerWithin,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import type { CollisionDetection } from '@dnd-kit/core';
import {
  SortableContext,
  arrayMove,
  horizontalListSortingStrategy,
  useSortable,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';

import { useAuth } from '@/features/auth/AuthContext';
import { api, type Column, type Task } from '@/shared/api/client';
import { useWebSocketTopic } from '@/shared/ws';
import { queueMoveTask } from '@/shared/offline/outbox';
import { Button } from '@/shared/ui/button';
import { Checkbox } from '@/shared/ui/checkbox';
import { Input } from '@/shared/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/ui/select';

import { ColumnView } from './ColumnView';
import { TaskCard } from './TaskCard';
import { computeReorderSuffix, computeTaskPosition, neighbourPositions } from './cardPosition';

/**
 * Kanban board for one project: drag-and-drop columns AND tasks via
 * @dnd-kit. Phase 12 added column-level reordering and the "+ Add
 * column" affordance.
 *
 * Two DnD layers share the same DndContext:
 *  - Task drag: `activeTask.id` matches a task id; drop target is a
 *    column id (the existing column droppable).
 *  - Column drag: `activeColumnId` matches a column id; drop target is
 *    another column id; we use SortableContext(horizontal) and reorder
 *    the columns array, then PATCH the moved column with a midpoint
 *    position.
 *
 * Tasks are loaded on mount and after every WS event; on drop we update
 * optimistically and POST to the move endpoint. On failure we revert.
 *
 * Child tasks (Phase 14: rows with `parent_task_id` set) live on the
 * board alongside their parents — they aren't hidden behind the
 * "ChildTasksList" UI anymore. By default they show up; the toggle at
 * the top of the board lets the user hide them when the board gets
 * crowded. Their cards carry a small "↳ child" badge so they stand
 * out from top-level work.
 *
 * Tasks whose `column_id` is NULL (legacy after migration 013, or any
 * future bug that lets one slip through) fall back to the first
 * column instead of disappearing entirely. The backend's
 * `createTaskHandler` also defaults new child tasks to the parent's
 * column so this fallback should be rare in practice.
 *
 * T106: the header search filters the board client-side (title,
 * description, T<number>, tag names — case-insensitive substring).
 * It's plain component state: it survives WS re-fetches (applied to
 * the fresh list) and resets on unmount when the user leaves the
 * page. "Select tasks" and the drop logic both operate on real task
 * ids, so filtering only changes which cards are VISIBLE — a drag
 * while filtered still moves the real task, and selection can't
 * sweep up hidden cards.
 */

/**
 * T106: does the task match the board's search query? Case-
 * insensitive substring over title, description and tag names, plus
 * a whole-token match on the human task number. Empty/whitespace
 * query matches everything, so a cleared input always restores the
 * board.
 *
 * Number matching: "t4", "4" and "#4" hit task T4 only — never
 * T40/T41/T42 (the number must be a complete token in the query).
 */
function taskMatchesQuery(task: Task, rawQuery: string): boolean {
  const q = rawQuery.trim().toLowerCase();
  if (!q) return true;
  if (task.title.toLowerCase().includes(q)) return true;
  if (task.description && task.description.toLowerCase().includes(q)) return true;
  if (task.tags?.some((tag) => tag.name.toLowerCase().includes(q))) return true;
  // T<number>: "t4" / "4" / "#4" must match task number 4 exactly —
  // not 40 or 41. Anchored regex: the number has to end right there
  // (so "t4 fix" still matches by continuing into other text), but a
  // bare "t4" never drags in T40/T41/T42.
  if (task.number > 0) {
    const re = new RegExp(`(?:^|[^0-9a-z])t?0*${task.number}(?:[^0-9a-z]|$)`);
    if (re.test(q)) return true;
  }
  return false;
}

/**
 * T160: the whole column is the drop zone. `closestCenter` (the old
 * detector) resolves `over` to the single NEAREST droppable center — in a
 * populated column that is always a card, so dropping into the column's
 * whitespace (below the last card, the padding gaps, beside the header)
 * neither highlighted the column nor registered it as the target. The
 * owner-visible symptom: only a small area of the column accepted drops.
 *
 * The fix follows the dnd-kit multi-container pattern: `pointerWithin`
 * finds every droppable under the pointer; among those, CARDS win
 * (card-over-card insert-next-to and same-column reorder keep working
 * exactly as T150/T118 built them), and the enclosing column takes the
 * drop wherever no card is under the pointer. Keyboard drags have no
 * pointer coordinates — fall back to closestCorners (rect-geometry based,
 * deterministic for the a11y sensor).
 */
const wholeColumnCollision: CollisionDetection = (args) => {
  const { pointerCoordinates, droppableContainers } = args;
  if (!pointerCoordinates) return closestCorners(args);

  const within = pointerWithin(args);
  if (within.length === 0) return [];

  const typeOf = (id: string | number): string | undefined => {
    const c = droppableContainers.find((d) => d.id === id);
    const data = c?.data.current as { type?: string } | undefined;
    return data?.type;
  };

  const cards = within.filter((c) => typeOf(c.id) === 'task');
  if (cards.length > 0) return cards;

  // Only columns under the pointer: nearest column center first.
  return within.filter((c) => typeOf(c.id) === 'column');
};

export function KanbanBoard({
  projectId,
  columns,
}: {
  projectId: string;
  columns: Column[];
}): JSX.Element {
  // useAuth is consumed only to keep the WS hook's context alive.
  useAuth();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [cols, setCols] = useState<Column[]>(columns);
  const [activeTask, setActiveTask] = useState<Task | null>(null);
  const [selectedTaskIds, setSelectedTaskIds] = useState<Set<string>>(new Set());
  const [bulkStatus, setBulkStatus] = useState('');
  const [bulkPriority, setBulkPriority] = useState('');
  const [bulkAssignee, setBulkAssignee] = useState('');
  const [bulkBusy, setBulkBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // T106: client-side task search. Local board state on purpose —
  // no URL sync (the постановка explicitly keeps routing untouched)
  // and no persistence: the query resets when the user leaves the
  // page, but survives WS-driven re-fetches because it's applied to
  // whatever the fresh task list is at render time.
  const [query, setQuery] = useState('');
  // Persist the toggle in localStorage so the user's choice survives
  // navigation. Defaults to true (show) per Phase 14 UX request.
  const [showChildren, setShowChildren] = useState<boolean>(() => {
    if (typeof window === 'undefined') return true;
    const v = window.localStorage.getItem('orenda.kanban.showChildren');
    return v === null ? true : v === 'true';
  });

  useEffect(() => {
    if (typeof window === 'undefined') return;
    window.localStorage.setItem('orenda.kanban.showChildren', String(showChildren));
  }, [showChildren]);

  // Phase 28.23: card density toggle. TaskCard already reads
  // `orenda.kanban.cardDensity` from localStorage (Phase 17), but
  // nothing wrote the flag — this is the write side. Persisted
  // synchronously in onChange so the same render pass that flips
  // state also flips any other card surface reading localStorage.
  const [compactCards, setCompactCards] = useState<boolean>(
    () =>
      typeof window !== 'undefined' &&
      window.localStorage.getItem('orenda.kanban.cardDensity') === 'compact',
  );

  function onToggleCompact(next: boolean): void {
    if (typeof window !== 'undefined') {
      window.localStorage.setItem('orenda.kanban.cardDensity', next ? 'compact' : 'detailed');
    }
    setCompactCards(next);
  }

  // Keep local cols in sync if the parent re-fetches the board.
  useEffect(() => {
    setCols(columns);
  }, [columns]);

  useEffect(() => {
    setSelectedTaskIds((current) => {
      const available = new Set(tasks.map((task) => task.id));
      return new Set(Array.from(current).filter((id) => available.has(id)));
    });
  }, [tasks]);

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }));

  async function load(): Promise<void> {
    try {
      setTasks(await api.listProjectTasks(projectId));
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId]);

  // Re-fetch on every task/column event. Simple, correct, and
  // acceptable at Phase 2/12 scale (one owner, one board, <1k tasks).
  useWebSocketTopic('tasks', () => {
    load();
  });

  function toggleTaskSelection(taskId: string): void {
    setSelectedTaskIds((current) => {
      const next = new Set(current);
      if (next.has(taskId)) next.delete(taskId);
      else next.add(taskId);
      return next;
    });
  }

  async function applyBulkEdit(): Promise<void> {
    if (selectedTaskIds.size === 0) return;
    const patch: Partial<Task> = {};
    if (bulkStatus) patch.status = bulkStatus;
    if (bulkPriority) patch.priority = bulkPriority as Task['priority'];
    if (bulkAssignee === 'unassigned') {
      patch.assignee_type = 'unassigned';
      patch.assignee_id = '';
    } else if (bulkAssignee) {
      const [type, id] = bulkAssignee.split(':');
      patch.assignee_type = type;
      patch.assignee_id = id;
    }
    if (Object.keys(patch).length === 0) {
      setError('Choose at least one field to update');
      return;
    }
    setBulkBusy(true);
    try {
      const result = await api.bulkPatchTasks({
        task_ids: Array.from(selectedTaskIds),
        patch,
      });
      setTasks((current) =>
        current.map((task) => result.tasks.find((updated) => updated.id === task.id) ?? task),
      );
      if (result.errors && Object.keys(result.errors).length > 0) {
        setError(`Some tasks could not be updated: ${Object.keys(result.errors).join(', ')}`);
      } else {
        setError(null);
      }
      setSelectedTaskIds(new Set());
      setBulkStatus('');
      setBulkPriority('');
      setBulkAssignee('');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBulkBusy(false);
    }
  }

  function isColumnId(id: string): boolean {
    return cols.some((c) => c.id === id);
  }

  function onDragStart(ev: DragStartEvent): void {
    if (isColumnId(String(ev.active.id))) return; // column drag handled in onDragEnd
    const t = tasks.find((x) => x.id === ev.active.id);
    if (t) setActiveTask(t);
  }

  // T150: drop side for card-over-card moves, from the live pointer
  // position: dnd-kit nulls active.rect.current.translated before
  // onDragEnd fires (verified in @dnd-kit/core source — the same is
  // true in the browser, not only in tests), so side must be
  // computed from the end event itself. activatorEvent carries the
  // pointerdown coordinates and delta the accumulated pointer
  // travel, so activatorY + delta.y is the pointer's drop Y in both
  // real drags and synthetic tests. Comparing it to the hovered
  // card's center decides before/after; without pointer data
  // (keyboard sensor) we slot after the target.
  function dropSideAfter(ev: DragEndEvent, overRect: DndKitClientRect | null): boolean {
    const activator = ev.activatorEvent;
    const fromY =
      activator && typeof activator === 'object' && 'clientY' in activator
        ? (activator as PointerEvent).clientY
        : null;
    if (fromY == null || !overRect || overRect.height <= 0) return true;
    return fromY + ev.delta.y >= overRect.top + overRect.height / 2;
  }

  async function onDragEnd(ev: DragEndEvent): Promise<void> {
    setActiveTask(null);
    const activeId = String(ev.active.id);
    const overId = ev.over ? String(ev.over.id) : null;
    if (!overId) return;

    // Column reorder: both endpoints are columns.
    if (isColumnId(activeId) && isColumnId(overId) && activeId !== overId) {
      await reorderColumns(activeId, overId);
      return;
    }

    // T118: task drag. `over` is a column id (cross-column move —
    // the column droppable) or another card id (same-column reorder
    // — cards are SortableCards since T118, so dropping ON a card
    // reports that card, not the column).
    if (isColumnId(overId)) {
      await moveTaskToColumn(activeId, overId);
      return;
    }

    // T150: cross-column card-over-card drop. Since T118 every card is
    // a SortableCard, closestCenter reports the CARD under the pointer
    // as `over` — including a card in ANOTHER column, which used to
    // bail out silently right here (the regression this branch fixes).
    // Treat it as a move into overTask's column, positioned next to
    // the target; the primary request goes through moveTaskToColumn so
    // the WIP-limit revert + toast stay in exactly one place.
    const current = tasks.find((t) => t.id === activeId);
    const overTask = tasks.find((t) => t.id === overId);
    if (!current || !overTask || activeId === overId) return;

    if (current.column_id !== overTask.column_id) {
      const targetColumnId = overTask.column_id;
      if (!targetColumnId) return;
      const targetTasks = tasks
        .filter((t) => t.column_id === targetColumnId)
        .sort((a, b) => a.position - b.position);
      const toIdx = targetTasks.findIndex((t) => t.id === overId);
      if (toIdx < 0) return;

      // Before/after the target by drop side (see dropSideAfter):
      // the pointer's drop Y vs the hovered card's center. Without
      // pointer data (keyboard sensor) this slots after the target —
      // matching the pre-T118 append path.
      const afterTarget = dropSideAfter(ev, ev.over?.rect ?? null);
      const insertIdx = afterTarget ? toIdx + 1 : toIdx;

      const orderedIds = [...targetTasks.map((t) => t.id)];
      orderedIds.splice(insertIdx, 0, activeId);
      const positions = new Map(targetTasks.map((t) => [t.id, t.position]));
      const { before, after } = neighbourPositions(orderedIds, positions, insertIdx);
      const position =
        computeTaskPosition(before, after).position ?? targetTasks[insertIdx]?.position ?? 0;
      positions.set(activeId, position);
      // Front slot (index 0) needs no rebalance: the midpoint sits
      // strictly below every existing position (after - GAP), while
      // the suffix helper's 0 baseline would only produce spurious
      // bumps. Deeper slots mirror the same-column reorder: legacy
      // ties (quick-created cards sharing one position) cannot
      // express "between" on the server, so the tied suffix — the
      // moved card first — gets strictly increasing positions.
      const suffix =
        insertIdx === 0 ? null : computeReorderSuffix(orderedIds, positions, insertIdx);
      const movedPos = suffix?.get(activeId) ?? position;
      const others = suffix ? [...suffix.entries()].filter(([id]) => id !== activeId) : [];

      // Mirror the same-column reorder: suffix bumps fire best-effort
      // after the primary move, and only when it actually landed — a
      // WIP-reverted drop must not shift the target column's cards.
      if (await moveTaskToColumn(activeId, targetColumnId, movedPos)) {
        for (const [id, pos] of others) {
          try {
            if (typeof navigator !== 'undefined' && !navigator.onLine) {
              await queueMoveTask(id, targetColumnId, pos);
            } else {
              await api.moveTask(id, targetColumnId, pos);
            }
          } catch {
            // Best-effort: a failed suffix bump re-syncs on next WS
            // refetch; the moved card itself is already authoritative.
          }
        }
      }
      return;
    }

    // Same-column reorder: active and over are both cards in the same
    // column (card-over-card since T118). A stale id must not corrupt
    // positions.
    const columnTasks = tasks
      .filter((t) => t.column_id === current.column_id)
      .sort((a, b) => a.position - b.position);
    const fromIdx = columnTasks.findIndex((t) => t.id === activeId);
    const toIdx = columnTasks.findIndex((t) => t.id === overId);
    if (fromIdx < 0 || toIdx < 0 || fromIdx === toIdx) return;

    const reordered = arrayMove(columnTasks, fromIdx, toIdx);
    const { before, after } = neighbourPositions(
      reordered.map((t) => t.id),
      new Map(reordered.map((t) => [t.id, t.position])),
      toIdx,
    );
    const position = computeTaskPosition(before, after).position ?? reordered[toIdx]?.position ?? 0;

    // Legacy columns can hold position ties (quick-created cards all
    // share e.g. 0). A tie midpoint cannot express "between" on the
    // server (ORDER BY position, created_at), so rebalance the tied
    // suffix — the moved card first — and fire one move per bumped
    // card. The primary request is the moved card itself; the rest
    // follow best-effort so the visible order survives reload.
    const suffix = computeReorderSuffix(
      reordered.map((t) => t.id),
      new Map(reordered.map((t) => [t.id, t.position])),
      toIdx,
    );
    const movedPos = suffix.get(activeId) ?? position;
    const others = [...suffix.entries()].filter(([id]) => id !== activeId);

    const prev = tasks;
    setTasks((cur) =>
      cur.map((t) => (suffix.has(t.id) ? { ...t, position: suffix.get(t.id)! } : t)),
    );

    try {
      const columnId = current.column_id ?? '';
      if (!columnId) return;
      if (typeof navigator !== 'undefined' && !navigator.onLine) {
        await queueMoveTask(activeId, columnId, movedPos);
      } else {
        await api.moveTask(activeId, columnId, movedPos);
      }
      for (const [id, pos] of others) {
        try {
          if (typeof navigator !== 'undefined' && !navigator.onLine) {
            await queueMoveTask(id, columnId, pos);
          } else {
            await api.moveTask(id, columnId, pos);
          }
        } catch {
          // Best-effort: a failed suffix bump re-syncs on next WS
          // refetch; the moved card itself is already authoritative.
        }
      }
    } catch {
      setTasks(prev);
    }
  }

  /**
   * T118: cross-column move extracted from onDragEnd so the
   * card-over-column drop path and the WIP-limit error handling stay
   * in one place. Behaviour identical to pre-T118: optimistic update,
   * outbox when offline, revert + toast on failure.
   *
   * T150: accepts an optional position (card-over-card drops in
   * another column slot the card next to its target) and returns
   * whether the move landed, so callers can skip follow-up work when
   * the optimistic update was reverted.
   */
  async function moveTaskToColumn(
    activeId: string,
    targetColumnId: string,
    position?: number,
  ): Promise<boolean> {
    const current = tasks.find((t) => t.id === activeId);
    if (!current || current.column_id === targetColumnId) return false;

    const prev = tasks;
    setTasks((cur) =>
      cur.map((t) =>
        t.id === activeId
          ? { ...t, column_id: targetColumnId, ...(position != null ? { position } : {}) }
          : t,
      ),
    );

    try {
      // Phase Wave 4 PR 2: route the move through the offline
      // outbox when the client is disconnected, so a dnd-while-
      // offline lands the position on the server once connectivity
      // returns. Online path is the same as before (axios call
      // catches the error and the optimistic update stays in place).
      if (typeof navigator !== 'undefined' && !navigator.onLine) {
        await queueMoveTask(activeId, targetColumnId, position);
      } else {
        await api.moveTask(activeId, targetColumnId, position);
      }
      return true;
    } catch (e) {
      // Phase 30.11: surface the WIP limit as a specific toast with
      // "N из M" rather than the raw backend message. The server
      // returns error: "wip_limit" (422) — we recognise it and
      // substitute the column-specific counter from local state so
      // the operator knows which limit they hit.
      const raw = e instanceof Error ? e.message : String(e);
      const targetCol = cols.find((c) => c.id === targetColumnId);
      if (/wip[_-]?limit/i.test(raw) && targetCol?.wip_limit != null) {
        const cardsInTarget = tasks.filter(
          (t) => t.column_id === targetColumnId && t.id !== activeId,
        ).length;
        const limit = targetCol.wip_limit;
        setError(
          `Column "${targetCol.name}" is at WIP limit (${cardsInTarget} of ${limit}). Pick another column or finish a task first.`,
        );
      } else {
        setError(raw);
      }
      setTasks(prev);
      return false;
    }
  }

  /**
   * Reorder columns locally + PATCH the moved column with a position
   * computed from its new neighbours. On PATCH failure we revert by
   * re-fetching the board (the WS broadcast on column update would do
   * it anyway, but doing it inline makes the failure feel immediate).
   */
  async function reorderColumns(activeId: string, overId: string): Promise<void> {
    const fromIdx = cols.findIndex((c) => c.id === activeId);
    const toIdx = cols.findIndex((c) => c.id === overId);
    if (fromIdx < 0 || toIdx < 0 || fromIdx === toIdx) return;

    const reordered = arrayMove(cols, fromIdx, toIdx);
    const prev = cols;
    setCols(reordered);

    // Midpoint between the columns now surrounding the moved one.
    const before = toIdx > 0 ? reordered[toIdx - 1].position : null;
    const after = toIdx < reordered.length - 1 ? reordered[toIdx + 1].position : null;
    let newPos: number;
    if (before != null && after != null) {
      newPos = (before + after) / 2;
    } else if (before != null) {
      // Moved to the very end.
      newPos = before + 1024;
    } else if (after != null) {
      // Moved to the very front.
      newPos = after - 1024;
    } else {
      // Single-column board — position is moot.
      newPos = 1024;
    }

    try {
      const updated = await api.updateColumn(activeId, { position: newPos });
      setCols((cur) => cur.map((c) => (c.id === updated.id ? updated : c)));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setCols(prev);
    }
  }

  // T106: the search filter — applied AFTER the child-task toggle so
  // the two are independent (a match inside a hidden child never
  // reveals it; the child toggle governs visibility first). Both the
  // column buckets and the "Select tasks" bulk operation derive from
  // this list, so hidden tasks can't be swept into a bulk edit.
  const filteredTasks = useMemo(
    () => tasks.filter((t) => taskMatchesQuery(t, query)),
    [tasks, query],
  );

  // T106: "the user is actively filtering" — a non-empty query. Gates
  // the per-column filtered-empty hint: a naturally empty column
  // (nothing ever in it, or the last card dragged out) must not claim
  // "Ничего не найдено" once the query is cleared.
  const filterActive = query.trim() !== '';

  // Build per-column buckets from the FILTERED task list (T106):
  // non-matching cards disappear from their columns, columns left
  // with no matching cards show their empty state. Legacy tasks
  // without a column still fall back to the first column — they go
  // through the same filter, so they're findable like any other card.
  const tasksByCol = useMemo(() => {
    const map = new Map<string, Task[]>();
    const fallback = cols[0]?.id;
    const visible = showChildren ? filteredTasks : filteredTasks.filter((t) => !t.parent_task_id);
    for (const t of visible) {
      const k = t.column_id ?? fallback ?? '';
      if (!k) continue;
      const list = map.get(k) ?? [];
      list.push(t);
      map.set(k, list);
    }
    return map;
  }, [filteredTasks, cols, showChildren]);

  const childCount = useMemo(() => tasks.filter((t) => !!t.parent_task_id).length, [tasks]);

  return (
    <div className="space-y-3">
      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <label className="flex items-center gap-2 text-xs text-slate-500 cursor-pointer">
            <Checkbox checked={showChildren} onCheckedChange={(v) => setShowChildren(v === true)} />
            <span>
              Show child tasks <span className="text-slate-400">({childCount})</span>
            </span>
          </label>
          <Input
            aria-label="Search tasks"
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Поиск задач…"
            className="h-8 w-48 text-xs"
          />
          {query.trim() !== '' && (
            <span className="text-xs text-slate-500 whitespace-nowrap">
              {filteredTasks.length} найдено
            </span>
          )}
          <Button
            type="button"
            onClick={() =>
              setSelectedTaskIds((current) =>
                current.size > 0 ? new Set() : new Set(filteredTasks.map((task) => task.id)),
              )
            }
            variant="outline"
            size="sm"
            className="text-xs"
          >
            {selectedTaskIds.size > 0 ? 'Clear selection' : 'Select tasks'}
          </Button>
          <label className="flex items-center gap-2 text-xs text-slate-500 cursor-pointer">
            <Checkbox checked={compactCards} onCheckedChange={(v) => onToggleCompact(v === true)} />
            <span>Compact cards</span>
          </label>
        </div>
      </div>
      <DndContext
        sensors={sensors}
        collisionDetection={wholeColumnCollision}
        onDragStart={onDragStart}
        onDragEnd={onDragEnd}
      >
        <SortableContext items={cols.map((c) => c.id)} strategy={horizontalListSortingStrategy}>
          <div className="grid grid-cols-1 md:grid-cols-[repeat(5,minmax(0,1fr))] gap-3">
            {cols.map((col) => (
              <SortableColumnView
                key={col.id}
                column={col}
                projectId={projectId}
                tasks={tasksByCol.get(col.id) ?? []}
                onCreate={async (title) => {
                  const t = await api.createTask(projectId, { title, column_id: col.id });
                  setTasks((cur) => [...cur, t]);
                }}
                onColumnUpdated={(updated) =>
                  setCols((cur) => cur.map((c) => (c.id === updated.id ? updated : c)))
                }
                onColumnDeleted={(colId) => {
                  // Phase 12.6: drop the column locally so the UI
                  // updates immediately; the WS broadcast does the
                  // same on every other tab.
                  setCols((cur) => cur.filter((c) => c.id !== colId));
                  setTasks((cur) => cur.filter((t) => t.column_id !== colId));
                }}
                selectedTaskIds={selectedTaskIds}
                onToggleTask={toggleTaskSelection}
                filterActive={filterActive}
              />
            ))}
            <AddColumnTile projectId={projectId} onCreated={(c) => setCols((cur) => [...cur, c])} />
          </div>
        </SortableContext>
        <DragOverlay>{activeTask ? <TaskCard task={activeTask} /> : null}</DragOverlay>
      </DndContext>
      {selectedTaskIds.size > 0 && (
        <div className="sticky bottom-3 z-20 rounded-lg border border-orenda-300 bg-card shadow-lg p-3 flex flex-wrap items-center gap-2">
          <strong className="text-sm">{selectedTaskIds.size} selected</strong>
          <Select value={bulkStatus} onValueChange={setBulkStatus}>
            <SelectTrigger
              aria-label="Bulk status"
              className="w-auto rounded border px-2 py-1 text-sm"
            >
              <SelectValue placeholder="Status…" />
            </SelectTrigger>
            <SelectContent>
              {cols
                .filter((column) => column.status)
                .map((column) => (
                  <SelectItem key={column.id} value={column.status!}>
                    {column.name}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
          <Select value={bulkPriority} onValueChange={setBulkPriority}>
            <SelectTrigger
              aria-label="Bulk priority"
              className="w-auto rounded border px-2 py-1 text-sm"
            >
              <SelectValue placeholder="Priority…" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="low">Low</SelectItem>
              <SelectItem value="medium">Medium</SelectItem>
              <SelectItem value="high">High</SelectItem>
              <SelectItem value="urgent">Urgent</SelectItem>
            </SelectContent>
          </Select>
          <Input
            aria-label="Bulk assignee"
            value={bulkAssignee}
            onChange={(e) => setBulkAssignee(e.target.value)}
            placeholder="assignee type:id"
            className="w-40 text-sm"
          />
          <Button
            type="button"
            onClick={applyBulkEdit}
            disabled={bulkBusy}
            variant="default"
            size="sm"
          >
            {bulkBusy ? 'Applying…' : 'Apply'}
          </Button>
        </div>
      )}
    </div>
  );
}

/**
 * Sortable wrapper around ColumnView that provides dnd-kit handle
 * listeners via useSortable. The column itself remains a useDroppable
 * target (for task drops) — useSortable's setNodeRef composes with
 * useDroppable's setNodeRef via forwarding ref. We pass the listeners
 * down to ColumnView's header via dragHandleProps.
 */
function SortableColumnView({
  column,
  projectId,
  tasks,
  onCreate,
  onColumnUpdated,
  onColumnDeleted,
  selectedTaskIds,
  onToggleTask,
  filterActive,
}: {
  column: Column;
  projectId: string;
  tasks: Task[];
  onCreate: (title: string) => Promise<void>;
  onColumnUpdated: (col: Column) => void;
  onColumnDeleted: (colId: string) => void;
  selectedTaskIds: ReadonlySet<string>;
  onToggleTask: (taskId: string) => void;
  filterActive: boolean;
}): JSX.Element {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: column.id,
    data: { type: 'column' },
  });
  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  };
  return (
    <div ref={setNodeRef} style={style} {...attributes} className="min-w-0">
      <ColumnView
        columnId={column.id}
        projectId={projectId}
        name={column.name}
        tasks={tasks}
        // Phase 27.10: pipe the saved colour + WIP into ColumnView
        // so the header renders the dot and the edit modal opens
        // with the persisted values (rename then Save no longer
        // wipes the colour).
        color={column.color}
        wipLimit={column.wip_limit}
        status={column.status}
        selectedTaskIds={selectedTaskIds}
        onToggleTask={onToggleTask}
        filterActive={filterActive}
        onCreate={onCreate}
        onColumnUpdated={onColumnUpdated}
        onColumnDeleted={onColumnDeleted}
        dragHandleProps={listeners}
      />
    </div>
  );
}

/**
 * "+ Add column" affordance at the end of the board. Inline form: name +
 * optional color, submit calls api.createColumn. Optimistic append on
 * success; on failure shows the error inline.
 */
function AddColumnTile({
  projectId,
  onCreated,
}: {
  projectId: string;
  onCreated: (col: Column) => void;
}): JSX.Element {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [status, setStatus] = useState('');
  const [color, setColor] = useState('#94a3b8');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError('Name is required');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const machineKey = status.trim().toLowerCase();
      if (machineKey !== '' && !/^[a-z][a-z0-9_]*$/.test(machineKey)) {
        setError(
          'Machine key must start with a letter and contain only lowercase letters, numbers, and underscores',
        );
        setBusy(false);
        return;
      }
      const col = await api.createColumn(projectId, {
        name: trimmed,
        color,
        ...(machineKey ? { status: machineKey } : {}),
      });
      onCreated(col);
      setName('');
      setColor('#94a3b8');
      setStatus('');
      setOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        data-testid="add-column-tile"
        className="rounded-lg border border-dashed border-border bg-transparent hover:bg-slate-50 dark:hover:bg-slate-900 text-xs text-slate-500 hover:text-orenda-600 min-h-[200px] flex items-center justify-center"
      >
        + Add column
      </button>
    );
  }

  return (
    <form
      onSubmit={submit}
      data-testid="add-column-form"
      className="rounded-lg border border-border bg-muted p-3 flex flex-col gap-2 min-h-[200px]"
    >
      <Input
        autoFocus
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Column name"
        className="text-sm"
      />
      <label className="flex items-center gap-2 text-xs text-slate-500">
        Color
        <input
          type="color"
          value={color}
          onChange={(e) => setColor(e.target.value)}
          className="w-10 h-6 rounded border border-border bg-transparent"
        />
      </label>
      <label className="text-xs text-slate-500">
        Machine key (optional)
        <Input
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          data-testid="add-column-status"
          placeholder="auto from name"
          className="mt-1 font-mono text-sm"
        />
      </label>
      {error && <p className="text-xs text-red-600">{error}</p>}
      <div className="flex gap-1 mt-auto">
        <Button
          type="submit"
          disabled={busy}
          variant="default"
          size="sm"
          className="flex-1 px-2 py-1 text-xs"
        >
          {busy ? 'Adding…' : 'Add'}
        </Button>
        <Button
          type="button"
          onClick={() => {
            setOpen(false);
            setError(null);
            setName('');
            setStatus('');
          }}
          variant="outline"
          size="sm"
          className="px-2 py-1 text-xs"
        >
          Cancel
        </Button>
      </div>
    </form>
  );
}
