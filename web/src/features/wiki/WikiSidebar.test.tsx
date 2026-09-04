// @vitest-environment jsdom
/**
 * WikiSidebar search tests (T103).
 *
 * One input, two modes:
 *   - instant client-side tree filter by title (ancestors of a match
 *     stay visible);
 *   - debounced (~300ms) FTS via api.search({type:'page'}) rendered as
 *     a "Content matches" section with <mark> snippets; clicking a hit
 *     navigates to /wiki/<slug>.
 *
 * Pins:
 *   (a) typing filters the tree instantly;
 *   (b) debounce: /api/v1/search called once with the right params;
 *   (c) click on a hit navigates to /wiki/<slug>;
 *   (d) clearing the query hides the FTS section;
 *   (e) the last response wins over a slower earlier one.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { WikiPage } from '@/features/wiki/WikiPage';

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

const TREE = [
  {
    page: { id: 'p1', slug: 'orenda-architecture', title: 'Orenda architecture', position: 0 },
    children: [
      {
        page: { id: 'p2', slug: 'search-notes', title: 'Search notes', position: 0 },
      },
    ],
  },
  {
    page: { id: 'p3', slug: 'cooking', title: 'Cooking recipes', position: 1 },
  },
];

function stubTree() {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/pages') return Promise.resolve({ data: { tree: TREE } });
    return Promise.resolve({ data: {} });
  });
}

function stubSearch(hits: unknown[]) {
  stubHttp.get.mockImplementation((url: string, config?: { params?: Record<string, unknown> }) => {
    if (url === '/api/v1/search') return Promise.resolve({ data: { hits, total: hits.length } });
    if (url === '/api/v1/pages') return Promise.resolve({ data: { tree: TREE } });
    void config;
    return Promise.resolve({ data: {} });
  });
}

// Renders a location probe so tests can assert the router URL.
function LocationProbe(): JSX.Element {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}</div>;
}

function mount(initialEntry: string) {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <Routes>
        <Route
          path="/wiki"
          element={
            <>
              <LocationProbe />
              <WikiPage />
            </>
          }
        />
        <Route
          path="/wiki/:slug"
          element={
            <>
              <LocationProbe />
              <WikiPage />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  );
}

function searchInput(): HTMLInputElement {
  return screen.getByRole('textbox', { name: 'Search wiki' }) as HTMLInputElement;
}

describe('WikiSidebar search', () => {
  it('filters the tree instantly by title, keeping ancestors of matches', async () => {
    stubTree();
    mount('/wiki');
    await screen.findByText('Orenda architecture');

    fireEvent.change(searchInput(), { target: { value: 'search' } });

    // Child "Search notes" matches; parent stays visible.
    expect(screen.getByText('Search notes')).toBeTruthy();
    expect(screen.getByText('Orenda architecture')).toBeTruthy();
    // Unrelated branch is hidden.
    expect(screen.queryByText('Cooking recipes')).toBeNull();
  });

  it('shows no results state when the filter matches nothing', async () => {
    stubTree();
    mount('/wiki');
    await screen.findByText('Orenda architecture');

    fireEvent.change(searchInput(), { target: { value: 'zzz-no-such' } });

    expect(screen.queryByText('Orenda architecture')).toBeNull();
    expect(screen.queryByText('Cooking recipes')).toBeNull();
  });

  it('debounces ~300ms and calls api.search with page type and limit 20', async () => {
    vi.useFakeTimers();
    try {
      stubTree();
      stubSearch([
        {
          type: 'page',
          id: 'p1',
          slug: 'orenda-architecture',
          title: 'Orenda architecture',
          snippet: 'plain <mark>architecture</mark> tail',
          score: 1.5,
        },
      ]);
      mount('/wiki');
      await vi.advanceTimersByTimeAsync(0);
      expect(screen.getByText('Orenda architecture')).toBeTruthy();

      fireEvent.change(searchInput(), { target: { value: 'архитектура' } });
      expect(stubHttp.get).not.toHaveBeenCalledWith(
        '/api/v1/search',
        expect.objectContaining({ params: { q: 'архитектура', type: 'page', limit: 20 } }),
      );

      await vi.advanceTimersByTimeAsync(300);
      expect(stubHttp.get).toHaveBeenCalledWith('/api/v1/search', {
        params: { q: 'архитектура', type: 'page', limit: 20 },
      });
    } finally {
      vi.useRealTimers();
    }
  });

  it('renders Content matches with mark highlighting and navigates to /wiki/<slug> on click', async () => {
    stubTree();
    stubSearch([
      {
        type: 'page',
        id: 'p2',
        slug: 'search-notes',
        title: 'Search notes',
        snippet: 'body with <mark>needle</mark> inside',
        score: 2.5,
      },
    ]);
    mount('/wiki');
    await screen.findByText('Orenda architecture');

    fireEvent.change(searchInput(), { target: { value: 'needle' } });

    expect(await screen.findByText('Content matches')).toBeTruthy();
    // Snippet renders <mark> as an element, neighbours as plain text.
    expect(screen.getByText('needle').closest('mark')).not.toBeNull();
    expect(screen.getByText(/body with/)).toBeTruthy();
    expect(screen.getByText(/inside/)).toBeTruthy();

    fireEvent.click(screen.getByText('Search notes'));
    await waitFor(() => {
      expect(screen.getByTestId('location').textContent).toBe('/wiki/search-notes');
    });
  });

  it('falls back to /wiki/<id> when a hit has no slug', async () => {
    stubTree();
    stubSearch([
      {
        type: 'page',
        id: 'p9',
        title: 'Legacy orphan',
        snippet: 'no slug here',
        score: 1,
      },
    ]);
    mount('/wiki');
    await screen.findByText('Orenda architecture');

    fireEvent.change(searchInput(), { target: { value: 'legacy' } });
    expect(await screen.findByText('Content matches')).toBeTruthy();

    fireEvent.click(screen.getByText('Legacy orphan'));
    await waitFor(() => {
      expect(screen.getByTestId('location').textContent).toBe('/wiki/p9');
    });
  });

  it('hides the Content matches section when the query is cleared', async () => {
    stubTree();
    stubSearch([
      {
        type: 'page',
        id: 'p1',
        slug: 'orenda-architecture',
        title: 'Orenda architecture',
        snippet: '<mark>hit</mark>',
        score: 1,
      },
    ]);
    mount('/wiki');
    await screen.findByText('Orenda architecture');

    fireEvent.change(searchInput(), { target: { value: 'hit' } });
    expect(await screen.findByText('Content matches')).toBeTruthy();

    fireEvent.change(searchInput(), { target: { value: '' } });
    await waitFor(() => {
      expect(screen.queryByText('Content matches')).toBeNull();
    });
    // The tree returns unfiltered.
    expect(screen.getByText('Cooking recipes')).toBeTruthy();
  });

  it('last response wins when an earlier request resolves late', async () => {
    vi.useFakeTimers();
    try {
      stubTree();
      let resolveSlow: (v: { data: unknown }) => void = () => {};
      const slow = new Promise<{ data: unknown }>((resolve) => {
        resolveSlow = resolve;
      });
      stubHttp.get.mockImplementation((url: string) => {
        if (url === '/api/v1/pages') return Promise.resolve({ data: { tree: TREE } });
        if (url === '/api/v1/search') return slow;
        return Promise.resolve({ data: {} });
      });

      mount('/wiki');
      await vi.advanceTimersByTimeAsync(0);
      expect(screen.getByText('Orenda architecture')).toBeTruthy();

      // First query — its response is held back.
      fireEvent.change(searchInput(), { target: { value: 'slow' } });
      await vi.advanceTimersByTimeAsync(300);

      // Second query — resolves immediately with its own hits.
      fireEvent.change(searchInput(), { target: { value: 'fresh' } });
      stubHttp.get.mockImplementation((url: string) => {
        if (url === '/api/v1/pages') return Promise.resolve({ data: { tree: TREE } });
        if (url === '/api/v1/search') {
          return Promise.resolve({
            data: {
              hits: [
                {
                  type: 'page',
                  id: 'p3',
                  slug: 'cooking',
                  title: 'Cooking recipes',
                  snippet: '<mark>fresh</mark> content',
                  score: 9,
                },
              ],
              total: 1,
            },
          });
        }
        return Promise.resolve({ data: {} });
      });
      await vi.advanceTimersByTimeAsync(300);
      expect(screen.getByText('Content matches')).toBeTruthy();
      expect(screen.getByText('Cooking recipes')).toBeTruthy();

      // The stale reply lands last in wall-clock time — it must be ignored.
      resolveSlow({
        data: {
          hits: [
            {
              type: 'page',
              id: 'px',
              slug: 'stale',
              title: 'Stale page',
              snippet: 'old <mark>slow</mark>',
              score: 1,
            },
          ],
          total: 1,
        },
      });
      await vi.advanceTimersByTimeAsync(0);
      expect(screen.queryByText('Stale page')).toBeNull();
      expect(screen.getByText('Cooking recipes')).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });
});
