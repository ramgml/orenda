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
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';

import { ProjectSettingsTab } from './tabs/ProjectSettingsTab';
import { ProjectDetailPage } from './ProjectDetailPage';
import { api, type Agent } from '@/shared/api/client';

const baseProject = {
  id: 'p-wiki',
  name: 'Wiki Proj',
  color: '#3b82f6',
  description: 'copy',
  owner_id: 'u-1',
  archived: false,
  agents_allowed: true,
  number: 1,
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
            number: 1,
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
            number: 2,
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
    expect(link.getAttribute('href')).toBe('/wiki/roadmap');
  });

  it('hides the wiki link when wiki_slug is empty', async () => {
    vi.spyOn(api, 'getProject').mockResolvedValue({ ...baseProject, wiki_slug: '' });
    renderPage();
    // Wait for project to render then assert the link is absent.
    await screen.findByRole('button', { name: /Wiki Proj/ });
    expect(screen.queryByText(/Wiki page/)).toBeNull();
  });
});

describe('ProjectSettingsTab — agent access', () => {
  // Full Agent shapes (the API registry type carries label/token
  // metadata the settings section never renders, but vi.spyOn pins
  // the declared types).
  const agentA: Agent = {
    id: 'a-1',
    name: 'Alpha',
    type: [],
    token_id: 'tok-a1',
    status: 'online',
    max_concurrent: 1,
    created_at: '2026-08-19T12:00:00Z',
  };
  const agentB: Agent = {
    id: 'a-2',
    name: 'Beta',
    type: [],
    token_id: 'tok-a2',
    status: 'offline',
    max_concurrent: 1,
    created_at: '2026-08-19T12:00:00Z',
  };

  // Restricted by default: matches the main render scenario —
  // agentsAllowed=false, first agent pre-granted, second not.
  function mockRestrictedProject(): void {
    vi.spyOn(api, 'getProject').mockResolvedValue({ ...baseProject, agents_allowed: false });
    vi.spyOn(api, 'listAgents').mockResolvedValue([agentA, agentB]);
    vi.spyOn(api, 'listProjectAgents').mockResolvedValue([agentA.id]);
  }

  beforeEach(() => {
    vi.spyOn(api, 'listPages').mockResolvedValue({ tree: [] });
    mockRestrictedProject();
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

  function agentAccessSection(): HTMLElement {
    const heading = screen.getByRole('heading', { name: /Agent access/i });
    return heading.closest('section') as HTMLElement;
  }

  it('renders the grant list with preloaded marks and the toggle off', async () => {
    renderTab();
    await screen.findByRole('heading', { name: /Agent access/i });
    const first = screen.getByLabelText(/Grant access: Alpha/i);
    const second = screen.getByLabelText(/Grant access: Beta/i);
    // Radix Checkbox renders <button role="checkbox"> — state lives in
    // aria-checked, not the input .checked property.
    expect(first.getAttribute('aria-checked')).toBe('true');
    expect(second.getAttribute('aria-checked')).toBe('false');
    const openToggle = screen.getByLabelText(/Open to all agents/i);
    expect(openToggle.getAttribute('aria-checked')).toBe('false');
    expect(first.hasAttribute('disabled')).toBe(false);
  });

  it('disables agent checkboxes while the project is open to all agents', async () => {
    renderTab();
    await screen.findByRole('heading', { name: /Agent access/i });
    fireEvent.click(screen.getByLabelText(/Open to all agents/i));
    const first = screen.getByLabelText(/Grant access: Alpha/i);
    const second = screen.getByLabelText(/Grant access: Beta/i);
    expect(first.hasAttribute('disabled')).toBe(true);
    expect(second.hasAttribute('disabled')).toBe(true);
  });

  it('saves agents_allowed via updateProject and the granted list via setProjectAgents', async () => {
    const updateSpy = vi
      .spyOn(api, 'updateProject')
      .mockResolvedValue({ ...baseProject, agents_allowed: false });
    const setSpy = vi.spyOn(api, 'setProjectAgents').mockResolvedValue(undefined);
    renderTab();
    await screen.findByRole('heading', { name: /Agent access/i });
    // Grant the second agent as well, then save the section.
    fireEvent.click(screen.getByLabelText(/Grant access: Beta/i));
    // The Save button is the only named button in the section — the
    // Radix checkboxes are also type="button", so query by role+name.
    fireEvent.click(within(agentAccessSection()).getByRole('button', { name: /Save changes/i }));
    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(
        'p-wiki',
        expect.objectContaining({ agents_allowed: false }),
      );
    });
    await waitFor(() => {
      expect(setSpy).toHaveBeenCalledWith('p-wiki', ['a-1', 'a-2']);
    });
  });

  it('surfaces a setProjectAgents failure in the error banner', async () => {
    vi.spyOn(api, 'updateProject').mockResolvedValue({ ...baseProject, agents_allowed: false });
    vi.spyOn(api, 'setProjectAgents').mockRejectedValue(new Error('grant write failed'));
    renderTab();
    await screen.findByRole('heading', { name: /Agent access/i });
    fireEvent.click(within(agentAccessSection()).getByRole('button', { name: /Save changes/i }));
    // The component renders errors in the shared red banner at the top
    // of the settings page.
    const banner = await screen.findByText(/grant write failed/i);
    expect(banner).toBeTruthy();
  });
});
