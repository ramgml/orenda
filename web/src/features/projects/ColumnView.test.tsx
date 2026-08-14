// @vitest-environment jsdom
/**
 * Phase 27.10 — EditColumnModal initialisation bug.
 *
 * Three regressions that the column colour fix has to keep from coming
 * back:
 *
 *   1. The saved colour is rendered as a dot left of the header.
 *   2. Re-opening the edit modal preserves the saved colour and WIP
 *      (the previous bug hardcoded both to defaults).
 *   3. Saving a rename (without touching the colour picker) does NOT
 *      reset the saved colour on the server — the bug was that the
 *      modal always posted color, even when unchanged, so a later
 *      rename clobbered it with the default slate.
 */
import { DndContext } from '@dnd-kit/core';
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { Column, Task } from '@/shared/api/client';
import { api } from '@/shared/api/client';
import { ColumnView } from '@/features/projects/ColumnView';

function makeColumn(over: Partial<Column> = {}): Column {
  return {
    id: 'col-1',
    board_id: 'b-1',
    name: 'In progress',
    position: 1,
    ...over,
  };
}

function makeTask(): Task {
  return {
    id: 'task-1',
    project_id: 'p1',
    column_id: 'col-1',
    title: 'Sample task',
    status: 'todo',
    priority: 'medium',
    awaiting: 'none',
    time_spent_s: 0,
    position: 0,
    created_at: '2026-08-13T00:00:00Z',
    updated_at: '2026-08-13T00:00:00Z',
    color: '',
    tags: [],
  };
}

describe('ColumnView — Phase 27.10 colour wiring', () => {
  // The ApiClient class is not exported, so TS can't narrow
  // vi.spyOn's second argument to the concrete method signature.
  // Cast through `any` — the runtime call is type-checked by the
  // existing PATCH payload.
  let updateColumnSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    updateColumnSpy = vi.spyOn(api as never, 'updateColumn') as ReturnType<typeof vi.spyOn>;
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  function renderColumn(props: { color?: string; wipLimit?: number } = {}) {
    return render(
      <DndContext>
        <MemoryRouter>
          <ColumnView
            columnId="col-1"
            projectId="p1"
            name="In progress"
            tasks={[makeTask()]}
            color={props.color}
            wipLimit={props.wipLimit}
            onCreate={async () => {}}
          />
        </MemoryRouter>
      </DndContext>,
    );
  }

  it('renders the saved colour as a dot with the right background', () => {
    const { getByTestId } = renderColumn({ color: '#ff8800' });
    const dot = getByTestId('column-color-dot');
    expect(dot.dataset.columnColor).toBe('#ff8800');
    expect(dot.style.backgroundColor).toBe('rgb(255, 136, 0)');
  });

  it('falls back to a neutral slate when no colour is saved', () => {
    const { getByTestId } = renderColumn();
    const dot = getByTestId('column-color-dot');
    expect(dot.dataset.columnColor).toBe('');
    // Slate fallback so the layout is stable across columns without
    // a colour set. #94a3b8 in rgb form.
    expect(dot.style.backgroundColor).toBe('rgb(148, 163, 184)');
  });

  it('opens the edit modal with the saved colour, not the slate default', async () => {
    const { getByTitle, getByLabelText } = renderColumn({
      color: '#22c55e',
      wipLimit: 7,
    });

    // ⚙ button opens the modal (title="Edit column").
    fireEvent.click(getByTitle('Edit column'));

    const colourInput = getByLabelText('Color') as HTMLInputElement;
    expect(colourInput.value).toBe('#22c55e');

    const wipInput = getByLabelText(/WIP limit/) as HTMLInputElement;
    expect(wipInput.value).toBe('7');
  });

  it('sends the colour in PATCH only when the user actually changed it', async () => {
    updateColumnSpy.mockResolvedValue({
      ...makeColumn(),
      color: '#22c55e',
      wip_limit: 7,
    });

    const { getByTitle, getByLabelText, getByRole } = renderColumn({
      color: '#22c55e',
      wipLimit: 7,
    });

    fireEvent.click(getByTitle('Edit column'));

    // User only edits the name — colour + WIP are untouched.
    const nameInput = getByLabelText('Name') as HTMLInputElement;
    fireEvent.change(nameInput, { target: { value: 'Doing' } });

    fireEvent.click(getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(updateColumnSpy).toHaveBeenCalledTimes(1));
    const [id, payload] = updateColumnSpy.mock.calls[0] as [
      string,
      { name?: string; color?: string; wip_limit?: number | null },
    ];
    expect(id).toBe('col-1');
    // Critical Phase 27.10 contract: a rename must not blank the
    // colour on the server. We omit `color` from the payload
    // because the picker wasn't touched.
    expect(payload).not.toHaveProperty('color');
    expect(payload.name).toBe('Doing');
    expect(payload.wip_limit).toBe(7);
  });

  it('does send the new colour when the user actually picks one', async () => {
    updateColumnSpy.mockResolvedValue({
      ...makeColumn(),
      color: '#ef4444',
    });

    const { getByTitle, getByLabelText, getByRole } = renderColumn({
      color: '#22c55e',
    });

    fireEvent.click(getByTitle('Edit column'));

    const colourInput = getByLabelText('Color') as HTMLInputElement;
    fireEvent.change(colourInput, { target: { value: '#ef4444' } });

    fireEvent.click(getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(updateColumnSpy).toHaveBeenCalledTimes(1));
    const [, payload] = updateColumnSpy.mock.calls[0] as [
      string,
      { name?: string; color?: string; wip_limit?: number | null },
    ];
    expect(payload.color).toBe('#ef4444');
  });
});
