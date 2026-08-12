// @vitest-environment jsdom
/**
 * SearchPage thin-coverage smoke tests.
 *
 *   - Initial placeholder ("Run a search to see results.").
 *   - Submitting with a non-empty query fires /api/v1/search.
 *   - Empty results render "No matches."
 *   - Page hits render as <Link> to /wiki/:slug; task/comment hits
 *     use TaskLink (we just assert the title appears).
 *   - Search button is disabled while busy.
 *   - Cmd+K focuses the input.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SearchPage } from '@/features/search/SearchPage'

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
      <SearchPage />
    </MemoryRouter>,
  )
}

function makeHit(overrides: { id?: string; type?: 'page' | 'task' | 'comment'; title?: string; score?: number; snippet?: string } = {}): {
  id: string
  type: 'page' | 'task' | 'comment'
  title: string
  score: number
  snippet: string
} {
  return {
    id: overrides.id ?? 'h-1',
    type: overrides.type ?? 'page',
    title: overrides.title ?? 'Result',
    score: overrides.score ?? 1.23,
    snippet: overrides.snippet ?? '<em>match</em> text',
  }
}

describe('SearchPage', () => {
  it('renders the placeholder before any search is run', () => {
    mount()
    expect(screen.getByText('Run a search to see results.')).toBeTruthy()
  })

  it('disables Search until the query is non-empty', () => {
    mount()
    const btn = screen.getByRole('button', { name: /search/i }) as HTMLButtonElement
    expect(btn.disabled).toBe(true)

    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: '   ' },
    })
    expect(btn.disabled).toBe(true)

    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: 'hello' },
    })
    expect(btn.disabled).toBe(false)
  })

  it('does not submit when the trimmed query is empty', async () => {
    mount()
    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: '   ' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    // run() short-circuits without calling api.search.
    expect(stubHttp.get).not.toHaveBeenCalled()
  })

  it('fires api.search with the trimmed query and renders hits', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { hits: [makeHit({ title: 'Found page' })] },
    })

    mount()
    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: '  hello  ' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(stubHttp.get).toHaveBeenCalledWith('/api/v1/search', {
        params: { q: 'hello', limit: 50 },
      })
    })
    expect(await screen.findByText('Found page')).toBeTruthy()
  })

  it('renders "No matches." when the server returns zero hits', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: { hits: [] } })

    mount()
    fireEvent.change(screen.getByRole('textbox', { name: '' }), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    expect(await screen.findByText('No matches.')).toBeTruthy()
  })

  it('page hits link to /wiki/:slug; task/comment hits use the TaskLink path', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        hits: [
          makeHit({ id: 'page-slug', type: 'page', title: 'Wiki Result' }),
          makeHit({ id: 'task-1', type: 'task', title: 'Task Result' }),
        ],
      },
    })

    mount()
    fireEvent.change(screen.getByRole('textbox', { name: '' }), { target: { value: 'q' } })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    expect(await screen.findByText('Wiki Result')).toBeTruthy()
    expect(screen.getByText('Task Result')).toBeTruthy()
    // Page hit becomes a plain Link (anchor with href); task hits
    // use TaskLink which is also an anchor. Either way we should
    // see at least one anchor per hit.
    const pageLink = screen.getByText('Wiki Result').closest('a')
    expect(pageLink?.getAttribute('href')).toBe('/wiki/page-slug')
  })

  it('surfaces an inline error when /search rejects', async () => {
    stubHttp.get.mockRejectedValueOnce(new Error('boom'))

    mount()
    fireEvent.change(screen.getByRole('textbox', { name: '' }), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    expect(await screen.findByText('boom')).toBeTruthy()
  })

  it('Cmd+K focuses the search input', () => {
    mount()
    const input = screen.getByRole('textbox', { name: '' })
    fireEvent.keyDown(document.body, { key: 'k', metaKey: true })
    expect(document.activeElement).toBe(input)
  })
})
