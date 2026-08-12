// @vitest-environment jsdom
/**
 * WikiPage thin-coverage smoke tests.
 *
 * The full Tiptap-based MarkdownEditor and the tree rendering are
 * best left to Playwright (they pull in heavy DOM machinery). Here
 * we pin only what's cheap:
 *   - Empty state ("No page selected") renders when the URL has no
 *     :slug param.
 *   - The "New page" form accepts a title; the slug auto-fills from
 *     slugify() and Create is disabled until the title is non-empty.
 *   - Slug validation: a non-conforming slug surfaces the inline
 *     error instead of submitting.
 *   - Empty tree state renders when /pages returns no children.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { WikiPage } from '@/features/wiki/WikiPage'

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

function stubPagesTree(tree: unknown[] = []) {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/pages') return Promise.resolve({ data: { tree } })
    return Promise.resolve({ data: {} })
  })
}

function mount(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route path="/wiki" element={<WikiPage />} />
        <Route path="/wiki/:slug" element={<WikiPage />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('WikiPage', () => {
  it('renders the empty state when no slug is in the URL', async () => {
    stubPagesTree([])

    mount('/wiki')

    expect(await screen.findByText('No page selected')).toBeTruthy()
    expect(screen.getByText(/Pick a page from the tree/)).toBeTruthy()
  })

  it('renders the "New page" form with auto-slug from title', async () => {
    stubPagesTree([])

    mount('/wiki')

    await screen.findByText('No page selected')

    const titleInput = screen.getByPlaceholderText('Page title (any language)')
    fireEvent.change(titleInput, { target: { value: 'Hello World' } })

    // The slug input's value should auto-derive from the title.
    const slugInput = screen.getByPlaceholderText('auto-generated') as HTMLInputElement
    expect(slugInput.value).toBe('hello-world')
  })

  it('Create button is disabled when the title is empty / whitespace-only', async () => {
    stubPagesTree([])

    mount('/wiki')
    await screen.findByText('No page selected')

    const create = screen.getByRole('button', { name: /create/i }) as HTMLButtonElement
    expect(create.disabled).toBe(true)

    fireEvent.change(screen.getByPlaceholderText('Page title (any language)'), {
      target: { value: '   ' },
    })
    expect(create.disabled).toBe(true)

    fireEvent.change(screen.getByPlaceholderText('Page title (any language)'), {
      target: { value: 'Real' },
    })
    expect(create.disabled).toBe(false)
  })

  it('rejects a slug with disallowed characters and shows an inline error', async () => {
    stubPagesTree([])

    mount('/wiki')
    await screen.findByText('No page selected')

    fireEvent.change(screen.getByPlaceholderText('Page title (any language)'), {
      target: { value: 'Anything' },
    })
    const slugInput = screen.getByPlaceholderText('auto-generated')
    fireEvent.change(slugInput, { target: { value: 'bad slug with spaces' } })
    fireEvent.click(screen.getByRole('button', { name: /create/i }))

    expect(await screen.findByText('Slug must contain only [a-z0-9_-].')).toBeTruthy()
    // The savePage endpoint should NOT have been called.
    expect(stubHttp.post).not.toHaveBeenCalled()
  })

  it('calls savePage on Create with the auto-derived slug', async () => {
    stubPagesTree([])
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'p-1', slug: 'my-page', title: 'My Page', content_md: '' },
    })

    mount('/wiki')
    await screen.findByText('No page selected')

    fireEvent.change(screen.getByPlaceholderText('Page title (any language)'), {
      target: { value: 'My Page' },
    })
    fireEvent.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/pages', {
        slug: 'my-page',
        title: 'My Page',
        content_md: '',
      })
    })
  })

  it('maps a slug_taken error to "A page with this slug already exists."', async () => {
    stubPagesTree([])
    stubHttp.post.mockRejectedValueOnce(new Error('slug_taken'))

    mount('/wiki')
    await screen.findByText('No page selected')

    fireEvent.change(screen.getByPlaceholderText('Page title (any language)'), {
      target: { value: 'Dup' },
    })
    fireEvent.click(screen.getByRole('button', { name: /create/i }))

    expect(await screen.findByText(/already exists/)).toBeTruthy()
  })
})