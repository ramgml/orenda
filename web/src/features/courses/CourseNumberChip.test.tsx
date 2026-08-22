// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { CourseNumberChip } from './CourseNumberChip';

describe('CourseNumberChip', () => {
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
    render(<CourseNumberChip number={7} />);
    const chip = screen.getByTestId('course-number-chip');
    expect(chip.textContent).toBe('C7');
    expect(chip.getAttribute('title')).toBe('Copy C7');
    expect(chip.getAttribute('aria-label')).toBe('Copy C7');
  });

  it('renders nothing when the number is missing or zero (legacy rows)', () => {
    const { container } = render(<CourseNumberChip number={0} />);
    expect(container.querySelector('[data-testid="course-number-chip"]')).toBeNull();
  });

  it('copies C<N> to the clipboard and shows a confirmation', async () => {
    render(<CourseNumberChip number={42} />);
    fireEvent.click(screen.getByTestId('course-number-chip'));

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith('C42');
    await waitFor(() => {
      expect(screen.getByTestId('course-number-chip').textContent).toBe('Copied C42');
    });
  });

  it('does not propagate the click to the parent underneath', () => {
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <CourseNumberChip number={7} />
      </div>,
    );
    fireEvent.click(screen.getByTestId('course-number-chip'));
    expect(onParentClick).not.toHaveBeenCalled();
    expect(writeText).toHaveBeenCalledWith('C7');
  });

  it('does not propagate pointerdown', () => {
    const onPointerDown = vi.fn();
    render(
      <div onPointerDown={onPointerDown}>
        <CourseNumberChip number={7} />
      </div>,
    );
    fireEvent.pointerDown(screen.getByTestId('course-number-chip'));
    expect(onPointerDown).not.toHaveBeenCalled();
  });

  it('copies via keyboard (Enter) without propagating', () => {
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <CourseNumberChip number={9} />
      </div>,
    );
    fireEvent.keyDown(screen.getByTestId('course-number-chip'), { key: 'Enter' });
    expect(writeText).toHaveBeenCalledWith('C9');
    expect(onParentClick).not.toHaveBeenCalled();
  });
});
