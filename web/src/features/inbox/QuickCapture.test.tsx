// @vitest-environment jsdom
/**
 * QuickCapture component tests.
 *
 * Global capture modal triggered by:
 *   - The 'q' hotkey (anywhere except inside text inputs)
 *   - The Cmd/Ctrl+K alias
 *   - The "+" button in the corner (data-testid="quick-capture-toggle")
 *
 * Submit creates an Inbox task via api.createInboxTask, then shows a
 * success toast with two actions: "Open task" (navigates to
 * /tasks/:id) and "Dismiss". Esc closes the modal.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { QuickCapture } from '@/features/inbox/QuickCapture'

const { stubHttp } = vi.hoisted(() => ({
  stubHttp: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
    interceptors: { response: { use: vi.fn() } },
  },
}))

vi.mock('axios', async (importOriginal) => {
  const actual = await importOriginal<typeof import('axios')>()
  return {
    ...actual,
    default: { ...actual.default, create: vi.fn(() => stubHttp) },
  }
})

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  cleanup()
})

function mount() {
  return render(
    <MemoryRouter>
      <QuickCapture />
    </MemoryRouter>,
  )
}

describe('QuickCapture', () => {
  it('does not render the modal until triggered', () => {
    mount()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens the modal when the "+" button is clicked', () => {
    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('opens the modal when the "q" hotkey fires outside an input', () => {
    mount()
    fireEvent.keyDown(document.body, { key: 'q' })
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('opens the modal on Cmd+K', () => {
    mount()
    fireEvent.keyDown(document.body, { key: 'k', ctrlKey: true })
    expect(screen.getByRole('dialog')).toBeTruthy()
  })

  it('does NOT open the modal when the user is typing in an input', () => {
    mount()
    const input = document.createElement('input')
    document.body.appendChild(input)
    input.focus()
    fireEvent.keyDown(input, { key: 'q' })
    expect(screen.queryByRole('dialog')).toBeNull()
    document.body.removeChild(input)
  })

  it('closes the modal when Escape is pressed inside it', () => {
    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toBeTruthy()

    // Esc fires on the textarea (which is inside the dialog); the
    // component wires Esc to close.
    const textarea = screen.getByTestId('quick-capture-input')
    fireEvent.keyDown(textarea, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('submits via api.createInboxTask and shows the success toast', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'Capture me', status: 'todo', priority: 'medium', awaiting: 'none' },
    })

    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: 'Capture me' },
    })
    fireEvent.click(screen.getByTestId('quick-capture-submit'))

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'Capture me' })
    })
    expect(await screen.findByTestId('quick-capture-toast')).toBeTruthy()
    expect(screen.getByText('✓ Captured to Inbox')).toBeTruthy()
  })

  it('disables the submit button while the request is in flight', async () => {
    let resolveCreate!: (v: unknown) => void
    stubHttp.post.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveCreate = resolve
      }),
    )

    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: 'pending' },
    })

    const submit = screen.getByTestId('quick-capture-submit')
    fireEvent.click(submit)

    await waitFor(() => {
      expect((submit as HTMLButtonElement).disabled).toBe(true)
    })

    resolveCreate({ data: { id: 'new-1', title: 'pending' } })
  })

  it('trims whitespace before submitting', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'trimmed', status: 'todo', priority: 'medium', awaiting: 'none' },
    })

    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: '  trimmed  ' },
    })
    fireEvent.click(screen.getByTestId('quick-capture-submit'))

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'trimmed' })
    })
  })

  it('does not submit when the title is empty (whitespace-only)', async () => {
    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: '   ' },
    })

    // Submit is disabled while the trimmed title is empty.
    const submit = screen.getByTestId('quick-capture-submit') as HTMLButtonElement
    expect(submit.disabled).toBe(true)
    // Force a click anyway — submit() should early-return.
    fireEvent.click(submit)
    expect(stubHttp.post).not.toHaveBeenCalled()
  })

  it('renders the toast with "Open task" and "Dismiss" actions on success', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'Hi', status: 'todo', priority: 'medium', awaiting: 'none' },
    })

    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    fireEvent.change(screen.getByTestId('quick-capture-input'), { target: { value: 'Hi' } })
    fireEvent.click(screen.getByTestId('quick-capture-submit'))

    expect(await screen.findByText('Open task')).toBeTruthy()
    expect(screen.getByText('Dismiss')).toBeTruthy()
  })

  it('Cmd+Enter inside the textarea also submits', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'kbd', status: 'todo', priority: 'medium', awaiting: 'none' },
    })

    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    const textarea = screen.getByTestId('quick-capture-input')
    fireEvent.change(textarea, { target: { value: 'kbd' } })
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true })

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'kbd' })
    })
  })

  it('keeps the modal open when createInboxTask fails (so the user can retry)', async () => {
    // We don't render the error inline today — QuickCapture just
    // clears busy and lets the user retry. The contract pinned here
    // is that the modal stays open and no toast appears.
    stubHttp.post.mockRejectedValueOnce(new Error('boom'))

    mount()
    fireEvent.click(screen.getByTestId('quick-capture-toggle'))
    fireEvent.change(screen.getByTestId('quick-capture-input'), { target: { value: 'x' } })
    fireEvent.click(screen.getByTestId('quick-capture-submit'))

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalled()
    })
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(screen.queryByTestId('quick-capture-toast')).toBeNull()
  })
})
