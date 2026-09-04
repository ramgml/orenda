// @vitest-environment jsdom
/**
 * Task 113: the project activity tab (/projects/:id/activity)
 * renders a human verb and human-readable payload details per row —
 * raw payload JSON must not appear in any row.
 */
import { cleanup, render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { api, type ProjectActivityItem } from '@/shared/api/client';
import { ProjectActivityTab } from '@/features/projects/tabs/ProjectActivityTab';

// Flat stub: the real TaskLink drags in the whole task-view tree,
// which this test doesn't exercise.
vi.mock('@/features/tasks/TaskModal', () => ({
  TaskLink: ({ taskId, children }: { taskId: string; children?: ReactNode }) => (
    <a href={`#task-${taskId}`}>{children}</a>
  ),
}));

const getProjectActivityMock = vi.spyOn(api, 'getProjectActivity');

function makeItem(
  overrides: Partial<Omit<ProjectActivityItem, 'task_id' | 'task_title'>>,
): ProjectActivityItem {
  return {
    id: 'act-1',
    actor_type: 'agent',
    actor_id: '01a00994-0000-0000-0000-000000000000',
    action: 'task.commented',
    payload: '{}',
    created_at: '2026-08-30T10:00:00Z',
    task_id: 'task-1',
    task_title: 'Alpha',
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
});

describe('ProjectActivityTab — task 113 readable payload details', () => {
  it('renders human verbs and details with no raw JSON in any row', async () => {
    getProjectActivityMock.mockResolvedValue({
      activity: [
        makeItem({
          id: 'a',
          action: 'task.commented',
          payload: '{"author_type":"agent","comment_id":"c1"}',
        }),
        makeItem({
          id: 'b',
          action: 'task.attachment_added',
          payload: '{"attachment_id":"att-1","filename":"spec.pdf"}',
        }),
        makeItem({
          id: 'c',
          // Legacy rows store tags/color actions unprefixed (Phase 14
          // constants) — both spellings must render the same details.
          action: 'tags_replaced',
          payload: '{"before":["ui"],"after":["epic"]}',
        }),
        makeItem({
          id: 'e',
          action: 'color_changed',
          payload: '{"from":"","to":"#22c55e"}',
        }),
        makeItem({
          id: 'd',
          action: 'task.status_changed',
          payload: '{"from":"todo","to":"doing"}',
        }),
      ],
    });

    render(
      <MemoryRouter initialEntries={['/projects/p1/activity']}>
        <Routes>
          <Route path="/projects/:id/activity" element={<ProjectActivityTab />} />
        </Routes>
      </MemoryRouter>,
    );
    await screen.findByText('left a comment');

    // Human verbs replace the raw action names.
    expect(screen.getByText('attached a file')).toBeTruthy();
    expect(screen.getByText('changed the tag set')).toBeTruthy();
    expect(screen.getByText('changed the status')).toBeTruthy();
    expect(screen.getByText('changed the colour label')).toBeTruthy();

    // Human payload details per action.
    expect(screen.getByText('· spec.pdf')).toBeTruthy();
    expect(screen.getByText('· → epic')).toBeTruthy();
    expect(screen.getByText('· todo → doing')).toBeTruthy();
    expect(screen.getByText('· → #22c55e')).toBeTruthy();

    // No raw JSON anywhere in the feed.
    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(5);

    for (const row of rows) {
      expect(row.textContent).not.toContain('{');
      expect(row.textContent).not.toContain('comment_id');
      expect(row.textContent).not.toContain('attachment_id');
      expect(row.textContent).not.toContain('"before"');
    }
  });
});
