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
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
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
});
