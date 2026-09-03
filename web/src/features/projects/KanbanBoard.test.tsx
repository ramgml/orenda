// @vitest-environment jsdom
/**
 * KanbanBoard tests (Phase 28.23).
 *
 * Pins the card-density toggle: TaskCard has read
 * `orenda.kanban.cardDensity` from localStorage since Phase 17, but
 * nothing wrote the flag — the board's toolbar now exposes a
 * "Compact cards" checkbox next to "Show child tasks".
 *
 * What's pinned:
 *   - The checkbox persists the flag to localStorage.
 *   - Toggling it switches card rendering density in the same pass
 *     (detailed cards show the due-badge row; compact ones don't).
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Column, Task } from '@/shared/api/client';
import { KanbanBoard } from '@/features/projects/KanbanBoard';

// jsdom doesn't implement scrollIntoView or scrolling APIs that Radix
// Select needs. Stub them globally before any component renders.
Element.prototype.scrollIntoView = vi.fn();
Element.prototype.scrollTo = vi.fn();
Object.defineProperty(Element.prototype, 'scrollTop', { value: 0, writable: true });
Object.defineProperty(Element.prototype, 'scrollHeight', { value: 0, writable: true });

// Radix UI components (Checkbox, Dialog, Select) use
// @radix-ui/react-use-size which needs ResizeObserver in jsdom.
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

const { stubHttp } = vi.hoisted(() => ({
  stubHttp: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
    interceptors: { response: { use: vi.fn() } },
  },
}));

vi.mock('axios', async (importOriginal) => {
  const actual = await importOriginal<typeof import('axios')>();
  return {
    ...actual,
    default: { ...actual.default, create: vi.fn(() => stubHttp) },
  };
});

// AuthProvider is NOT mounted; KanbanBoard calls useAuth() only to keep
// the WS hook context alive, so mock the context to a stable value.
vi.mock('@/features/auth/AuthContext', () => ({
  useAuth: () => ({ user: null, status: 'anonymous' }),
  AuthProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

const DENSITY_KEY = 'orenda.kanban.cardDensity';

function makeColumn(over: Partial<Column> = {}): Column {
  return { id: 'col-1', board_id: 'b-1', name: 'Todo', position: 1, ...over };
}

function makeTask(over: Partial<Task> = {}): Task {
  return {
    id: 'task-1',
    number: 1,
    project_id: 'p1',
    column_id: 'col-1',
    title: 'Dense task',
    status: 'todo',
    priority: 'medium',
    awaiting: 'none',
    time_spent_s: 0,
    position: 0,
    created_at: '2026-08-16T00:00:00Z',
    updated_at: '2026-08-16T00:00:00Z',
    color: '',
    tags: [],
    // A due date gives the detailed card a badge the compact card hides.
    due_at: '2026-08-20T00:00:00Z',
    ...over,
  };
}

function mountBoard(tasks: Task[]) {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/agents') return Promise.resolve({ data: { agents: [] } });
    if (url === '/api/v1/projects/p1/tasks') return Promise.resolve({ data: { tasks } });
    return Promise.reject(new Error(`unexpected GET ${url}`));
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <KanbanBoard projectId="p1" columns={[makeColumn()]} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
});

afterEach(() => {
  cleanup();
  window.localStorage.clear();
});

describe('KanbanBoard — card density toggle', () => {
  it('toggling "Compact cards" switches card density and persists the flag', async () => {
    mountBoard([makeTask()]);

    // Detailed by default: the due badge is visible.
    expect(await screen.findByTestId('due-badge')).toBeTruthy();
    expect(window.localStorage.getItem(DENSITY_KEY)).toBeNull();

    const toggle = screen.getByRole('checkbox', { name: /compact cards/i });
    fireEvent.click(toggle);

    await waitFor(() => {
      expect(window.localStorage.getItem(DENSITY_KEY)).toBe('compact');
      expect(screen.queryByTestId('due-badge')).toBeNull();
    });

    // Back to detailed.
    fireEvent.click(screen.getByRole('checkbox', { name: /compact cards/i }));
    await waitFor(() => {
      expect(window.localStorage.getItem(DENSITY_KEY)).toBe('detailed');
      expect(screen.getByTestId('due-badge')).toBeTruthy();
    });
  });

  it('honours a persisted compact flag on mount', async () => {
    window.localStorage.setItem(DENSITY_KEY, 'compact');
    mountBoard([makeTask()]);

    // Card renders (title present) but the detailed badge row is hidden.
    expect(await screen.findByText('Dense task')).toBeTruthy();
    expect(screen.queryByTestId('due-badge')).toBeNull();
    expect(
      screen.getByRole('checkbox', { name: /compact cards/i }).getAttribute('data-state'),
    ).toBe('checked');
  });

  it('selects a task and applies a bulk priority update', async () => {
    const task = makeTask();
    stubHttp.post.mockResolvedValue({ data: { tasks: [{ ...task, priority: 'urgent' }] } });
    mountBoard([task]);

    fireEvent.click(await screen.findByRole('checkbox', { name: /select dense task/i }));
    // Radix Select: click trigger to open, then click the option.
    // The bulk action bar renders after selection — wait for the combobox.
    const trigger = await screen.findByRole('combobox', { name: /bulk priority/i });
    fireEvent.click(trigger);
    // Radix Select renders options in a portal after the trigger click.
    // Wait for the option to appear in the DOM.
    await waitFor(() => {
      expect(screen.getAllByRole('option').length).toBeGreaterThan(0);
    });
    fireEvent.click(screen.getByRole('option', { name: 'Urgent' }));
    fireEvent.click(screen.getByRole('button', { name: 'Apply' }));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/tasks/bulk-edit', {
        task_ids: ['task-1'],
        patch: { priority: 'urgent' },
      });
      expect(screen.queryByText('1 selected')).toBeNull();
    });
  });
});

describe('KanbanBoard — T106 board task search', () => {
  function type(query: string): void {
    const input = screen.getByLabelText('Search tasks');
    fireEvent.change(input, { target: { value: query } });
  }

  it('empty query shows every card', async () => {
    mountBoard([makeTask({ title: 'Alpha' }), makeTask({ id: 'task-2', title: 'Beta' })]);
    expect(await screen.findByText('Alpha')).toBeTruthy();
    expect(screen.getByText('Beta')).toBeTruthy();
    expect(screen.queryByText(/\d+ найдено/)).toBeNull();
  });

  it('filters by title substring, case-insensitively', async () => {
    mountBoard([makeTask({ title: 'Alpha' }), makeTask({ id: 'task-2', title: 'Beta' })]);
    await screen.findByText('Alpha');
    type('bet');
    expect(screen.queryByText('Alpha')).toBeNull();
    expect(screen.getByText('Beta')).toBeTruthy();
  });

  it('filters by T<number> (full token, not a digit prefix match)', async () => {
    mountBoard([
      makeTask({ id: 'task-4', number: 4, title: 'Fix login' }),
      makeTask({ id: 'task-40', number: 40, title: 'Add export' }),
    ]);
    await screen.findByText('Fix login');
    type('t4');
    // "t4" matches the full token t4 but not t40 — only T4 stays.
    expect(screen.getByText('Fix login')).toBeTruthy();
    expect(screen.queryByText('Add export')).toBeNull();
  });

  it('filters by tag name', async () => {
    const tagged = makeTask({
      id: 'task-3',
      title: 'Untitledbecause',
      tags: [{ id: 'tag-1', name: 'infra' }],
    });
    const other = makeTask({ id: 'task-5', title: 'Something else', tags: [] });
    mountBoard([tagged, other]);
    await screen.findByText('Untitledbecause');
    type('infra');
    expect(screen.getByText('Untitledbecause')).toBeTruthy();
    expect(screen.queryByText('Something else')).toBeNull();
  });

  it('filters by description substring', async () => {
    mountBoard([
      makeTask({ id: 'task-6', title: 'One', description: 'migrate the database' }),
      makeTask({ id: 'task-7', title: 'Two' }),
    ]);
    await screen.findByText('One');
    type('database');
    expect(screen.getByText('One')).toBeTruthy();
    expect(screen.queryByText('Two')).toBeNull();
  });

  it('shows a result counter and empty-column state when nothing matches', async () => {
    mountBoard([makeTask({ title: 'Alpha' })]);
    await screen.findByText('Alpha');
    type('zzz-no-match');
    expect(screen.getByText('0 найдено')).toBeTruthy();
    expect(screen.getAllByTestId('column-empty').length).toBeGreaterThan(0);
    expect(screen.queryByText('Alpha')).toBeNull();
  });

  it('clearing the query restores the full board', async () => {
    mountBoard([makeTask({ title: 'Alpha' }), makeTask({ id: 'task-2', title: 'Beta' })]);
    await screen.findByText('Alpha');
    type('zzz');
    expect(screen.queryByText('Beta')).toBeNull();
    type('');
    expect(screen.getByText('Alpha')).toBeTruthy();
    expect(screen.getByText('Beta')).toBeTruthy();
  });

  it('"Select tasks" selects only visible (matching) tasks', async () => {
    const a = makeTask({ title: 'Alpha' });
    const b = makeTask({ id: 'task-2', title: 'Beta' });
    mountBoard([a, b]);
    await screen.findByText('Alpha');
    type('alpha');
    fireEvent.click(screen.getByRole('button', { name: 'Select tasks' }));
    // Only Alpha is on the board, so the bulk bar shows 1 selected.
    expect(await screen.findByText('1 selected')).toBeTruthy();
    // The hidden Beta has no checkbox on the board at all.
    expect(screen.queryByRole('checkbox', { name: /select beta/i })).toBeNull();
  });

  it('naturally empty column does NOT show the hint while the filter is active', async () => {
    // Column "Empty" never had tasks; column "Todo" holds the only task.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/agents') return Promise.resolve({ data: { agents: [] } });
      if (url === '/api/v1/projects/p1/tasks')
        return Promise.resolve({ data: { tasks: [makeTask({ title: 'Alpha' })] } });
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <KanbanBoard
            projectId="p1"
            columns={[makeColumn(), makeColumn({ id: 'col-empty', name: 'Empty', position: 2 })]}
          />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await screen.findByText('Alpha');

    // No filter → no hint anywhere (there are no matching-hiding going on).
    expect(screen.queryByTestId('column-empty')).toBeNull();

    // Filtering to a non-match hides Alpha's column content AND marks
    // the naturally empty column with the hint.
    type('zzz');
    expect(screen.getAllByTestId('column-empty').length).toBe(2);

    // Clearing the filter removes the hint from both columns.
    type('');
    expect(screen.queryByTestId('column-empty')).toBeNull();
  });

  it('legacy task without column_id is found by search and shown in the first column', async () => {
    const legacy = makeTask({
      id: 'task-legacy',
      number: 99,
      title: 'Legacy orphan',
      column_id: undefined,
    });
    mountBoard([makeTask({ title: 'Alpha' }), legacy]);
    await screen.findByText('Alpha');

    // The orphan renders in the first column next to regular tasks.
    expect(screen.getByText('Legacy orphan')).toBeTruthy();

    // Search finds it by title and by T-number.
    type('orphan');
    expect(screen.getByText('Legacy orphan')).toBeTruthy();
    expect(screen.queryByText('Alpha')).toBeNull();
    type('t99');
    expect(screen.getByText('Legacy orphan')).toBeTruthy();
    type('');
    expect(screen.getByText('Legacy orphan')).toBeTruthy();
  });

  it('filter survives a WS re-fetch and applies to the fresh task list', async () => {
    mountBoard([makeTask({ title: 'Alpha' })]);
    await screen.findByText('Alpha');
    type('alpha');
    expect(screen.getByText('Alpha')).toBeTruthy();

    // The 'tasks' WS topic re-fetches; the fresh list brings a new task.
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/agents') return Promise.resolve({ data: { agents: [] } });
      if (url === '/api/v1/projects/p1/tasks')
        return Promise.resolve({
          data: {
            tasks: [
              makeTask({ title: 'Alpha' }),
              makeTask({ id: 'task-2', number: 2, title: 'Beta fresh' }),
            ],
          },
        });
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });

    // Re-trigger load() through the real WS dispatch path: the
    // component's listener is registered on the singleton for topic
    // 'tasks'; drive the underlying socket's onmessage exactly as a
    // server frame would (jsdom's WebSocket never connects, but
    // openSocket assigns onmessage synchronously).
    const { wsClient } = await import('@/shared/ws');
    wsClient.connect();
    const sock = (wsClient as unknown as { ws?: { onmessage?: (ev: { data: string }) => void } })
      .ws;
    sock?.onmessage?.({ data: JSON.stringify({ topic: 'tasks', body: {} }) });
    wsClient.disconnect();

    // Beta arrived but stays hidden — the query is still active.
    await waitFor(() => {
      expect(screen.queryByText('Beta fresh')).toBeNull();
      expect(screen.getByText('Alpha')).toBeTruthy();
    });

    // Clearing reveals it — the filter applies to the fresh list, not a snapshot.
    type('');
    expect(screen.getByText('Beta fresh')).toBeTruthy();
  });
});

/**
 * T150 — cross-column card-over-card drop.
 *
 * Since T118 every card is a dnd-kit sortable item, so dropping a card
 * ON ANOTHER CARD reports that card as `over` — not the column. That
 * combination used to hit a guard that silently returned, so the drop
 * did nothing (the regression). These tests drive the REAL dnd-kit
 * pipeline (PointerSensor with the board's 4px activation constraint)
 * over geometric stubs, so the assertions cover the same code path a
 * real pointer drag exercises: sensor activation → closestCenter →
 * onDragEnd cross-column branch → api.moveTask.
 *
 * Geometric plumbing (jsdom has no layout):
 *  - `rects`: per-test id → fake rect map served through a
 *    getBoundingClientRect spy. Card ids double as element ids (tagged
 *    after mount), so both DndContext's droppable measuring and the
 *    before/after side check read coherent geometry.
 *  - `FakePointerEvent`: jsdom lacks the PointerEvent constructor, so a
 *    MouseEvent subclass carries `isPrimary`; dnd-kit's PointerSensor
 *    is instantiated by the activator's onPointerDown, then listens on
 *    the DOCUMENT for pointermove/pointerup. Every frame is flushed
 *    through act() so DndContext's layout-effect snapshot (`over`) is
 *    fresh when the drop reads it — an unflushed drop reads null.
 */
type FakeRect = { left: number; top: number; width: number; height: number };

let rects: Map<string, FakeRect> = new Map();

function stubRects(map: Record<string, FakeRect>): void {
  rects = new Map(Object.entries(map));
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    const direct = rects.get(this.id);
    if (direct) return fakeDomRect(direct);
    // DragOverlay's wrapper div has no id (and the overlay card sits
    // outside any column <li> while dragging): resolve the fixture id
    // from the card's T-number chip text instead — "T1" → t1.
    const inner = this.querySelector('[data-testid="task-card"]');
    if (inner) {
      const m = /T(\d+)/.exec(inner.textContent ?? '');
      const r = m?.[1] ? rects.get(`t${m[1]}`) : undefined;
      if (r) return fakeDomRect(r);
    }
    return {
      left: 0,
      top: 0,
      width: 0,
      height: 0,
      right: 0,
      bottom: 0,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    } as DOMRect;
  });
}

function fakeDomRect(r: FakeRect): DOMRect {
  return {
    left: r.left,
    top: r.top,
    width: r.width,
    height: r.height,
    right: r.left + r.width,
    bottom: r.top + r.height,
    x: r.left,
    y: r.top,
    toJSON: () => r,
  } as DOMRect;
}

function rectOfT(id: string): FakeRect {
  const r = rects.get(id);
  if (!r) throw new Error(`no rect stubbed for ${id}`);
  return r;
}

function centerOf(r: FakeRect): { x: number; y: number } {
  return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
}

/**
 * The pointerdown activator lives on TaskCard's inner div (it spreads
 * the sortable listeners; React synthetic props aren't visible on DOM
 * nodes, so we key off the card's stable testid).
 */
function cardHandle(root: HTMLElement, title: string): HTMLElement {
  const cards = Array.from(root.querySelectorAll('[data-testid="task-card"]'));
  const match = cards.find((el) => el.textContent?.includes(title));
  if (!match || !(match instanceof HTMLElement)) {
    throw new Error(`drag handle not found for card ${title}`);
  }
  return match;
}

interface PointerEventInitLite {
  clientX?: number;
  clientY?: number;
}

class FakePointerEvent extends MouseEvent {
  readonly isPrimary = true;
  readonly pointerId = 1;
  constructor(type: string, init: PointerEventInitLite = {}) {
    super(type, {
      bubbles: true,
      cancelable: true,
      composed: true,
      clientX: init.clientX ?? 0,
      clientY: init.clientY ?? 0,
      button: 0,
      buttons: type === 'pointerup' ? 0 : 1,
    });
  }
}

/**
 * Drives a real pointer drag against dnd-kit's sensor pipeline:
 * pointerdown on the card handle → 4 pointermove frames on the
 * document (each flushed through act()) → pointerup.
 *
 * dnd-kit's droppable measuring runs at MeasuringFrequency.Optimized
 * — droppable rects are re-measured from a THROTTLED timer (100ms
 * leading edge) that fires OUTSIDE React's render cycle, so plain
 * act() flushing misses it. Every dispatch therefore advances the
 * fake timers inside act() until the pending measure tick (and any
 * cascading state effects) has drained — that is what keeps `over`
 * deterministic when the drop reads it.
 */
async function dragCard(
  handle: HTMLElement,
  from: { x: number; y: number },
  to: { x: number; y: number },
): Promise<void> {
  await act(async () => {
    handle.dispatchEvent(new FakePointerEvent('pointerdown', { clientX: from.x, clientY: from.y }));
    // 4px activation distance of the board's PointerSensor: first
    // move activates the drag, then the timer tick measures.
    document.dispatchEvent(
      new FakePointerEvent('pointermove', { clientX: from.x + 6, clientY: from.y }),
    );
    vi.advanceTimersByTime(100);
  });
  const steps = 4;
  for (let i = 2; i <= steps; i++) {
    const x = from.x + ((to.x - from.x) * i) / steps;
    const y = from.y + ((to.y - from.y) * i) / steps;
    await act(async () => {
      document.dispatchEvent(new FakePointerEvent('pointermove', { clientX: x, clientY: y }));
      vi.advanceTimersByTime(100);
    });
  }
  await act(async () => {
    document.dispatchEvent(new FakePointerEvent('pointermove', { clientX: to.x, clientY: to.y }));
    vi.advanceTimersByTime(100);
    document.dispatchEvent(new FakePointerEvent('pointerup', { clientX: to.x, clientY: to.y }));
    vi.advanceTimersByTime(100);
  });
}

/** A parsed move request (POST /api/v1/tasks/:id/move). */
interface MoveCall {
  taskId: string;
  columnId: string;
  position?: number;
}

function moveCalls(): MoveCall[] {
  return stubHttp.post.mock.calls
    .filter((call): call is [string, Record<string, unknown>] => String(call[0]).endsWith('/move'))
    .map((call) => {
      const taskId = String(call[0]).split('/')[4] ?? '';
      let columnId = '';
      let position: number | undefined;
      const body: unknown = call[1];
      if (typeof body === 'object' && body !== null && 'column_id' in body) {
        const cid: unknown = body.column_id;
        if (typeof cid === 'string') columnId = cid;
      }
      if (typeof body === 'object' && body !== null && 'position' in body) {
        const pos: unknown = body.position;
        if (typeof pos === 'number') position = pos;
      }
      return { taskId, columnId, position };
    });
}

function movesFor(taskId: string): MoveCall[] {
  return moveCalls().filter((c) => c.taskId === taskId);
}

/**
 * Mounts the board with two columns (col-1 "Todo", col-2 "Doing");
 * `columns` overrides the defaults (e.g. WIP limits).
 */
function mountTwoColBoard(tasks: Task[], columns?: Column[]): void {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/agents') return Promise.resolve({ data: { agents: [] } });
    if (url === '/api/v1/projects/p1/tasks') return Promise.resolve({ data: { tasks } });
    return Promise.reject(new Error(`unexpected GET ${url}`));
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <KanbanBoard
          projectId="p1"
          columns={
            columns ?? [
              makeColumn({ id: 'col-1', name: 'Todo', position: 1 }),
              makeColumn({ id: 'col-2', name: 'Doing', position: 2 }),
            ]
          }
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

/**
 * Tags the mounted DOM so rect stubs resolve: the two
 * SortableColumnView wrappers get col-1/col-2 ids (board order),
 * every task card gets its fixture id (t1/t2/t3 — T-number chip is
 * 1:1 with fixture ids here).
 */
function tagBoardGeometry(): void {
  const wraps = document.querySelectorAll('.space-y-3 > div > .min-w-0');
  const ids = ['col-1', 'col-2'];
  wraps.forEach((el, i) => {
    el.id = ids[i] ?? '';
  });
  // The sortable <li> is the measured node (setNodeRef from
  // useSortable); the inner card div carries only the listeners.
  for (const li of document.querySelectorAll('li > [data-testid="task-card"]')) {
    const numEl = li.querySelector('.text-foreground');
    const m = /T(\d+)/.exec(numEl?.textContent ?? '');
    if (m?.[1]) li.id = `t${m[1]}`;
  }
}

describe('KanbanBoard — T150 cross-column card-over-card drop', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    stubRects({});
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('drops card A onto card B in another column → move A into B column, positioned after B', async () => {
    stubRects({
      'col-1': { left: 0, top: 0, width: 240, height: 400 },
      'col-2': { left: 260, top: 0, width: 240, height: 400 },
      t1: { left: 10, top: 60, width: 220, height: 56 },
      t2: { left: 270, top: 60, width: 220, height: 56 },
    });
    mountTwoColBoard(TWO_COL_TASKS);
    await screen.findByText('Card One');
    tagBoardGeometry();

    await dragCard(
      cardHandle(document.body, 'Card One'),
      centerOf(rectOfT('t1')),
      centerOf(rectOfT('t2')),
    );

    const calls = movesFor('t1');
    expect(calls).toHaveLength(1);
    expect(calls[0].columnId).toBe('col-2');
    // Card centers align → drop side is "after the target"; t2 sits at
    // 1024, no deeper neighbour → midpoint semantics put t1 above it.
    expect(calls[0].position).toBeGreaterThan(1024);
    // Exactly one move — a clean drop fires no suffix bumps.
    expect(moveCalls()).toHaveLength(1);
  });

  it('drop ABOVE the target slots before it; drop BELOW slots after', async () => {
    stubRects({
      'col-1': { left: 0, top: 0, width: 240, height: 400 },
      'col-2': { left: 260, top: 0, width: 240, height: 400 },
      t1: { left: 10, top: 60, width: 220, height: 56 },
      t2: { left: 270, top: 60, width: 220, height: 56 },
      t3: { left: 270, top: 130, width: 220, height: 56 },
    });
    mountTwoColBoard([
      ...TWO_COL_TASKS,
      makeTask({ id: 't3', number: 3, column_id: 'col-2', title: 'Card Three', position: 2048 }),
    ]);
    await screen.findByText('Card One');
    tagBoardGeometry();

    // Aim above t2's center: t1 must land BEFORE t2 (< 1024).
    const t2r = rectOfT('t2');
    await dragCard(cardHandle(document.body, 'Card One'), centerOf(rectOfT('t1')), {
      x: t2r.left + t2r.width / 2,
      y: t2r.top + 12,
    });
    const before = movesFor('t1');
    expect(before).toHaveLength(1);
    expect(before[0].position).toBeLessThan(1024);
    // Fresh board for the mirrored case: aim below t2's center. The
    // POST stub is mockImplementation-based (no call reset needed),
    // but moveCalls() reads the shared call log — clear it so the
    // first phase's move doesn't leak into this assertion.
    stubHttp.post.mockClear();
    cleanup();
    stubRects({
      'col-1': { left: 0, top: 0, width: 240, height: 400 },
      'col-2': { left: 260, top: 0, width: 240, height: 400 },
      t1: { left: 10, top: 60, width: 220, height: 56 },
      t2: { left: 270, top: 60, width: 220, height: 56 },
      t3: { left: 270, top: 130, width: 220, height: 56 },
    });
    mountTwoColBoard([
      ...TWO_COL_TASKS,
      makeTask({ id: 't3', number: 3, column_id: 'col-2', title: 'Card Three', position: 2048 }),
    ]);
    await screen.findByText('Card One');
    tagBoardGeometry();
    const t2b = rectOfT('t2');
    await dragCard(cardHandle(document.body, 'Card One'), centerOf(rectOfT('t1')), {
      x: t2b.left + t2b.width / 2,
      y: t2b.top + t2b.height - 6,
    });
    const after = movesFor('t1');
    expect(after).toHaveLength(1);
    expect(after[0].position).toBeGreaterThan(1024);
    expect(after[0].position).toBeLessThan(2048);
  });

  it('WIP-limit 422 reverts the move and shows the N-of-M toast; cards stay put', async () => {
    stubRects({
      'col-1': { left: 0, top: 0, width: 240, height: 400 },
      'col-2': { left: 260, top: 0, width: 240, height: 400 },
      t1: { left: 10, top: 60, width: 220, height: 56 },
      t2: { left: 270, top: 60, width: 220, height: 56 },
    });
    stubHttp.post.mockImplementation((url: string) => {
      if (String(url).endsWith('/move')) {
        return Promise.reject(
          Object.assign(new Error('wip_limit'), {
            response: { status: 422, data: { error: 'wip_limit' } },
          }),
        );
      }
      return Promise.reject(new Error(`unexpected POST ${url}`));
    });
    // col-2 is at WIP 1 and already holds t2: the drop must bounce.
    // The toast derives N and M from `cols` state, so the receiving
    // column carries the limit in the fixture.
    mountTwoColBoard(TWO_COL_TASKS, [
      makeColumn({ id: 'col-1', name: 'Todo', position: 1 }),
      makeColumn({ id: 'col-2', name: 'Doing', position: 2, wip_limit: 1 }),
    ]);
    await screen.findByText('Card One');
    tagBoardGeometry();

    await dragCard(
      cardHandle(document.body, 'Card One'),
      centerOf(rectOfT('t1')),
      centerOf(rectOfT('t2')),
    );

    await waitFor(() => {
      expect(screen.getByText(/is at WIP limit \(1 of 1\)/)).toBeTruthy();
    });
    // Revert: both cards still rendered, no data lost.
    expect(screen.getByText('Card One')).toBeTruthy();
    expect(screen.getByText('Card Two')).toBeTruthy();
    expect(movesFor('t1')).toHaveLength(1);
  });

  it('same-column card-over-card drop still reorders without a column move', async () => {
    stubRects({
      'col-1': { left: 0, top: 0, width: 240, height: 400 },
      'col-2': { left: 260, top: 0, width: 240, height: 400 },
      t1: { left: 10, top: 60, width: 220, height: 56 },
      t2: { left: 10, top: 130, width: 220, height: 56 },
    });
    mountTwoColBoard([
      makeTask({ id: 't1', number: 1, column_id: 'col-1', title: 'Card One', position: 1024 }),
      makeTask({ id: 't2', number: 2, column_id: 'col-1', title: 'Card Two', position: 2048 }),
    ]);
    await screen.findByText('Card One');
    tagBoardGeometry();

    // Drag t2's handle onto t1: same-column reorder → t2 above t1.
    await dragCard(
      cardHandle(document.body, 'Card Two'),
      centerOf(rectOfT('t2')),
      centerOf(rectOfT('t1')),
    );

    const calls = movesFor('t2');
    expect(calls).toHaveLength(1);
    expect(calls[0].columnId).toBe('col-1');
    expect(calls[0].position).toBeLessThan(1024);
    expect(moveCalls()).toHaveLength(1);
  });
});

const TWO_COL_TASKS: Task[] = [
  makeTask({ id: 't1', number: 1, column_id: 'col-1', title: 'Card One', position: 1024 }),
  makeTask({ id: 't2', number: 2, column_id: 'col-2', title: 'Card Two', position: 1024 }),
];
