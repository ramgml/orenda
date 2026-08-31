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
 *   3. Task 113: payload details are human-readable per action —
 *      commented renders nothing, attachment renders the filename,
 *      tags_replaced renders the incoming tag set, diffs render
 *      `from → to`. Raw payload JSON never appears in a row; it
 *      stays on hover only.
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

const COMMENT_PAYLOAD = '{"author_type":"agent","comment_id":"01a0comment0000000000000000"}';
const ATTACHMENT_PAYLOAD =
  '{"attachment_id":"01a0attach00000000000000000","filename":"spec-v2.pdf"}';

function makeItem(overrides: Partial<Omit<TaskActivity, 'task_id'>>): TaskActivity {
  return {
    id: 'act-1',
    actor_type: 'agent',
    actor_id: '01a00994-0000-0000-0000-000000000000',
    action: 'task.commented',
    payload: COMMENT_PAYLOAD,
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
          payload: ATTACHMENT_PAYLOAD,
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

    // Task 113: the comment payload is pure noise — no payload span
    // at all, and none of its keys leak into the row.
    expect(rows[1].children).toHaveLength(3);
    expect(rows[1].textContent).not.toContain('comment_id');
    expect(rows[1].textContent).not.toContain('{');

    // Payload span (attachment): shrinkable + ellipsised, shows the
    // human filename, full JSON only on hover.
    const payloadSpan = rows[0].children[3];
    expect(payloadSpan.className).toContain('min-w-0');
    expect(payloadSpan.className).toContain('truncate');
    expect(payloadSpan.textContent).toBe('· spec-v2.pdf');
    expect(payloadSpan.getAttribute('title')).toBe(ATTACHMENT_PAYLOAD);

    // Created row has an empty `{}` payload — no payload span either.
    expect(rows[2].children).toHaveLength(3);
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

describe('ActivityLog — task 113 human-readable payload details', () => {
  it('renders no payload for task.commented', async () => {
    mount([makeItem({ action: 'task.commented', payload: COMMENT_PAYLOAD })]);

    await screen.findByText('left a comment');
    const row = rowItems()[0];
    // Only timestamp, actor, verb — no payload span, no raw JSON.
    expect(row.children).toHaveLength(3);
    expect(row.textContent).not.toContain('{');
    expect(row.textContent).not.toContain('comment_id');
    expect(row.textContent).not.toContain('author_type');
  });

  it('renders the filename for task.attachment_added and nothing when missing', async () => {
    mount([
      makeItem({
        id: 'act-a',
        action: 'task.attachment_added',
        payload: ATTACHMENT_PAYLOAD,
        created_at: '2026-08-30T11:00:00Z',
      }),
      makeItem({
        id: 'act-b',
        action: 'task.attachment_added',
        // id-only payload: nothing readable to show — no payload span.
        payload: '{"attachment_id":"01a0attach00000000000000000"}',
        created_at: '2026-08-30T10:30:00Z',
      }),
    ]);

    const rows = await waitFor(() => rowItems());
    expect(rows[0].textContent).toContain('· spec-v2.pdf');
    expect(rows[0].textContent).not.toContain('attachment_id');
    // Hover still exposes the full JSON.
    expect(rows[0].children[3].getAttribute('title')).toBe(ATTACHMENT_PAYLOAD);

    expect(rows[1].textContent).toContain('attached a file');
    expect(rows[1].textContent).not.toContain('·');
    expect(rows[1].children).toHaveLength(3);
  });

  it('renders the incoming tag set for task.tags_replaced', async () => {
    mount([
      makeItem({
        id: 'act-t',
        action: 'task.tags_replaced',
        payload: '{"before":["ui"],"after":["epic"]}',
        created_at: '2026-08-30T11:00:00Z',
      }),
      makeItem({
        id: 'act-c',
        action: 'task.tags_replaced',
        payload: '{"before":["epic"],"after":[]}',
        created_at: '2026-08-30T10:30:00Z',
      }),
    ]);

    const rows = await waitFor(() => rowItems());
    expect(rows[0].textContent).toContain('changed the tag set');
    expect(rows[0].textContent).toContain('· → epic');
    expect(rows[0].textContent).not.toContain('{');
    expect(rows[0].textContent).not.toContain('before');
    expect(rows[0].textContent).not.toContain('ui');

    // Cleared tag set renders the explicit empty marker.
    expect(rows[1].textContent).toContain('· → —');
    expect(rows[1].textContent).not.toContain('{');
  });

  it('renders a from → to diff for scalar payloads and keeps JSON on hover', async () => {
    const payload = '{"from":"medium","to":"high"}';
    mount([
      makeItem({
        action: 'task.priority_changed',
        payload,
        created_at: '2026-08-30T11:00:00Z',
      }),
    ]);
    await screen.findByText('changed the priority');
    const row = rowItems()[0];
    // Verb span is human; the detail span holds the diff.
    expect(row.textContent).toContain('medium → high');
    const detail = row.children[3];
    expect(detail.textContent).toBe('· medium → high');
    expect(detail.getAttribute('title')).toBe(payload);
  });
});
