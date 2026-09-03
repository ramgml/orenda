// @vitest-environment jsdom
/**
 * InboxPage component tests.
 *
 * Phase 16 Inbox: flat list of unfiled tasks. Tests pin:
 *   - Empty state when the inbox has no tasks.
 *   - One row per task; quick-add form prepends a new row.
 *   - Quick-add is disabled while busy; submit fires
 *     createInboxTask with the trimmed title.
 *   - "File under" select calls patchTask({ project_id }) and
 *     drops the row when filed under a project.
 *   - The delete button asks window.confirm and removes the row
 *     on accept; cancel is a no-op.
 *   - Clicking a card navigates via the router with
 *     state.backgroundLocation (TaskModal contract) — never via
 *     window.location (a full reload would tear down the SPA).
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { InboxPage } from '@/features/inbox/InboxPage';
import { agentsQueryKey } from '@/shared/hooks/useAgents';
import type { Agent } from '@/shared/api/client';

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

beforeEach(() => {
  vi.clearAllMocks();
  // Radix Select calls scrollIntoView on the selected item; jsdom
  // doesn't implement it — stub it to avoid "not a function" errors.
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
});

// Phase 28.19: TaskCard pulls in useAgents() for the AssigneeChip
// title hint, which lives behind React Query. Wrap InboxPage tests
// in a throwaway QueryClient and seed the agents cache — the stubbed
// axios resolves `/api/v1/agents` to `{ data: {} }`, and a queryFn
// that resolves `undefined` makes TanStack Query log a warning per
// inbox row.
function mount() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  qc.setQueryData(agentsQueryKey, [] as Agent[]);
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <InboxPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function makeTask(
  id: string,
  title: string,
  description?: string,
): {
  id: string;
  title: string;
  description?: string;
  project_id: string;
  column_id: string | null;
  status: string;
  priority: string;
  awaiting: string;
  time_spent_s: number;
  position: number;
  color: string;
  created_at: string;
  updated_at: string;
} {
  return {
    id,
    title,
    description,
    project_id: '',
    column_id: null,
    status: 'todo',
    priority: 'medium',
    awaiting: 'none',
    time_spent_s: 0,
    position: 0,
    color: '',
    created_at: '2026-08-12T00:00:00Z',
    updated_at: '2026-08-12T00:00:00Z',
  };
}

function makeProject(
  id: string,
  name: string,
): {
  id: string;
  name: string;
  color: string;
  description?: string;
  archived?: number;
  created_at?: string;
  updated_at?: string;
} {
  return { id, name, color: '#3b82f6', archived: 0 };
}

describe('InboxPage', () => {
  it('renders the empty state when the inbox has no tasks', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks') return Promise.resolve({ data: { tasks: [] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });

    mount();

    expect(await screen.findByText(/Nothing in the inbox\./)).toBeTruthy();
  });

  it('renders one row per inbox task', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks')
        return Promise.resolve({
          data: { tasks: [makeTask('a', 'Alpha'), makeTask('b', 'Beta')] },
        });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });

    mount();

    expect(await screen.findByText('Alpha')).toBeTruthy();
    expect(screen.getByText('Beta')).toBeTruthy();
  });

  it('quick-add creates an inbox task and prepends it to the list', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks') return Promise.resolve({ data: { tasks: [] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });
    stubHttp.post.mockResolvedValueOnce({
      data: makeTask('new', 'New thought'),
    });

    mount();
    await screen.findByText(/Nothing in the inbox\./);

    const textarea = screen.getByPlaceholderText("What's on your mind?");
    fireEvent.change(textarea, { target: { value: 'New thought' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'New thought' });
    });
    expect(await screen.findByText('New thought')).toBeTruthy();
  });

  it('trims whitespace before submitting', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks') return Promise.resolve({ data: { tasks: [] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });
    stubHttp.post.mockResolvedValueOnce({ data: makeTask('new', 'trimmed') });

    mount();
    await screen.findByText(/Nothing in the inbox\./);

    const textarea = screen.getByPlaceholderText("What's on your mind?");
    fireEvent.change(textarea, { target: { value: '   trimmed   ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add' }));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'trimmed' });
    });
  });

  it('Add button is disabled when the title is empty / whitespace-only', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks') return Promise.resolve({ data: { tasks: [] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });

    mount();
    await screen.findByText(/Nothing in the inbox\./);

    const add = screen.getByRole('button', { name: 'Add' }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);

    const textarea = screen.getByPlaceholderText("What's on your mind?");
    fireEvent.change(textarea, { target: { value: '   ' } });
    expect(add.disabled).toBe(true);

    fireEvent.change(textarea, { target: { value: 'real' } });
    expect(add.disabled).toBe(false);
  });

  it('surfaces an error when the inbox list fails to load', async () => {
    stubHttp.get.mockRejectedValueOnce(new Error('boom'));
    mount();
    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('"File under" select calls patchTask and drops the row', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks')
        return Promise.resolve({ data: { tasks: [makeTask('a', 'Alpha')] } });
      if (url === '/api/v1/projects')
        return Promise.resolve({ data: { projects: [makeProject('p-1', 'Demo')] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });
    stubHttp.patch.mockResolvedValueOnce({ data: makeTask('a', 'Alpha') });

    mount();
    await screen.findByText('Alpha');

    // Radix Select: click the trigger to open the popover, then click the item.
    const trigger = screen.getByRole('combobox');
    fireEvent.click(trigger);
    const option = await screen.findByText('Demo');
    fireEvent.click(option);

    await waitFor(() => {
      expect(stubHttp.patch).toHaveBeenCalledWith('/api/v1/tasks/a', { project_id: 'p-1' });
    });
    expect(screen.queryByText('Alpha')).toBeNull();
  });

  it("archives (hides) projects flagged archived so they don't show in the picker", async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks')
        return Promise.resolve({ data: { tasks: [makeTask('a', 'Alpha')] } });
      if (url === '/api/v1/projects')
        return Promise.resolve({
          data: {
            projects: [makeProject('p-1', 'Live'), { ...makeProject('p-2', 'Dead'), archived: 1 }],
          },
        });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });

    mount();
    await screen.findByText('Alpha');

    // Radix Select: open the popover and check which items are present.
    const trigger = screen.getByRole('combobox');
    fireEvent.click(trigger);
    await waitFor(() => {
      expect(screen.queryByText('Live')).toBeTruthy();
    });
    expect(screen.queryByText('Dead')).toBeNull();
  });

  it('delete asks window.confirm and removes the row on accept', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks')
        return Promise.resolve({ data: { tasks: [makeTask('a', 'Alpha')] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });
    stubHttp.delete.mockResolvedValueOnce({ data: undefined });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);

    mount();
    await screen.findByText('Alpha');

    fireEvent.click(screen.getByRole('button', { name: 'delete' }));

    await waitFor(() => {
      expect(stubHttp.delete).toHaveBeenCalledWith('/api/v1/tasks/a');
    });
    expect(screen.queryByText('Alpha')).toBeNull();
    expect(confirmSpy).toHaveBeenCalledTimes(1);
  });

  it('canceling the delete confirm is a no-op', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks')
        return Promise.resolve({ data: { tasks: [makeTask('a', 'Alpha')] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    mount();
    await screen.findByText('Alpha');

    fireEvent.click(screen.getByRole('button', { name: 'delete' }));

    expect(stubHttp.delete).not.toHaveBeenCalled();
    expect(screen.getByText('Alpha')).toBeTruthy();
  });

  // Task 102: clicking a card must open the task modal via the
  // router (state.backgroundLocation) — not window.location.href,
  // which used to full-reload the page and tear down the SPA.
  it('card click navigates via the router with backgroundLocation, never window.location', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks')
        return Promise.resolve({ data: { tasks: [makeTask('modal-9', 'Modal me')] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
      if (url.startsWith('/api/v1/agents')) return Promise.resolve({ data: { agents: [] } });
      return Promise.resolve({ data: {} });
    });

    const navigations: Array<{ pathname: string; state: unknown }> = [];
    function Probe() {
      const location = useLocation();
      navigations.push({ pathname: location.pathname, state: location.state });
      return null;
    }
    const hrefSpy = vi.fn();
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: {
        ...window.location,
        get href() {
          return 'http://localhost/inbox';
        },
        set href(v: string) {
          hrefSpy(v);
        },
      },
    });

    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/inbox']}>
          <Probe />
          <InboxPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    fireEvent.click(await screen.findByTestId('task-card'));

    const last = navigations[navigations.length - 1];
    expect(last.pathname).toBe('/tasks/modal-9');
    expect(last.state).toEqual({
      backgroundLocation: expect.objectContaining({ pathname: '/inbox' }),
    });
    expect(hrefSpy).not.toHaveBeenCalled();
  });
});
