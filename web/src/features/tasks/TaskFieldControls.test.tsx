// @vitest-environment jsdom
/**
 * TaskFieldControls tests (Phase 27.7 + 27.8.4).
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
 */
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TaskFieldControls } from '@/features/tasks/TaskFieldControls'
import { api, type BoardColumn, type ProjectBoard } from '@/shared/api/client'

// AuthContext mock: provide a user with id 'u-me' so the "me"
// branch of the Assignee select is exercisable.
vi.mock('@/features/auth/AuthContext', () => ({
  useAuth: () => ({ user: { user_id: 'u-me', email: 'me@x.io', display_name: 'Me' } }),
}))

// Default board used by project-aware tests. Five canonical columns
// — order matters (sorted by position by the component).
const defaultBoardColumns: BoardColumn[] = [
  { id: 'c-bl', board_id: 'b1', name: 'Backlog', position: 0, status: 'backlog' },
  { id: 'c-td', board_id: 'b1', name: 'Todo', position: 1024, status: 'todo' },
  { id: 'c-ip', board_id: 'b1', name: 'In progress', position: 2048, status: 'in_progress' },
  { id: 'c-rv', board_id: 'b1', name: 'Review', position: 3072, status: 'review' },
  { id: 'c-dn', board_id: 'b1', name: 'Done', position: 4096, status: 'done' },
]

const defaultBoard: ProjectBoard = {
  board: {
    id: 'b1',
    project_id: 'p1',
    name: 'Main',
    position: 0,
    created_at: '2026-01-01T00:00:00Z',
  },
  columns: defaultBoardColumns,
}

function mockBoard(cols: BoardColumn[] = defaultBoardColumns): void {
  vi.spyOn(api, 'getBoard').mockResolvedValue({
    board: defaultBoard.board,
    columns: cols,
  })
}

beforeEach(() => {
  vi.restoreAllMocks()
})

afterEach(() => {
  cleanup()
})

describe('TaskFieldControls', () => {
  it('renders all three controls with the current values', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    mockBoard()
    const { getByTestId } = render(
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
      />,
    )
    // Wait for the board to load so the Status select is present.
    await waitFor(() => expect(getByTestId('task-status')).toBeTruthy())
    const status = getByTestId('task-status') as HTMLSelectElement
    const priority = getByTestId('task-priority') as HTMLSelectElement
    expect(status.value).toBe('todo')
    expect(priority.value).toBe('medium')
    // Assignee defaults to "unassigned" for an unassigned task.
    const assignee = getByTestId('task-assignee').querySelector('select') as HTMLSelectElement
    expect(assignee.value).toBe('unassigned')
  })

  it('Status select renders project columns sorted by position', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    // Deliberately unsorted + a custom column to verify both
    // ordering and that custom statuses show up.
    mockBoard([
      { id: 'c-dn', board_id: 'b1', name: 'Done', position: 4096, status: 'done' },
      { id: 'c-td', board_id: 'b1', name: 'Todo', position: 1024, status: 'todo' },
      { id: 'c-qa', board_id: 'b1', name: 'QA', position: 5120, status: 'qa' },
      { id: 'c-bl', board_id: 'b1', name: 'Backlog', position: 0, status: 'backlog' },
    ])
    const { getByTestId } = render(
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
      />,
    )
    const status = await waitFor(() => getByTestId('task-status') as HTMLSelectElement)
    const labels = Array.from(status.options).map((o) => `${o.value}=${o.text}`)
    // Position-ordered: backlog, todo, done, qa.
    expect(labels).toEqual(['backlog=Backlog', 'todo=Todo', 'done=Done', 'qa=QA'])
  })

  it('Patches the task when status changes', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    mockBoard()
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({
      id: 't1',
      status: 'in_progress',
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any)
    const onChanged = vi.fn()
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType=""
        assigneeID=""
        taskID="t1"
        busy={false}
        projectID="p1"
        onChanged={onChanged}
        onError={() => {}}
      />,
    )
    const status = await waitFor(() => getByTestId('task-status') as HTMLSelectElement)
    fireEvent.change(status, { target: { value: 'in_progress' } })
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(1))
    expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ status: 'in_progress' }))
    expect(onChanged).toHaveBeenCalled()
  })

  it('Patches priority with the new value', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    mockBoard()
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
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
      />,
    )
    fireEvent.change(getByTestId('task-priority'), { target: { value: 'urgent' } })
    await waitFor(() => expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ priority: 'urgent' })))
  })

  it('Assignee "Unassigned" sends empty assignee pair', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    mockBoard()
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType="agent"
        assigneeID="a1"
        taskID="t1"
        busy={false}
        projectID="p1"
        onChanged={() => {}}
        onError={() => {}}
      />,
    )
    const select = getByTestId('task-assignee').querySelector('select') as HTMLSelectElement
    fireEvent.change(select, { target: { value: 'unassigned' } })
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ assignee_type: '', assignee_id: '' })),
    )
  })

  it('Assignee "Me" sends user / user_id', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    mockBoard()
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
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
      />,
    )
    const select = getByTestId('task-assignee').querySelector('select') as HTMLSelectElement
    fireEvent.change(select, { target: { value: 'me' } })
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ assignee_type: 'user', assignee_id: 'u-me' })),
    )
  })

  it('Lists agents in the Assignee dropdown by name', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      { id: 'a1', name: 'QA-bot', status: 'online' } as any,
    ])
    mockBoard()
    vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
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
      />,
    )
    const select = getByTestId('task-assignee').querySelector('select') as HTMLSelectElement
    await waitFor(() => {
      const opts = Array.from(select.options).map((o) => o.value)
      expect(opts).toContain('agent:a1')
    })
  })

  it('surfaces api errors via onError', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    mockBoard()
    vi.spyOn(api, 'patchTask').mockRejectedValue(new Error('boom'))
    const onError = vi.fn()
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType=""
        assigneeID=""
        taskID="t1"
        busy={false}
        projectID="p1"
        onChanged={() => {}}
        onError={onError}
      />,
    )
    const status = await waitFor(() => getByTestId('task-status') as HTMLSelectElement)
    fireEvent.change(status, { target: { value: 'done' } })
    await waitFor(() => expect(onError).toHaveBeenCalledWith('boom'))
  })

  // Phase 27.8.4: Inbox tasks have no project, hence no board —
  // Status renders as a read-only label, no select, no API call.
  it('renders Status as read-only when projectID is empty (Inbox)', async () => {
    const getBoardSpy = vi.spyOn(api, 'getBoard')
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const { queryByTestId, getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType=""
        assigneeID=""
        taskID="t1"
        busy={false}
        projectID=""
        onChanged={() => {}}
        onError={() => {}}
      />,
    )
    // No select — read-only instead.
    expect(queryByTestId('task-status')).toBeNull()
    expect(getByTestId('task-status-readonly')).toBeTruthy()
    expect(getByTestId('task-status-value').textContent).toBe('todo')
    // And the component must not have tried to fetch a board.
    expect(getBoardSpy).not.toHaveBeenCalled()
  })
})
