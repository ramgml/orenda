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
 *   - Errors from listInboxTasks / listProjects surface inline.
 *
 * Clicking on the row link would fire openTaskModal — that path is
 * hard to drive from jsdom and is covered by the E2E suite; here we
 * just render the rows.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { InboxPage } from '@/features/inbox/InboxPage';

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
});

afterEach(() => {
  cleanup();
});

function mount() {
  return render(
    <MemoryRouter>
      <InboxPage />
    </MemoryRouter>,
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
      return Promise.resolve({ data: {} });
    });
    stubHttp.patch.mockResolvedValueOnce({ data: makeTask('a', 'Alpha') });

    mount();
    await screen.findByText('Alpha');

    // The first <select> in the row is the file-under picker.
    const select = screen.getByRole('combobox') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'p-1' } });

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
      return Promise.resolve({ data: {} });
    });

    mount();
    await screen.findByText('Alpha');

    // The picker is per-row; with one task we expect 3 options
    // (placeholder + Live + Dead-not-archived).
    const options = screen.getAllByRole('option') as HTMLOptionElement[];
    const labels = options.map((o) => o.textContent);
    expect(labels).toContain('Live');
    expect(labels).not.toContain('Dead');
  });

  it('delete asks window.confirm and removes the row on accept', async () => {
    stubHttp.get.mockImplementation((url: string) => {
      if (url === '/api/v1/inbox/tasks')
        return Promise.resolve({ data: { tasks: [makeTask('a', 'Alpha')] } });
      if (url === '/api/v1/projects') return Promise.resolve({ data: { projects: [] } });
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
      return Promise.resolve({ data: {} });
    });
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    mount();
    await screen.findByText('Alpha');

    fireEvent.click(screen.getByRole('button', { name: 'delete' }));

    expect(stubHttp.delete).not.toHaveBeenCalled();
    expect(screen.getByText('Alpha')).toBeTruthy();
  });
});
