// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { LessonNumberChip } from './LessonNumberChip';

describe('LessonNumberChip', () => {
  const writeText = vi.fn();

  afterEach(() => cleanup());

  beforeEach(() => {
    writeText.mockReset().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
  });

  it('renders the human-readable number', () => {
    render(<LessonNumberChip number={10} />);
    const chip = screen.getByTestId('lesson-number-chip');
    expect(chip.textContent).toBe('L10');
    expect(chip.getAttribute('title')).toBe('Copy L10');
    expect(chip.getAttribute('aria-label')).toBe('Copy L10');
  });

  it('renders nothing when the number is missing or zero (legacy rows)', () => {
    const { container } = render(<LessonNumberChip number={0} />);
    expect(container.querySelector('[data-testid="lesson-number-chip"]')).toBeNull();
  });

  it('copies L<N> to the clipboard and shows a confirmation', async () => {
    render(<LessonNumberChip number={42} />);
    fireEvent.click(screen.getByTestId('lesson-number-chip'));

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith('L42');
    await waitFor(() => {
      expect(screen.getByTestId('lesson-number-chip').textContent).toBe('Copied L42');
    });
  });

  it('does not propagate the click to the parent underneath', () => {
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <LessonNumberChip number={10} />
      </div>,
    );
    fireEvent.click(screen.getByTestId('lesson-number-chip'));
    expect(onParentClick).not.toHaveBeenCalled();
    expect(writeText).toHaveBeenCalledWith('L10');
  });

  it('does not propagate pointerdown', () => {
    const onPointerDown = vi.fn();
    render(
      <div onPointerDown={onPointerDown}>
        <LessonNumberChip number={10} />
      </div>,
    );
    fireEvent.pointerDown(screen.getByTestId('lesson-number-chip'));
    expect(onPointerDown).not.toHaveBeenCalled();
  });

  it('copies via keyboard (Enter) without propagating', () => {
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <LessonNumberChip number={9} />
      </div>,
    );
    fireEvent.keyDown(screen.getByTestId('lesson-number-chip'), { key: 'Enter' });
    expect(writeText).toHaveBeenCalledWith('L9');
    expect(onParentClick).not.toHaveBeenCalled();
  });
});
