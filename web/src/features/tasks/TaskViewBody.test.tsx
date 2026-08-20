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
import { afterEach, describe, expect, it, vi } from 'vitest';

import { DescriptionEditor } from '@/features/tasks/TaskViewBody';

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

    fireEvent.click(screen.getByRole('link', { name: 'Orenda docs' }));

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
