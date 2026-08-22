// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { ProjectNumberChip } from './ProjectNumberChip';

/**
 * The chip copies `P<N>` to the clipboard on click and must not leak the
 * gesture to its hosts.
 */
describe('ProjectNumberChip', () => {
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
    render(<ProjectNumberChip number={7} />);
    const chip = screen.getByTestId('project-number-chip');
    expect(chip.textContent).toBe('P7');
    expect(chip.getAttribute('title')).toBe('Copy P7');
    expect(chip.getAttribute('aria-label')).toBe('Copy P7');
  });

  it('renders nothing when the number is missing or zero (legacy rows)', () => {
    const { container } = render(<ProjectNumberChip number={0} />);
    expect(container.querySelector('[data-testid="project-number-chip"]')).toBeNull();
  });

  it('copies P<N> to the clipboard and shows a confirmation', async () => {
    render(<ProjectNumberChip number={42} />);
    fireEvent.click(screen.getByTestId('project-number-chip'));

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith('P42');
    await waitFor(() => {
      expect(screen.getByTestId('project-number-chip').textContent).toBe('Copied P42');
    });
  });

  it('does not propagate the click to the parent underneath', () => {
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <ProjectNumberChip number={7} />
      </div>,
    );
    fireEvent.click(screen.getByTestId('project-number-chip'));
    expect(onParentClick).not.toHaveBeenCalled();
    expect(writeText).toHaveBeenCalledWith('P7');
  });

  it('does not propagate pointerdown', () => {
    const onPointerDown = vi.fn();
    render(
      <div onPointerDown={onPointerDown}>
        <ProjectNumberChip number={7} />
      </div>,
    );
    fireEvent.pointerDown(screen.getByTestId('project-number-chip'));
    expect(onPointerDown).not.toHaveBeenCalled();
  });

  it('copies via keyboard (Enter) without propagating', () => {
    const onParentClick = vi.fn();
    render(
      <div onClick={onParentClick}>
        <ProjectNumberChip number={9} />
      </div>,
    );
    fireEvent.keyDown(screen.getByTestId('project-number-chip'), { key: 'Enter' });
    expect(writeText).toHaveBeenCalledWith('P9');
    expect(onParentClick).not.toHaveBeenCalled();
  });
});
