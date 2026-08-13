// @vitest-environment jsdom
/**
 * TaskFieldControls tests (Phase 27.7).
 *
 * Pins three contracts:
 *   1. Status / Priority selects render with the canonical enums.
 *   2. Changing a select PATCHes the task through api.patchTask.
 *   3. The Assignee dropdown maps "Unassigned" / "Me" / each agent
 *      onto the wire shape (assignee_type, assignee_id) and sends
 *      an empty pair for "Unassigned".
 *
 * AuthContext is mocked by providing a wrapper component that sets
 * the user. We don't bother with the full AuthProvider — the
 * component reads only `useAuth().user`, which is what we stub.
 */
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TaskFieldControls } from '@/features/tasks/TaskFieldControls'
import { api } from '@/shared/api/client'

// AuthContext mock: provide a user with id 'u-me' so the "me"
// branch of the Assignee select is exercisable.
vi.mock('@/features/auth/AuthContext', () => ({
  useAuth: () => ({ user: { user_id: 'u-me', email: 'me@x.io', display_name: 'Me' } }),
}))

beforeEach(() => {
  vi.restoreAllMocks()
})

afterEach(() => {
  cleanup()
})

describe('TaskFieldControls', () => {
  it('renders all three controls with the current values', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType=""
        assigneeID=""
        taskID="t1"
        busy={false}
        onChanged={() => {}}
        onError={() => {}}
      />,
    )
    const status = getByTestId('task-status') as HTMLSelectElement
    const priority = getByTestId('task-priority') as HTMLSelectElement
    expect(status.value).toBe('todo')
    expect(priority.value).toBe('medium')
    // Assignee defaults to "unassigned" for an unassigned task.
    const assignee = getByTestId('task-assignee').querySelector('select') as HTMLSelectElement
    expect(assignee.value).toBe('unassigned')
  })

  it('Patches the task when status changes', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
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
        onChanged={onChanged}
        onError={() => {}}
      />,
    )
    fireEvent.change(getByTestId('task-status'), { target: { value: 'in_progress' } })
    await waitFor(() => expect(spy).toHaveBeenCalledTimes(1))
    expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ status: 'in_progress' }))
    expect(onChanged).toHaveBeenCalled()
  })

  it('Patches priority with the new value', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType=""
        assigneeID=""
        taskID="t1"
        busy={false}
        onChanged={() => {}}
        onError={() => {}}
      />,
    )
    fireEvent.change(getByTestId('task-priority'), { target: { value: 'urgent' } })
    await waitFor(() => expect(spy).toHaveBeenCalledWith('t1', expect.objectContaining({ priority: 'urgent' })))
  })

  it('Assignee "Unassigned" sends empty assignee pair', async () => {
    vi.spyOn(api, 'listAgents').mockResolvedValue([])
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType="agent"
        assigneeID="a1"
        taskID="t1"
        busy={false}
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
    const spy = vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType=""
        assigneeID=""
        taskID="t1"
        busy={false}
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
    vi.spyOn(api, 'patchTask').mockResolvedValue({} as never)
    const { getByTestId } = render(
      <TaskFieldControls
        status="todo"
        priority="medium"
        assigneeType=""
        assigneeID=""
        taskID="t1"
        busy={false}
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
        onChanged={() => {}}
        onError={onError}
      />,
    )
    fireEvent.change(getByTestId('task-status'), { target: { value: 'done' } })
    await waitFor(() => expect(onError).toHaveBeenCalledWith('boom'))
  })
})
