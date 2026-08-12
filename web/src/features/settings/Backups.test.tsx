// @vitest-environment jsdom
/**
 * BackupsSettingsPage thin-coverage smoke tests.
 *
 * The settings panel is read-only (config.yaml is the source of truth),
 * but the manual actions (Test push, Snapshot now, Restore) and the
 * data summaries (settings, snapshots, log) are what we pin.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { BackupsSettingsPage } from '@/features/settings/Backups'

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

function stubDefaults(overrides: {
  settings?: object
  snapshots?: object[]
  log?: object[]
} = {}) {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/backups/settings') {
      return Promise.resolve({
        data: overrides.settings ?? {
          enabled: true,
          remote_url: 'git@github.com:foo/bar.git',
          has_auth: true,
        },
      })
    }
    if (url === '/api/v1/backups/snapshots') {
      return Promise.resolve({ data: { snapshots: overrides.snapshots ?? [] } })
    }
    if (url === '/api/v1/backups/log') {
      return Promise.resolve({ data: { log: overrides.log ?? [] } })
    }
    return Promise.resolve({ data: {} })
  })
}

describe('BackupsSettingsPage', () => {
  it('renders the read-only settings panel', async () => {
    stubDefaults()

    render(<BackupsSettingsPage />)

    expect(await screen.findByText('git@github.com:foo/bar.git')).toBeTruthy()
    // Both "Enabled" and "Auth configured" are 'yes' with this stub;
    // assert the count to disambiguate from siblings.
    expect(screen.getAllByText('yes').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('data/config.yaml')).toBeTruthy()
  })

  it('renders the empty state for snapshots when none exist', async () => {
    stubDefaults()

    render(<BackupsSettingsPage />)

    expect(await screen.findByText('No snapshots yet.')).toBeTruthy()
    expect(screen.getByText('No log entries yet.')).toBeTruthy()
  })

  it('renders one row per snapshot', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/2026-08-12.db',
          size: 1024 * 1024,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    })

    render(<BackupsSettingsPage />)

    expect(await screen.findByText('Snapshots (1)')).toBeTruthy()
    expect(screen.getByText('/data/snapshots/2026-08-12.db')).toBeTruthy()
  })

  it('Test push shows the success banner and the test endpoint was hit', async () => {
    stubDefaults()
    stubHttp.post.mockResolvedValueOnce({ data: { status: 'ok' } })

    render(<BackupsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /test push/i }))

    expect(await screen.findByText('Push succeeded.')).toBeTruthy()
    expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/backups/test', {})
  })

  it('Test push maps a no_remote error to the inline hint', async () => {
    stubDefaults()
    stubHttp.post.mockRejectedValueOnce(new Error('no_remote: missing remote'))

    render(<BackupsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /test push/i }))

    expect(await screen.findByText(/No git remote configured/)).toBeTruthy()
  })

  it('Snapshot now shows the path and triggers a reload', async () => {
    stubDefaults()
    stubHttp.post.mockResolvedValueOnce({ data: { path: '/data/snapshots/new.db' } })

    render(<BackupsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /snapshot now/i }))

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/backups/snapshot', {})
    })
    expect(await screen.findByText(/Snapshot written: \/data\/snapshots\/new\.db/)).toBeTruthy()
  })

  it('Restore opens a modal with the CLI hint', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/2026-08-12.db',
          size: 1024,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    })
    // restoreBackup rejects with an error message containing a hint.
    stubHttp.post.mockRejectedValueOnce(
      new Error('{"hint": "orenda backup restore --from /data/snapshots/2026-08-12.db --yes"}'),
    )

    render(<BackupsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /restore/i }))

    expect(await screen.findByText('Restore from snapshot')).toBeTruthy()
    expect(
      screen.getByText(/orenda backup restore --from \/data\/snapshots\/2026-08-12\.db --yes/),
    ).toBeTruthy()
  })

  it('Restore falls back to a default hint when the server does not include one', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/x.db',
          size: 0,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    })
    stubHttp.post.mockRejectedValueOnce(new Error('plain error, no hint'))

    render(<BackupsSettingsPage />)
    fireEvent.click(await screen.findByRole('button', { name: /restore/i }))

    expect(await screen.findByText(/Restore from snapshot/)).toBeTruthy()
    expect(screen.getByText(/orenda backup restore --from \/data\/snapshots\/x\.db/)).toBeTruthy()
  })
})
