// @vitest-environment jsdom
/**
 * BotsSettingsPage thin-coverage smoke tests.
 *
 *   - Empty subscriptions state.
 *   - "Add subscription" opens the form with the default event
 *     (task.review_needed) selected.
 *   - Toggling an event checkbox adds/removes it from the list.
 *   - Submitting the form calls createSubscription with the right
 *     payload; the success banner shows up.
 *   - Delete calls deleteSubscription and reloads the list.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { BotsSettingsPage } from '@/features/settings/Bots'

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

function stubSubs(subs: unknown[] = []) {
  stubHttp.get.mockResolvedValue({ data: { subscriptions: subs } })
}

describe('BotsSettingsPage', () => {
  it('renders the empty state when there are no subscriptions', async () => {
    stubSubs([])

    render(<BotsSettingsPage />)

    expect(await screen.findByText('No subscriptions yet.')).toBeTruthy()
  })

  it('renders one row per subscription', async () => {
    stubSubs([
      {
        id: 's-1',
        bot_type: 'webhook',
        target_address: 'https://example.com/hook',
        events: ['task.review_needed'],
        enabled: true,
      },
      {
        id: 's-2',
        bot_type: 'email',
        target_address: 'me@x.com',
        events: ['mention.created', 'agent.offline'],
        enabled: true,
      },
    ])

    render(<BotsSettingsPage />)

    expect(await screen.findByText('https://example.com/hook')).toBeTruthy()
    expect(screen.getByText('me@x.com')).toBeTruthy()
    expect(screen.getByText('mention.created, agent.offline')).toBeTruthy()
  })

  it('opens the create form and pre-selects task.review_needed', async () => {
    stubSubs([])

    render(<BotsSettingsPage />)

    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }))

    // The default selection includes task.review_needed.
    const checkbox = screen.getByRole('checkbox', { name: /task\.review_needed/ }) as HTMLInputElement
    expect(checkbox.checked).toBe(true)
  })

  it('toggling an event adds and removes it from the selection', async () => {
    stubSubs([])

    render(<BotsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }))

    const mention = screen.getByRole('checkbox', { name: /mention\.created/ }) as HTMLInputElement
    expect(mention.checked).toBe(false)
    fireEvent.click(mention)
    expect(mention.checked).toBe(true)
    fireEvent.click(mention)
    expect(mention.checked).toBe(false)
  })

  it('submitting the form posts createSubscription with the payload', async () => {
    stubSubs([])
    stubHttp.post.mockResolvedValueOnce({
      data: {
        id: 's-new',
        bot_type: 'telegram',
        target_address: '123',
        events: ['task.review_needed', 'mention.created'],
        enabled: true,
      },
    })

    render(<BotsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }))

    // Change bot type to telegram so the placeholder makes sense.
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'telegram' } })
    fireEvent.change(screen.getByPlaceholderText(/chat id/i), { target: { value: '123' } })
    // Add mention.created.
    fireEvent.click(screen.getByRole('checkbox', { name: /mention\.created/ }))
    // Submit (the form has a button labelled "Add subscription" inside).
    const formButton = screen.getAllByRole('button', { name: /add subscription/i })[0]
    fireEvent.click(formButton)

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/notifications/subscriptions', {
        bot_type: 'telegram',
        target_address: '123',
        events: ['task.review_needed', 'mention.created'],
        enabled: true,
      })
    })
    expect(await screen.findByText('Subscription added.')).toBeTruthy()
  })

  it('surfaces an inline error when createSubscription rejects', async () => {
    stubSubs([])
    stubHttp.post.mockRejectedValueOnce(new Error('boom'))

    render(<BotsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }))
    // Switch to telegram so the target placeholder is "chat id".
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'telegram' } })
    fireEvent.change(screen.getByPlaceholderText(/chat id/i), { target: { value: 'x' } })

    const formButton = screen.getAllByRole('button', { name: /add subscription/i })[0]
    fireEvent.click(formButton)

    expect(await screen.findByText('boom')).toBeTruthy()
  })

  it('Delete removes a subscription via the api', async () => {
    stubSubs([
      {
        id: 's-1',
        bot_type: 'webhook',
        target_address: 'https://x.com',
        events: ['task.review_needed'],
        enabled: true,
      },
    ])
    stubHttp.delete.mockResolvedValueOnce({ data: undefined })

    render(<BotsSettingsPage />)
    await screen.findByText('https://x.com')

    fireEvent.click(screen.getByRole('button', { name: /delete/i }))

    await waitFor(() => {
      expect(stubHttp.delete).toHaveBeenCalledWith('/api/v1/notifications/subscriptions/s-1')
    })
  })

  it('surfaces an inline error when listSubscriptions rejects', async () => {
    stubHttp.get.mockRejectedValueOnce(new Error('boom'))

    render(<BotsSettingsPage />)

    expect(await screen.findByText('boom')).toBeTruthy()
  })
})