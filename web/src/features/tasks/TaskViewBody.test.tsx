// @vitest-environment jsdom
/**
 * Task 11: the task description renders as Markdown in view mode.
 *
 * Contracts pinned here (TaskViewBody → DescriptionEditor):
 *
 *   1. View mode renders the description through ReactMarkdown —
 *      headings, lists, code and links come out as real HTML, not
 *      raw source text.
 *   2. Click-to-edit: clicking the rendered description opens the
 *      edit form with the raw markdown in the textarea.
 *   3. Clicking a link inside the rendered markdown does NOT flip
 *      into edit mode (stopPropagation on <a>).
 *   4. The empty state stays the "+ Add description" <button>.
 */
import { cleanup, fireEvent, render, screen, type RenderResult } from '@testing-library/react';
import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import { MemoryRouter } from 'react-router-dom';

import { DescriptionEditor, DueEditor, EstimateEditor } from '@/features/tasks/TaskViewBody';
import type { Task } from '@/shared/api/client';

const MARKDOWN = [
  '# Heading One',
  '',
  '- first item',
  '- second item',
  '',
  'Inline `code` here.',
  '',
  '[Orenda docs](https://example.com/docs)',
].join('\n');
function mountDescription(value: string): RenderResult {
  return render(
    <DescriptionEditor value={value} onSave={vi.fn().mockResolvedValue(undefined)} busy={false} />,
  );
}

afterEach(() => {
  cleanup();
});

describe('DescriptionEditor — markdown view mode', () => {
  it('renders the description as markdown (heading, list, code, link)', () => {
    const { container } = mountDescription(MARKDOWN);

    expect(screen.getByRole('heading', { level: 1, name: 'Heading One' })).toBeTruthy();
    expect(screen.getByRole('list')).toBeTruthy();
    const items = screen.getAllByRole('listitem');
    expect(items.map((li) => li.textContent)).toEqual(['first item', 'second item']);
    expect(screen.getByRole('link', { name: 'Orenda docs' })).toBeTruthy();
    // Inline code has no ARIA role — assert the element directly.
    expect(container.querySelector('code')).toBeTruthy();
    // The raw source must not leak as plain text.
    expect(screen.queryByText(MARKDOWN)).toBeNull();
  });

  it('clicking the rendered description opens the edit form', () => {
    mountDescription(MARKDOWN);

    fireEvent.click(screen.getByRole('article'));

    const textarea = screen.getByRole('textbox');
    expect(textarea).toBeTruthy();
    expect((textarea as HTMLTextAreaElement).value).toBe(MARKDOWN);
  });

  it('clicking a link inside the markdown does not enter edit mode', () => {
    mountDescription(MARKDOWN);

    const link = screen.getByRole('link', { name: 'Orenda docs' });
    // jsdom implements <a> clicks as a timer-based window navigation and
    // logs "Not implemented: navigation" — prevent the default activation
    // so the test only exercises the stopPropagation contract.
    link.addEventListener('click', (e) => e.preventDefault());
    fireEvent.click(link);

    expect(screen.queryByRole('textbox')).toBeNull();
    // Still in view mode.
    expect(screen.getByRole('heading', { level: 1, name: 'Heading One' })).toBeTruthy();
  });

  it('keeps the "+ Add description" button for an empty description', () => {
    mountDescription('');

    const button = screen.getByRole('button', { name: '+ Add description' });
    expect(button).toBeTruthy();
    expect(button.tagName).toBe('BUTTON');
  });
});

// ---------------------------------------------------------------------------
// T90: editable Due field + "Show in calendar" deep link
// ---------------------------------------------------------------------------

// Minimal task fixture — DueEditor reads only the calendar fields.
function dueTask(overrides: Partial<Task> = {}): Task {
  return {
    id: 't-90',
    title: 'Task with a due',
    status: 'todo',
    priority: 'medium',
    time_spent_s: 0,
    ...overrides,
  } as Task;
}

describe('DueEditor (T90)', () => {
  afterEach(() => {
    cleanup();
  });

  function mountDue(task: Task): { onSaveDue: Mock } {
    const onSaveDue = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <DueEditor task={task} busy={false} onSaveDue={onSaveDue} />
      </MemoryRouter>,
    );
    return { onSaveDue };
  }

  it('renders a date input prefilled with the due date', () => {
    mountDue(dueTask({ due_at: '2030-01-15T00:00:00.000Z' }));

    const input = screen.getByTitle('Due date') as HTMLInputElement;
    expect(input.type).toBe('date');
    expect(input.value).toBe('2030-01-15');
  });

  it('changing the date commits the new value through onSaveDue', () => {
    const { onSaveDue } = mountDue(dueTask({ due_at: '2030-01-15T00:00:00.000Z' }));

    fireEvent.change(screen.getByTitle('Due date'), { target: { value: '2030-02-20' } });

    expect(onSaveDue).toHaveBeenCalledWith('2030-02-20');
  });

  it('clearing via the clear button commits the empty string', () => {
    const { onSaveDue } = mountDue(dueTask({ due_at: '2030-01-15T00:00:00.000Z' }));

    fireEvent.click(screen.getByRole('button', { name: 'clear' }));

    expect(onSaveDue).toHaveBeenCalledWith('');
  });

  it('shows "Show in calendar" seeded with the due date', () => {
    mountDue(dueTask({ due_at: '2030-01-15T00:00:00.000Z' }));

    const link = screen.getByRole('link', { name: 'Show in calendar' });
    expect(link.getAttribute('href')).toBe('/calendar?date=2030-01-15');
  });

  it('falls back to start_at for the link when no due date is set', () => {
    mountDue(dueTask({ start_at: '2031-03-02T09:00:00.000Z' }));

    const link = screen.getByRole('link', { name: 'Show in calendar' });
    expect(link.getAttribute('href')).toBe('/calendar?date=2031-03-02');
    // No due → no clear affordance.
    expect(screen.queryByRole('button', { name: 'clear' })).toBeNull();
  });

  it('renders no link when the task has neither due_at nor start_at', () => {
    mountDue(dueTask());

    expect(screen.queryByRole('link', { name: 'Show in calendar' })).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// T120: editable time estimate (minutes in, seconds on the wire)
// ---------------------------------------------------------------------------

describe('EstimateEditor (T120)', () => {
  afterEach(() => {
    cleanup();
  });

  function mountEstimate(task: Task): { onSaveEstimate: Mock } {
    const onSaveEstimate = vi.fn().mockResolvedValue(undefined);
    render(
      <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <EstimateEditor task={task} busy={false} onSaveEstimate={onSaveEstimate} />
      </MemoryRouter>,
    );
    return { onSaveEstimate };
  }

  it('renders without a clear button when no estimate is set', () => {
    mountEstimate(dueTask());

    expect(screen.getByTitle('Time estimate')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'clear' })).toBeNull();
  });

  it('setting minutes commits seconds through onSaveEstimate', () => {
    const { onSaveEstimate } = mountEstimate(dueTask());

    fireEvent.change(screen.getByTitle('Time estimate'), { target: { value: '90' } });
    fireEvent.click(screen.getByRole('button', { name: 'Set' }));

    expect(onSaveEstimate).toHaveBeenCalledWith(90 * 60);
  });

  it('clearing via the clear button commits the 0 sentinel', () => {
    const { onSaveEstimate } = mountEstimate(dueTask({ time_estimate_s: 5400 }));

    fireEvent.click(screen.getByRole('button', { name: 'clear' }));

    expect(onSaveEstimate).toHaveBeenCalledWith(0);
  });

  it('ignores a non-numeric or negative input (Set stays silent)', () => {
    const { onSaveEstimate } = mountEstimate(dueTask());

    fireEvent.change(screen.getByTitle('Time estimate'), { target: { value: '-5' } });
    fireEvent.click(screen.getByRole('button', { name: 'Set' }));

    expect(onSaveEstimate).not.toHaveBeenCalled();
  });
});
