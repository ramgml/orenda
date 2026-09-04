// @vitest-environment jsdom
/**
 * SearchPage thin-coverage smoke tests.
 *
 *   - Initial placeholder ("Run a search to see results.").
 *   - Submitting with a non-empty query fires /api/v1/search.
 *   - Empty results render "No matches."
 *   - Page hits render as <Link> to /wiki/:slug; task/comment hits
 *     use TaskLink (we just assert the title appears).
 *   - Snippets render as plain text with `<mark>`/`</mark>` markers
 *     turned into highlight elements; HTML-ish content (e.g. an
 *     `<img onerror=...>` from a document body) must be escaped, not
 *     interpreted — raw HTML injection is gone for good.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { SearchPage } from '@/features/search/SearchPage';

const { stubHttp } = vi.hoisted(() => ({
  stubHttp: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
    interceptors: { response: { use: vi.fn() } },
  },
}));

vi.mock('axios', async (importOriginal) => {
  const actual = await importOriginal<typeof import('axios')>();
  return {
    ...actual,
    default: { ...actual.default, create: vi.fn(() => stubHttp) },
  };
});

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

function mount() {
  return render(
    <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <SearchPage />
    </MemoryRouter>,
  );
}

function makeHit(
  overrides: {
    id?: string;
    slug?: string;
    type?: 'page' | 'task' | 'comment';
    title?: string;
    score?: number;
    snippet?: string;
  } = {},
): {
  id: string;
  slug?: string;
  type: 'page' | 'task' | 'comment';
  title: string;
  score: number;
  snippet: string;
} {
  return {
    id: overrides.id ?? 'h-1',
    slug: overrides.slug,
    type: overrides.type ?? 'page',
    title: overrides.title ?? 'Result',
    score: overrides.score ?? 1.23,
    snippet: overrides.snippet ?? '<mark>match</mark> text',
  };
}

describe('SearchPage', () => {
  it('renders the placeholder before any search is run', () => {
    mount();
    expect(screen.getByText('Run a search to see results.')).toBeTruthy();
  });

  it('disables Search until the query is non-empty', () => {
    mount();
    const btn = screen.getByRole('button', { name: /search/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);

    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: '   ' },
    });
    expect(btn.disabled).toBe(true);

    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: 'hello' },
    });
    expect(btn.disabled).toBe(false);
  });

  it('does not submit when the trimmed query is empty', async () => {
    mount();
    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: '   ' },
    });
    fireEvent.click(screen.getByRole('button', { name: /search/i }));

    // run() short-circuits without calling api.search.
    expect(stubHttp.get).not.toHaveBeenCalled();
  });

  it('fires api.search with the trimmed query and renders hits', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: { hits: [makeHit({ title: 'Found page' })] },
    });

    mount();
    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: '  hello  ' },
    });
    fireEvent.click(screen.getByRole('button', { name: /search/i }));

    await waitFor(() => {
      expect(stubHttp.get).toHaveBeenCalledWith('/api/v1/search', {
        params: { q: 'hello', limit: 50 },
      });
    });
    expect(await screen.findByText('Found page')).toBeTruthy();
  });

  it('renders "No matches." when the server returns zero hits', async () => {
    stubHttp.get.mockResolvedValueOnce({ data: { hits: [] } });

    mount();
    fireEvent.change(screen.getByRole('textbox', { name: '' }), { target: { value: 'x' } });
    fireEvent.click(screen.getByRole('button', { name: /search/i }));

    expect(await screen.findByText('No matches.')).toBeTruthy();
  });

  it('page hits link to /wiki/:slug (id fallback); task/comment hits use the task path', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        hits: [
          makeHit({ id: 'page-uuid', slug: 'page-slug', type: 'page', title: 'Wiki Result' }),
          makeHit({ id: 'fallback-id', type: 'page', title: 'Legacy Result' }),
          makeHit({ id: 'task-1', type: 'task', title: 'Task Result' }),
          makeHit({ id: 'comment-1', type: 'comment', title: 'Comment Result' }),
        ],
      },
    });

    mount();
    fireEvent.change(screen.getByRole('textbox', { name: '' }), { target: { value: 'q' } });
    fireEvent.click(screen.getByRole('button', { name: /search/i }));

    expect(await screen.findByText('Wiki Result')).toBeTruthy();
    expect(screen.getByText('Legacy Result')).toBeTruthy();
    expect(screen.getByText('Comment Result')).toBeTruthy();
    // Page hits are plain links; slug wins when present, otherwise
    // the raw id is the fallback.
    expect(screen.getByText('Wiki Result').closest('a')?.getAttribute('href')).toBe(
      '/wiki/page-slug',
    );
    expect(screen.getByText('Legacy Result').closest('a')?.getAttribute('href')).toBe(
      '/wiki/fallback-id',
    );
    // Comment hits link to the parent task (no dedicated route).
    expect(screen.getByText('Comment Result').closest('a')?.getAttribute('href')).toBe(
      '/tasks/comment-1',
    );
  });

  it('surfaces an inline error when /search rejects', async () => {
    stubHttp.get.mockRejectedValueOnce(new Error('boom'));

    mount();
    fireEvent.change(screen.getByRole('textbox', { name: '' }), { target: { value: 'x' } });
    fireEvent.click(screen.getByRole('button', { name: /search/i }));

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('Cmd+K focuses the search input', () => {
    mount();
    const input = screen.getByRole('textbox', { name: '' });
    fireEvent.keyDown(document.body, { key: 'k', metaKey: true });
    expect(document.activeElement).toBe(input);
  });

  it('renders mark markers as highlight elements with plain-text neighbours', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        hits: [makeHit({ title: 'Highlight page', snippet: 'plain <mark>match</mark> tail' })],
      },
    });

    mount();
    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: 'match' },
    });
    fireEvent.click(screen.getByRole('button', { name: /search/i }));

    expect(await screen.findByText('match').then((el) => el.closest('mark'))).not.toBeNull();
    expect(screen.getByText(/plain/)).toBeTruthy();
    expect(screen.getByText(/tail/)).toBeTruthy();
  });

  it('escapes HTML in snippet text while keeping the mark highlight', async () => {
    stubHttp.get.mockResolvedValueOnce({
      data: {
        hits: [
          makeHit({
            title: 'XSS page',
            // What FTS5 snippet() emits when the document body itself
            // contains markup: content verbatim + the fixed markers.
            snippet: '<img src=x onerror=alert(1)> <mark>term</mark> <script>alert(2)</script>',
          }),
        ],
      },
    });

    const { container } = mount();
    fireEvent.change(screen.getByRole('textbox', { name: '' }), {
      target: { value: 'term' },
    });
    fireEvent.click(screen.getByRole('button', { name: /search/i }));

    expect(await screen.findByText('term')).toBeTruthy();
    // Highlight survives as a real element...
    const mark = container.querySelector('mark');
    expect(mark).not.toBeNull();
    expect(mark?.textContent).toBe('term');
    // ...while hostile content never becomes markup.
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('script')).toBeNull();
    const snippet = container.querySelector('p.text-sm') as HTMLElement;
    expect(snippet.innerHTML).toContain('&lt;img src=x onerror=alert(1)&gt;');
    expect(snippet.innerHTML).toContain('&lt;script&gt;alert(2)&lt;/script&gt;');
  });
});
