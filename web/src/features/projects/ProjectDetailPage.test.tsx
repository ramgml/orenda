// @vitest-environment jsdom
/**
 * Smoke tests for the inline-editable project name on the project
 * detail header (Phase 11.1).
 *
 * We render the page with a minimal MemoryRouter + stub project, then
 * drive the inline-edit interaction:
 *   • click the title → input appears with the current name pre-selected
 *   • Enter on a non-empty new name → triggers PATCH + onRename
 *   • Escape → reverts, no PATCH
 *
 * The tests stub the API client so they do not depend on a live
 * server. They exercise the most important regression risk: a silent
 * rename (e.g. a stale closure capturing the old name) is much easier
 * to catch now than after the UI ships.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';

import { ProjectDetailPage } from './ProjectDetailPage';
import { api } from '@/shared/api/client';

describe('ProjectDetailPage — inline rename', () => {
  const fakeProject = {
    id: 'p-1',
    name: 'Original',
    color: '#3b82f6',
    description: '',
    owner_id: 'u-1',
    archived: false,
    created_at: '2026-08-10T12:00:00Z',
    updated_at: '2026-08-10T12:00:00Z',
  };

  beforeEach(() => {
    vi.spyOn(api, 'getProject').mockResolvedValue(fakeProject);
    vi.spyOn(api, 'listProjects').mockResolvedValue([fakeProject]);
    vi.spyOn(api, 'getBoard').mockResolvedValue({
      board: {
        id: 'b-1',
        project_id: 'p-1',
        name: 'Main',
        position: 0,
        created_at: '2026-08-10T12:00:00Z',
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
      <MemoryRouter initialEntries={['/projects/p-1']}>
        <Routes>
          <Route path="/projects/:id" element={<ProjectDetailPage />}>
            <Route index element={<div>KANBAN</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );
  }

  it('clicking the name reveals an input with the current value', async () => {
    renderPage();
    // Wait for the project to load so the inline-edit button is rendered.
    const titleButton = await screen.findByRole('button', { name: /Original/ });
    fireEvent.click(titleButton);

    const input = await screen.findByLabelText(/Project name/i);
    expect((input as HTMLInputElement).value).toBe('Original');
  });

  it('Enter saves the new name via updateProject', async () => {
    const updateSpy = vi
      .spyOn(api, 'updateProject')
      .mockResolvedValue({ ...fakeProject, name: 'Renamed' });

    renderPage();
    const titleButton = await screen.findByRole('button', { name: /Original/ });
    fireEvent.click(titleButton);

    const input = await screen.findByLabelText(/Project name/i);
    fireEvent.change(input, { target: { value: 'Renamed' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith('p-1', { name: 'Renamed' });
    });
  });

  it('Escape cancels without calling updateProject', async () => {
    const updateSpy = vi.spyOn(api, 'updateProject').mockResolvedValue(fakeProject);
    renderPage();
    const titleButton = await screen.findByRole('button', { name: /Original/ });
    fireEvent.click(titleButton);

    const input = await screen.findByLabelText(/Project name/i);
    fireEvent.change(input, { target: { value: 'Never saved' } });
    fireEvent.keyDown(input, { key: 'Escape' });

    // Allow any pending microtasks to flush.
    await waitFor(() => {
      expect(updateSpy).not.toHaveBeenCalled();
    });

    // After Escape the button with the original name must be back.
    expect(await screen.findByRole('button', { name: /Original/ })).toBeTruthy();
  });

  it('blur also saves (commits when focus leaves the input)', async () => {
    const updateSpy = vi
      .spyOn(api, 'updateProject')
      .mockResolvedValue({ ...fakeProject, name: 'Via blur' });

    renderPage();
    const titleButton = await screen.findByRole('button', { name: /Original/ });
    fireEvent.click(titleButton);

    const input = await screen.findByLabelText(/Project name/i);
    fireEvent.change(input, { target: { value: 'Via blur' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith('p-1', { name: 'Via blur' });
    });
  });

  it('whitespace-only name does not call updateProject', async () => {
    const updateSpy = vi.spyOn(api, 'updateProject').mockResolvedValue(fakeProject);
    renderPage();
    const titleButton = await screen.findByRole('button', { name: /Original/ });
    fireEvent.click(titleButton);

    const input = await screen.findByLabelText(/Project name/i);
    fireEvent.change(input, { target: { value: '   ' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(updateSpy).not.toHaveBeenCalled();
    });
  });
});
