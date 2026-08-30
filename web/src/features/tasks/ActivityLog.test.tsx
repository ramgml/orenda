// @vitest-environment jsdom
/**
 * Task 104: the Activity feed row layout and actor labels in the
 * task card (ActivityLog).
 *
 * Contracts pinned here:
 *
 *   1. Row layout — the verb span carries `whitespace-nowrap` (no
 *      mid-phrase wrapping: "left a / comment"), the payload span
 *      carries `min-w-0 truncate` so the flex item is allowed to
 *      shrink and ellipsises inside the container, and the full
 *      payload JSON is exposed via the `title` attribute (hover
 *      shows the whole JSON).
 *   2. Actor labels are human-readable: `agent:<id>` resolves to the
 *      agent's name from the cached listAgents() result (id-prefix
 *      fallback only while the cache is cold); `user:<id>` matching
 *      the authenticated user renders their display_name.
 */
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { api, type TaskActivity } from '@/shared/api/client';
import { ActivityLog } from '@/features/tasks/TaskViewBody';

// The actor label for user activities comes from the auth session
// (same convention as assigneeLabel() in TaskFieldControls).
vi.mock('@/features/auth/AuthContext', () => ({
  useAuth: () => ({ user: { user_id: 'u-me', email: 'me@x.io', display_name: 'Alice Owner' } }),
}));

// ActivityLog resolves agent actors through the cached listAgents()
// query — mock the API so no network call is made. The query still
// runs, so every render must sit inside a QueryClientProvider (same
// convention as TaskCard.test).
const listAgentsMock = vi.spyOn(api, 'listAgents');

const PAYLOAD_JSON = '{"author_type":"agent","comment_id":"01a0comment0000000000000000"}';

function makeItem(overrides: Partial<Omit<TaskActivity, 'task_id'>>): TaskActivity {
  return {
    id: 'act-1',
    actor_type: 'agent',
    actor_id: '01a00994-0000-0000-0000-000000000000',
    action: 'task.commented',
    payload: PAYLOAD_JSON,
    created_at: '2026-08-30T10:00:00Z',
    task_id: 'task-1',
    ...overrides,
  };
}

function makeAgent(
  id: string,
  name: string,
): {
  id: string;
  name: string;
  type: string[];
  status: 'online' | 'offline' | 'disabled';
  max_concurrent: number;
  created_at: string;
  token_id: string;
} {
  return {
    id,
    name,
    type: [],
    status: 'online',
    max_concurrent: 1,
    created_at: '2026-08-30T09:00:00Z',
    token_id: 'tok-1',
  };
}

function mount(items: TaskActivity[], agents: unknown[] = []): void {
  listAgentsMock.mockResolvedValue(agents as never);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={qc}>
      <ActivityLog items={items} />
    </QueryClientProvider>,
  );
}

function rowItems(): HTMLElement[] {
  return screen.getAllByRole('listitem');
}

beforeEach(() => {
  // Fresh default per test: reset wipes prior per-test resolved
  // values, then the empty-list default covers the cold-cache case.
  listAgentsMock.mockReset();
  listAgentsMock.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
});

describe('ActivityLog — task 104 layout and actor labels', () => {
  it('renders comment, attachment and created rows with layout classes', async () => {
    mount(
      [
        makeItem({
          id: 'act-2',
          action: 'task.attachment_added',
          payload: '{"attachment_id":"01a0attach00000000000000000"}',
          created_at: '2026-08-30T11:00:00Z',
        }),
        makeItem({ id: 'act-1' }),
        makeItem({
          id: 'act-0',
          action: 'task.created',
          payload: '{}',
          actor_type: 'user',
          actor_id: 'u-me',
          created_at: '2026-08-30T09:00:00Z',
        }),
      ],
      [makeAgent('01a00994-0000-0000-0000-000000000000', 'QA-bot')],
    );

    const rows = await waitFor(() => {
      const found = rowItems();
      expect(found).toHaveLength(3);
      return found;
    });

    // Most recent first.
    expect(rows[0].textContent).toContain('attached a file');
    expect(rows[1].textContent).toContain('left a comment');
    expect(rows[2].textContent).toContain('created the task');

    // Verb span: no mid-phrase wrapping.
    const verbSpan = rows[1].children[2];
    expect(verbSpan.textContent).toBe('left a comment');
    expect(verbSpan.className).toContain('whitespace-nowrap');

    // Payload span: shrinkable + ellipsised, full JSON on hover.
    const payloadSpan = rows[1].children[3];
    expect(payloadSpan.className).toContain('min-w-0');
    expect(payloadSpan.className).toContain('truncate');
    expect(payloadSpan.getAttribute('title')).toBe(PAYLOAD_JSON);
  });

  it('resolves an agent actor to the agent name from the mocked listAgents', async () => {
    mount([makeItem({})], [makeAgent('01a00994-0000-0000-0000-000000000000', 'QA-bot')]);

    const actor = await screen.findByText('QA-bot');
    expect(actor.className).toContain('shrink-0');
    expect(actor.className).toContain('whitespace-nowrap');
    // Raw id must not leak into the label.
    expect(rowItems()[0].textContent).not.toContain('01a00994');
  });

  it('falls back to the id prefix while the agent cache is cold', async () => {
    mount([makeItem({})], []);

    await waitFor(() => {
      expect(rowItems()[0].children[1].textContent).toBe('01a00994');
    });
  });

  it('renders the current user actor as the display name', async () => {
    mount(
      [makeItem({ actor_type: 'user', actor_id: 'u-me' })],
      [makeAgent('01a00994-0000-0000-0000-000000000000', 'QA-bot')],
    );

    const actor = await screen.findByText('Alice Owner');
    expect(actor.className).toContain('shrink-0');
    expect(actor.className).toContain('whitespace-nowrap');
  });
});
