// @vitest-environment jsdom
/**
 * wiki:project-wiki-link — UI smoke for the wiki_slug field on
 * ProjectSettingsTab and the "Open wiki page" link on the project
 * header.
 *
 * The autocomplete is a native <datalist>; we don't drive the
 * dropdown here — we only assert the input renders and the save
 * round-trips through updateProject. The dropdown UX is browser-
 * native; no JS state to test.
 *
 * The header link is a plain <a href>; we assert it appears only
 * when wiki_slug is set.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import { ProjectSettingsTab } from './tabs/ProjectSettingsTab';
import { ProjectDetailPage } from './ProjectDetailPage';
import { api } from '@/shared/api/client';

const baseProject = {
  id: 'p-wiki',
  name: 'Wiki Proj',
  color: '#3b82f6',
  description: 'copy',
  owner_id: 'u-1',
  archived: false,
  created_at: '2026-08-19T12:00:00Z',
  updated_at: '2026-08-19T12:00:00Z',
};

describe('ProjectSettingsTab — wiki_slug field', () => {
  beforeEach(() => {
    vi.spyOn(api, 'getProject').mockResolvedValue(baseProject);
    vi.spyOn(api, 'listPages').mockResolvedValue({
      tree: [
        {
          page: {
            id: 'w-1',
            slug: 'roadmap',
            title: 'Roadmap',
            position: 0,
            created_at: '2026-08-19T12:00:00Z',
            updated_at: '2026-08-19T12:00:00Z',
          },
          children: [],
        },
        {
          page: {
            id: 'w-2',
            slug: 'adr',
            title: 'Decision log',
            position: 1,
            created_at: '2026-08-19T12:00:00Z',
            updated_at: '2026-08-19T12:00:00Z',
          },
          children: [],
        },
      ],
    });
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  function renderTab(): void {
    render(
      <MemoryRouter initialEntries={['/projects/p-wiki/settings']}>
        <Routes>
          <Route path="/projects/:id/settings" element={<ProjectSettingsTab />} />
        </Routes>
      </MemoryRouter>,
    );
  }

  it('renders an empty wiki_slug input + datalist autocomplete by default', async () => {
    renderTab();
    const input = (await screen.findByLabelText(/Wiki page slug/i)) as HTMLInputElement;
    expect(input.value).toBe('');
    // <datalist><option> is not an a11y role, so query the DOM directly.
    const dataList = document.getElementById('wiki-slug-options') as HTMLDataListElement | null;
    expect(dataList).not.toBeNull();
    const slugs = Array.from(dataList!.options).map((o) => o.value);
    expect(slugs).toEqual(expect.arrayContaining(['roadmap', 'adr']));
  });

  it('preloads the existing wiki_slug from the project row', async () => {
    vi.spyOn(api, 'getProject').mockResolvedValue({ ...baseProject, wiki_slug: 'roadmap' });
    renderTab();
    const input = (await screen.findByLabelText(/Wiki page slug/i)) as HTMLInputElement;
    expect(input.value).toBe('roadmap');
  });

  it('saves wiki_slug via updateProject on the wiki section save button', async () => {
    const updateSpy = vi
      .spyOn(api, 'updateProject')
      .mockResolvedValue({ ...baseProject, wiki_slug: 'adr' });
    renderTab();
    const input = await screen.findByLabelText(/Wiki page slug/i);
    fireEvent.change(input, { target: { value: 'adr' } });
    // The settings page has multiple "Save changes" buttons (description,
    // wiki, etc.). Pick the one inside the wiki section by scoping to the
    // heading above it.
    const wikiHeading = await screen.findByRole('heading', { name: /Wiki page/i });
    const wikiSection = wikiHeading.closest('section') as HTMLElement;
    const save = wikiSection.querySelector('button[type="button"]') as HTMLButtonElement;
    fireEvent.click(save);
    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(
        'p-wiki',
        expect.objectContaining({ wiki_slug: 'adr' }),
      );
    });
  });
});

describe('ProjectDetailPage — wiki page header link', () => {
  beforeEach(() => {
    vi.spyOn(api, 'listProjects').mockResolvedValue([baseProject]);
    vi.spyOn(api, 'getBoard').mockResolvedValue({
      board: {
        id: 'b-1',
        project_id: 'p-wiki',
        name: 'Main',
        position: 0,
        created_at: '2026-08-19T12:00:00Z',
      },
      columns: [],
    });
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  function renderPage(): void {
    render(
      <MemoryRouter initialEntries={['/projects/p-wiki']}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />}>
            <Route index element={<div>KANBAN</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
  }

  it('shows the wiki link when wiki_slug is set', async () => {
    vi.spyOn(api, 'getProject').mockResolvedValue({ ...baseProject, wiki_slug: 'roadmap' });
    renderPage();
    const link = (await screen.findByText(/Wiki page/)) as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe('/pages/roadmap');
  });

  it('hides the wiki link when wiki_slug is empty', async () => {
    vi.spyOn(api, 'getProject').mockResolvedValue({ ...baseProject, wiki_slug: '' });
    renderPage();
    // Wait for project to render then assert the link is absent.
    await screen.findByRole('button', { name: /Wiki Proj/ });
    expect(screen.queryByText(/Wiki page/)).toBeNull();
  });
});
