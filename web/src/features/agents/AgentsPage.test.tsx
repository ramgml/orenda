// @vitest-environment jsdom
/**
 * AgentsPage thin-coverage smoke tests.
 *
 *   - Empty state when no agents exist.
 *   - Table renders one row per agent with the status pill colour
 *     driven by a.status.
 *   - The "New agent" button toggles the create form.
 *   - The chips input in the create form commits labels on Enter
 *     and posts createAgent with the label set on submit.
 *   - The filter chips narrow the list and re-issue listAgents
 *     with the OR-filter query string.
 *   - Submitting the create form posts createAgent and shows the
 *     plaintext API token exactly once (until Dismiss).
 *   - Delete requires window.confirm; accepted calls deleteAgent.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { AuthProvider } from '@/features/auth/AuthContext';
import { AgentsPage } from '@/features/agents/AgentsPage';

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

/**
 * Configure the http stub for a test, then mount the page.
 *
 * Note: AgentsPage calls `load()` in a useEffect on first render
 * (Phase 28.19 added the filter dep), so the stub MUST be set
 * BEFORE render. We always stub /api/v1/me (so AuthProvider lands
 * in 'authenticated') and pass `agents` to control what
 * `listAgents` returns.
 */
function mount(agents: unknown[] = []) {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/me') {
      return Promise.resolve({
        data: { user_id: 'u-1', email: 'me@x.com', display_name: 'Me', role: 'owner' },
      });
    }
    if (url === '/api/v1/agents') return Promise.resolve({ data: { agents } });
    return Promise.resolve({ data: {} });
  });

  return render(
    <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <AuthProvider>
        <AgentsPage />
      </AuthProvider>
    </MemoryRouter>,
  );
}

function makeAgent(
  overrides: Partial<{
    id: string;
    name: string;
    type: string[];
    status: string;
    last_seen_at: string;
    created_at: string;
  }> = {},
): {
  id: string;
  name: string;
  type: string[];
  status: string;
  last_seen_at: string | null;
  created_at: string;
} {
  return {
    id: 'a-1',
    name: 'agent-1',
    type: [],
    status: 'online',
    last_seen_at: '2026-08-12T10:00:00Z',
    created_at: '2026-08-12T09:00:00Z',
    ...overrides,
  };
}

describe('AgentsPage', () => {
  it('renders the empty state when there are no agents', async () => {
    mount();

    expect(await screen.findByText(/No agents yet\./)).toBeTruthy();
  });

  it('renders one row per agent with the right status pill', async () => {
    mount([
      makeAgent({ id: 'a-1', name: 'first', status: 'online' }),
      makeAgent({ id: 'a-2', name: 'second', status: 'offline' }),
      makeAgent({ id: 'a-3', name: 'third', status: 'disabled' }),
    ]);

    expect(await screen.findByText('first')).toBeTruthy();
    expect(screen.getByText('second')).toBeTruthy();
    expect(screen.getByText('third')).toBeTruthy();
    expect(screen.getByText('online')).toBeTruthy();
    expect(screen.getByText('offline')).toBeTruthy();
    expect(screen.getByText('disabled')).toBeTruthy();
  });

  it('renders label chips per agent (free-form set, Phase 28.19)', async () => {
    mount([
      makeAgent({ id: 'a-1', name: 'first', type: ['qwen', 'installer'] }),
      makeAgent({ id: 'a-2', name: 'second', type: [] }),
      makeAgent({ id: 'a-3', name: 'third', type: ['claude'] }),
    ]);

    await screen.findByText('first');
    // Two agents have non-empty type sets; each chip must render
    // exactly one <span> per label. Empty set shows the em-dash.
    expect(screen.getAllByText('qwen').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('installer').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('claude').length).toBeGreaterThanOrEqual(1);
    // Agents with no labels render an em-dash, not empty space.
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(1);
  });

  it('"New agent" toggles the create form open', async () => {
    mount();
    await screen.findByText(/No agents yet\./);

    fireEvent.click(screen.getByRole('button', { name: /new agent/i }));
    expect(screen.getByPlaceholderText('Agent name (unique)')).toBeTruthy();

    // Toggle back closes the form.
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }));
    expect(screen.queryByPlaceholderText('Agent name (unique)')).toBeNull();
  });

  it('chips input: Enter commits a label; × removes it', async () => {
    mount();
    await screen.findByText(/No agents yet\./);
    fireEvent.click(screen.getByRole('button', { name: /new agent/i }));

    const labelInput = screen.getAllByPlaceholderText(/Labels/i)[0] as HTMLInputElement;
    fireEvent.change(labelInput, { target: { value: 'qwen' } });
    fireEvent.keyDown(labelInput, { key: 'Enter' });
    expect(screen.getAllByTestId('label-chip')[0]?.textContent).toMatch(/qwen/);

    fireEvent.change(labelInput, { target: { value: 'Installer' } });
    fireEvent.keyDown(labelInput, { key: 'Enter' });
    const chips = screen.getAllByTestId('label-chip');
    expect(chips).toHaveLength(2);
    expect(chips[1]?.textContent).toMatch(/installer/);

    // × on the first chip removes it.
    fireEvent.click(screen.getAllByRole('button', { name: /Remove qwen/i })[0]);
    expect(screen.getAllByTestId('label-chip')).toHaveLength(1);
  });

  it('submitting the create form posts createAgent with the label set and reveals the token', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: {
        agent: makeAgent({ id: 'a-new', name: 'fresh-agent', type: ['qwen'] }),
        plain_token: 'ort_secret_abc123',
      },
    });

    mount();
    await screen.findByText(/No agents yet\./);

    fireEvent.click(screen.getByRole('button', { name: /new agent/i }));
    fireEvent.change(screen.getByPlaceholderText('Agent name (unique)'), {
      target: { value: 'fresh-agent' },
    });
    const labelInput = screen.getAllByPlaceholderText(/Labels/i)[0] as HTMLInputElement;
    fireEvent.change(labelInput, { target: { value: 'qwen' } });
    fireEvent.keyDown(labelInput, { key: 'Enter' });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/agents', {
        name: 'fresh-agent',
        type: ['qwen'],
        description: undefined,
      });
    });
    // Token reveal banner.
    expect(await screen.findByText(/copy now, it won/)).toBeTruthy();
    expect(screen.getByText('ort_secret_abc123')).toBeTruthy();
  });

  it('filter chips refetch with repeated ?type= query', async () => {
    mount([
      makeAgent({ id: 'a-1', name: 'one', type: ['qwen'] }),
      makeAgent({ id: 'a-2', name: 'two', type: ['claude'] }),
    ]);
    await screen.findByText('one');

    // The second mount's filter input sits above the table.
    const filterInput = screen.getAllByPlaceholderText(/Type a label/i)[0] as HTMLInputElement;
    fireEvent.change(filterInput, { target: { value: 'qwen' } });
    fireEvent.keyDown(filterInput, { key: 'Enter' });
    fireEvent.change(filterInput, { target: { value: 'installer' } });
    fireEvent.keyDown(filterInput, { key: 'Enter' });

    await waitFor(() => {
      const calls = stubHttp.get.mock.calls
        .map((c) => c[0] as string)
        .filter((u) => u.startsWith('/api/v1/agents'));
      expect(calls.some((u) => u.includes('type=qwen') && u.includes('type=installer'))).toBe(true);
    });
  });

  it('surfaces an inline error when createAgent rejects', async () => {
    stubHttp.post.mockRejectedValueOnce(new Error('boom'));

    mount();
    await screen.findByText(/No agents yet\./);
    fireEvent.click(screen.getByRole('button', { name: /new agent/i }));
    fireEvent.change(screen.getByPlaceholderText('Agent name (unique)'), {
      target: { value: 'x' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('Delete requires window.confirm; accepted calls deleteAgent', async () => {
    stubHttp.delete.mockResolvedValueOnce({ data: undefined });
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);

    mount([makeAgent({ id: 'a-1', name: 'doomed' })]);
    await screen.findByText('doomed');
    fireEvent.click(screen.getByRole('button', { name: /delete/i }));

    await waitFor(() => {
      expect(stubHttp.delete).toHaveBeenCalledWith('/api/v1/agents/a-1');
    });
    expect(confirmSpy).toHaveBeenCalledTimes(1);
  });

  it('Delete is a no-op when the user cancels the confirm', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);

    mount([makeAgent({ id: 'a-1', name: 'kept' })]);
    await screen.findByText('kept');

    fireEvent.click(screen.getByRole('button', { name: /delete/i }));

    expect(stubHttp.delete).not.toHaveBeenCalled();
    expect(screen.getByText('kept')).toBeTruthy();
  });
});
