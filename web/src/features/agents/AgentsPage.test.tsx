// @vitest-environment jsdom
/**
 * AgentsPage thin-coverage smoke tests.
 *
 *   - Empty state when no agents exist.
 *   - Table renders one row per agent with the status pill colour
 *     driven by a.status.
 *   - The "New agent" button toggles the create form.
 *   - Submitting the create form posts createAgent and shows the
 *     plaintext API token exactly once (until Dismiss).
 *   - Delete requires window.confirm; accepted calls deleteAgent.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AuthProvider } from '@/features/auth/AuthContext'
import { AgentsPage } from '@/features/agents/AgentsPage'

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

/**
 * Configure the http stub for a test, then mount the page.
 *
 * Note: AgentsPage calls `load()` synchronously on first render
 * (no useEffect), so the stub MUST be set BEFORE render. We always
 * stub /api/v1/me (so AuthProvider lands in 'authenticated'); pass
 * `agents` to control what `listAgents` returns.
 */
function mount(agents: unknown[] = []) {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/me') {
      return Promise.resolve({
        data: { user_id: 'u-1', email: 'me@x.com', display_name: 'Me', role: 'owner' },
      })
    }
    if (url === '/api/v1/agents') return Promise.resolve({ data: { agents } })
    return Promise.resolve({ data: {} })
  })

  return render(
    <MemoryRouter>
      <AuthProvider>
        <AgentsPage />
      </AuthProvider>
    </MemoryRouter>,
  )
}

function makeAgent(overrides: Partial<{
  id: string
  name: string
  type: string
  status: string
  last_seen_at: string
  created_at: string
}> = {}): {
  id: string
  name: string
  type: string
  status: string
  last_seen_at: string | null
  created_at: string
} {
  return {
    id: 'a-1',
    name: 'agent-1',
    type: 'qwen',
    status: 'online',
    last_seen_at: '2026-08-12T10:00:00Z',
    created_at: '2026-08-12T09:00:00Z',
    ...overrides,
  }
}

describe('AgentsPage', () => {
  it('renders the empty state when there are no agents', async () => {
    mount()

    expect(
      await screen.findByText(/No agents yet\./),
    ).toBeTruthy()
  })

  it('renders one row per agent with the right status pill', async () => {
    mount([
      makeAgent({ id: 'a-1', name: 'first', status: 'online' }),
      makeAgent({ id: 'a-2', name: 'second', status: 'offline' }),
      makeAgent({ id: 'a-3', name: 'third', status: 'disabled' }),
    ])

    expect(await screen.findByText('first')).toBeTruthy()
    expect(screen.getByText('second')).toBeTruthy()
    expect(screen.getByText('third')).toBeTruthy()
    expect(screen.getByText('online')).toBeTruthy()
    expect(screen.getByText('offline')).toBeTruthy()
    expect(screen.getByText('disabled')).toBeTruthy()
  })

  it('"New agent" toggles the create form open', async () => {
    mount()
    await screen.findByText(/No agents yet\./)

    fireEvent.click(screen.getByRole('button', { name: /new agent/i }))
    expect(screen.getByPlaceholderText('Agent name (unique)')).toBeTruthy()

    // Toggle back closes the form.
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(screen.queryByPlaceholderText('Agent name (unique)')).toBeNull()
  })

  it('submitting the create form posts createAgent and reveals the token', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: {
        agent: makeAgent({ id: 'a-new', name: 'fresh-agent' }),
        plain_token: 'ort_secret_abc123',
      },
    })

    mount()
    await screen.findByText(/No agents yet\./)

    fireEvent.click(screen.getByRole('button', { name: /new agent/i }))
    fireEvent.change(screen.getByPlaceholderText('Agent name (unique)'), {
      target: { value: 'fresh-agent' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/agents', {
        name: 'fresh-agent',
        type: 'qwen',
        description: undefined,
      })
    })
    // Token reveal banner.
    expect(await screen.findByText(/copy now, it won/)).toBeTruthy()
    expect(screen.getByText('ort_secret_abc123')).toBeTruthy()
  })

  it('surfaces an inline error when createAgent rejects', async () => {
    stubHttp.post.mockRejectedValueOnce(new Error('boom'))

    mount()
    await screen.findByText(/No agents yet\./)
    fireEvent.click(screen.getByRole('button', { name: /new agent/i }))
    fireEvent.change(screen.getByPlaceholderText('Agent name (unique)'), {
      target: { value: 'x' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))

    expect(await screen.findByText('boom')).toBeTruthy()
  })

  it('Delete requires window.confirm; accepted calls deleteAgent', async () => {
    stubHttp.delete.mockResolvedValueOnce({ data: undefined })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)

    mount([makeAgent({ id: 'a-1', name: 'doomed' })])
    await screen.findByText('doomed')
    fireEvent.click(screen.getByRole('button', { name: /delete/i }))

    await waitFor(() => {
      expect(stubHttp.delete).toHaveBeenCalledWith('/api/v1/agents/a-1')
    })
    expect(confirmSpy).toHaveBeenCalledTimes(1)
  })

  it('Delete is a no-op when the user cancels the confirm', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false)

    mount([makeAgent({ id: 'a-1', name: 'kept' })])
    await screen.findByText('kept')

    fireEvent.click(screen.getByRole('button', { name: /delete/i }))

    expect(stubHttp.delete).not.toHaveBeenCalled()
    expect(screen.getByText('kept')).toBeTruthy()
  })
})