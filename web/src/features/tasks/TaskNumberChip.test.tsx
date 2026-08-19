// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { TaskNumberChip } from './TaskNumberChip';

/**
 * The chip copies `#N` to the clipboard on click and must not leak the
 * gesture to its hosts: TaskCard opens the task on click, dnd-kit
 * starts a drag on pointerdown. Both are pinned here by wrapping the
 * chip in a container with spies.
 */
describe('TaskNumberChip', () => {
  const writeText = vi.fn();

  // vitest.config has globals:false — RTL's auto-cleanup doesn't
  // register, so unmount explicitly between tests.
  afterEach(() => cleanup());

  beforeEach(() => {
    writeText.mockReset().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
  });

  it('renders the human-readable number', () => {
    render(<TaskNumberChip number={123} />);
    const chip = screen.getByTestId('task-number-chip');
    expect(chip.textContent).toBe('#123');
    expect(chip.getAttribute('title')).toBe('Copy #123');
    expect(chip.getAttribute('aria-label')).toBe('Copy #123');
  });

  it('renders nothing when the number is missing or zero (legacy rows)', () => {
    const { container } = render(<TaskNumberChip number={0} />);
    expect(container.querySelector('[data-testid="task-number-chip"]')).toBeNull();
  });

  it('copies #N to the clipboard and shows a confirmation', async () => {
    render(<TaskNumberChip number={42} />);
    fireEvent.click(screen.getByTestId('task-number-chip'));

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith('#42');
    await waitFor(() => {
      expect(screen.getByTestId('task-number-chip').textContent).toBe('Copied #42');
    });
  });

  it('does not propagate the click to the card underneath', () => {
    const onCardClick = vi.fn();
    render(
      <div onClick={onCardClick}>
        <TaskNumberChip number={7} />
      </div>,
    );
    fireEvent.click(screen.getByTestId('task-number-chip'));
    expect(onCardClick).not.toHaveBeenCalled();
    // …but the copy still happened.
    expect(writeText).toHaveBeenCalledWith('#7');
  });

  it('does not propagate pointerdown, so dnd-kit never starts a drag', () => {
    const onPointerDown = vi.fn();
    render(
      <div onPointerDown={onPointerDown}>
        <TaskNumberChip number={7} />
      </div>,
    );
    fireEvent.pointerDown(screen.getByTestId('task-number-chip'));
    expect(onPointerDown).not.toHaveBeenCalled();
  });

  it('copies via keyboard (Enter) without propagating', () => {
    const onCardClick = vi.fn();
    render(
      <div onClick={onCardClick}>
        <TaskNumberChip number={9} />
      </div>,
    );
    fireEvent.keyDown(screen.getByTestId('task-number-chip'), { key: 'Enter' });
    expect(writeText).toHaveBeenCalledWith('#9');
    expect(onCardClick).not.toHaveBeenCalled();
  });
});
