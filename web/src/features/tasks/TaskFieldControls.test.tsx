// @vitest-environment jsdom
/**
 * TaskFieldControls tests (Phase 27.7 + 27.8.4; shadcn Select since
 * task 12 step 3b).
 *
 * Pins these contracts:
 *   1. The Status select renders the project's columns (sorted by
 *      position), not a hardcoded enum — a custom column added in
 *      Phase 12 shows up automatically.
 *   2. PATCHing the status goes through api.patchTask — the
 *      backend's SyncStatusAndColumn (Phase 27.8) moves the card.
 *   3. Inbox tasks (project_id === '') render Status as a
 *      read-only label — there's no board to project onto.
 *   4. Priority / Assignee keep their 27.7 behaviour.
 *
 * AuthContext is mocked by providing a wrapper component that sets
 * the user. We don't bother with the full AuthProvider — the
 * component reads only `useAuth().user`, which is what we stub.
 *
 * Task 12 step 3b: the controls are now the shadcn `Select`
 * (Radix), not a native `<select>`. Radix opens the dropdown on
 * `pointerdown` (mouse, button 0, no ctrl) and selects an item on
 * `pointerup` — jsdom ships no PointerEvent implementation, so we
 * polyfill a minimal one (plus the pointer-capture helpers Radix
 * calls) at the top of the file and drive the dropdown with those
 * two events. The behavioral contracts (which options render, what
 * gets PATCHed) are unchanged.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { TaskFieldControls } from '@/features/tasks/TaskFieldControls';
import { api, type BoardColumn, type ProjectBoard } from '@/shared/api/client';

// --- jsdom PointerEvent polyfill (Radix Select needs it) ---
//
// jsdom has no PointerEvent; testing-library's fireEvent.pointerDown
// / .pointerUp fall back to MouseEvent and lose `pointerType`, which
// Radix reads. A constructor extending MouseEvent with the
// pointerType field carries everything Radix touches.
class FakePointerEvent extends MouseEvent {
  pointerType: string;
  constructor(type: string, init: PointerEventInit & { pointerType?: string } = {}) {
    super(type, init);
    this.pointerType = init.pointerType ?? '';
  }
}
window.PointerEvent = FakePointerEvent as unknown as typeof PointerEvent;
window.HTMLElement.prototype.scrollIntoView = () => {};
window.HTMLElement.prototype.hasPointerCapture = () => false;
window.HTMLElement.prototype.releasePointerCapture = () => {};

// Drive a Radix Select: open its dropdown via the trigger testid,
// then resolve to the option with the accessible name.
function openSelect(getByTestId: (id: string) => HTMLElement, testId: string): void {
  fireEvent.pointerDown(getByTestId(testId), {
    button: 0,
    ctrlKey: false,
    pointerType: 'mouse',
  });
}

async function pickOption(name: string | RegExp): Promise<HTMLElement> {
  const option = await screen.findByRole('option', { name });
  fireEvent.click(option);
  return option;
}

// AuthContext mock: provide a user with id 'u-me' so the "me"
// branch of the Assignee select is exercisable.
vi.mock('@/features/auth/AuthContext', () => ({
  useAuth: () => ({ user: { user_id: 'u-me', email: 'me@x.io', display_name: 'Me' } }),
}));

// Default board used by project-aware tests. Five canonical columns
// — order matters (sorted by position by the component).
const defaultBoardColumns: BoardColumn[] = [
  { id: 'c-bl', board_id: 'b1', name: 'Backlog', position: 0, status: 'backlog' },
  { id: 'c-td', board_id: 'b1', name: 'Todo', position: 1024, status: 'todo' },
  { id: 'c-ip', board_id: 'b1', name: 'In progress', position: 2048, status: 'in_progress' },
  { id: 'c-rv', board_id: 'b1', name: 'Review', position: 3072, status: 'review' },
  { id: 'c-dn', board_id: 'b1', name: 'Done', position: 4096, status: 'done' },
];

const defaultBoard: ProjectBoard = {
  board: {
    id: 'b1',
    project_id: 'p1',
    name: 'Main',
    position: 0,
    created_at: '2026-01-01T00:00:00Z',
  },
  columns: defaultBoardColumns,
};

function mockBoard(cols: BoardColumn[] = defaultBoardColumns): void {
  vi.spyOn(api, 'getBoard').mockResolvedValue(defaultBoardWith(cols));
}

function defaultBoardWith(cols: BoardColumn[]): ProjectBoard {
  return { ...defaultBoard, columns: cols };
}

beforeEach(() => {
  vi.restoreAllMocks();
});

afterEach(() => {
  cleanup();
});

function renderControls(extra: Partial<Parameters<typeof TaskFieldControls>[0]> = {}) {
  return render(
    <TaskFieldControls
      status="todo"
      priority="medium"
      assigneeType=""
      assigneeID=""
      taskID="t1"
      busy={false}
      projectID="p1"
      onChanged={() => {}}
      onError={() => {}}
      {...extra}
    />,
  );
}

describe('TaskFieldControls', () => {
  it('renders all three controls with the current values', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    mockBoard();
    const { getByTestId } = renderControls();
    // Wait for the board to load so the Status select is present.
    await waitFor(() => expect(getByTestId('task-status')).toBeTruthy());
    // Radix Select shows the selected option's label in the trigger.
    expect(getByTestId('task-status').textContent).toContain('Todo');
    expect(getByTestId('task-priority').textContent).toContain('Medium');
    // Assignee defaults to "Unassigned" for an unassigned task.
    expect(getByTestId('task-assignee-trigger').textContent).toContain('Unassigned');
  });

  it('Status select renders project columns sorted by position', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    // Deliberately unsorted + a custom column to verify both
    // ordering and that custom statuses show up.
    mockBoard([
      { id: 'c-dn', board_id: 'b1', name: 'Done', position: 4096, status: 'done' },
      { id: 'c-td', board_id: 'b1', name: 'Todo', position: 1024, status: 'todo' },
      { id: 'c-qa', board_id: 'b1', name: 'QA', position: 5120, status: 'qa' },
      { id: 'c-bl', board_id: 'b1', name: 'Backlog', position: 0, status: 'backlog' },
    ]);
    const { getByTestId } = renderControls();
    await waitFor(() => expect(getByTestId('task-status')).toBeTruthy());
    openSelect(getByTestId, 'task-status');
    const names = (await screen.findAllByRole('option')).map((o) => o.textContent);
    // Position-ordered: backlog, todo, done, qa.
    expect(names).toEqual(['Backlog', 'Todo', 'Done', 'QA']);
  });

  it('Patches the task when status changes', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    mockBoard();
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({
      id: 't1',
      status: 'in_progress',
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    const onChanged = vi.fn();
    const { getByTestId } = renderControls({ onChanged });
    await waitFor(() => expect(getByTestId('task-status')).toBeTruthy());
    openSelect(getByTestId, 'task-status');
    await pickOption('In progress');
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(1));
    expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ status: 'in_progress' }));
    expect(onChanged).toHaveBeenCalled();
  });

  it('Patches priority with the new value', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    mockBoard();
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never);
    const { getByTestId } = renderControls();
    await waitFor(() => expect(getByTestId('task-priority')).toBeTruthy());
    openSelect(getByTestId, 'task-priority');
    await pickOption('Urgent');
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ priority: 'urgent' })),
    );
  });

  it('Assignee "Unassigned" sends empty assignee pair', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    mockBoard();
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never);
    const { getByTestId } = renderControls({ assigneeType: 'agent', assigneeID: 'a1' });
    await waitFor(() => expect(getByTestId('task-assignee-trigger')).toBeTruthy());
    openSelect(getByTestId, 'task-assignee-trigger');
    await pickOption('Unassigned');
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith(
        't1',
        expect.objectContaining({ assignee_type: '', assignee_id: '' }),
      ),
    );
  });

  it('Assignee "Me" sends user / user_id', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    mockBoard();
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never);
    const { getByTestId } = renderControls();
    await waitFor(() => expect(getByTestId('task-assignee-trigger')).toBeTruthy());
    openSelect(getByTestId, 'task-assignee-trigger');
    await pickOption('Me');
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith(
        't1',
        expect.objectContaining({ assignee_type: 'user', assignee_id: 'u-me' }),
      ),
    );
  });

  it('Lists agents in the Assignee dropdown by name', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      { id: 'a1', name: 'QA-bot', status: 'online' } as any,
    ]);
    mockBoard();
    vi.spyOn(api, 'patchTask').mockResolvedValue({} as never);
    const { getByTestId } = renderControls();
    await waitFor(() => expect(getByTestId('task-assignee-trigger')).toBeTruthy());
    openSelect(getByTestId, 'task-assignee-trigger');
    // The agent appears as a named option (value "agent:a1" is a
    // React prop; the accessible surface is the label text).
    const option = await screen.findByRole('option', { name: 'QA-bot (online)' });
    expect(option).toBeTruthy();
  });

  it('surfaces api errors via onError', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    mockBoard();
    vi.spyOn(api, 'patchTask').mockRejectedValue(new Error('boom'));
    const onError = vi.fn();
    const { getByTestId } = renderControls({ onError });
    await waitFor(() => expect(getByTestId('task-status')).toBeTruthy());
    openSelect(getByTestId, 'task-status');
    await pickOption('Done');
    await waitFor(() => expect(onError).toHaveBeenCalledWith('boom'));
  });

  // Phase 27.8.4: Inbox tasks have no project, hence no board —
  // Status renders as a read-only label, no select, no API call.
  it('renders Status as read-only when projectID is empty (Inbox)', async () => {
    const getBoardSpy = vi.spyOn(api, 'getBoard');
    vi.spyOn(api, 'listAgents').mockResolvedValue([]);
    const { queryByTestId, getByTestId } = renderControls({ projectID: '' });
    // No select — read-only instead.
    expect(queryByTestId('task-status')).toBeNull();
    expect(getByTestId('task-status-readonly')).toBeTruthy();
    expect(getByTestId('task-status-value').textContent).toBe('todo');
    // And the component must not have tried to fetch a board.
    expect(getBoardSpy).not.toHaveBeenCalled();
  });
});
