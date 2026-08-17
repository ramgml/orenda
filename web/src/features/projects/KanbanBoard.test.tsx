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
    expect(screen.getByRole('checkbox', { name: /compact cards/i })).toHaveProperty(
      'checked',
      true,
    );
  });

  it('selects a task and applies a bulk priority update', async () => {
    const task = makeTask();
    stubHttp.post.mockResolvedValue({ data: { tasks: [{ ...task, priority: 'urgent' }] } });
    mountBoard([task]);

    fireEvent.click(await screen.findByRole('checkbox', { name: /select dense task/i }));
    fireEvent.change(screen.getByRole('combobox', { name: /bulk priority/i }), {
      target: { value: 'urgent' },
    });
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
