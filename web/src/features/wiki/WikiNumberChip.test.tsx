// @vitest-environment jsdom
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';

import { WikiNumberChip } from './WikiNumberChip';

/**
 * The chip copies `W<N>` to the clipboard on click and must not leak the
 * gesture to its hosts: PageEditor title area, click handlers, etc.
 */
describe('WikiNumberChip', () => {
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
    render(<WikiNumberChip number={123} />);
    const chip = screen.getByTestId('wiki-number-chip');
    expect(chip.textContent).toBe('W123');
    expect(chip.getAttribute('title')).toBe('Copy W123');
    expect(chip.getAttribute('aria-label')).toBe('Copy W123');
  });

  it('renders nothing when the number is missing or zero (legacy rows)', () => {
    const { container } = render(<WikiNumberChip number={0} />);
    expect(container.querySelector('[data-testid="wiki-number-chip"]')).toBeNull();
  });

  it('copies W<N> to the clipboard and shows a confirmation', async () => {
    render(<WikiNumberChip number={42} />);
    fireEvent.click(screen.getByTestId('wiki-number-chip'));

    expect(writeText).toHaveBeenCalledTimes(1);
    expect(writeText).toHaveBeenCalledWith('W42');
    await waitFor(() => {
      expect(screen.getByTestId('wiki-number-chip').textContent).toBe('Copied W42');
    });
  });

  it('does not propagate the click to the card underneath', () => {
    const onCardClick = vi.fn();
    render(
      <div onClick={onCardClick}>
        <WikiNumberChip number={7} />
      </div>,
    );
    fireEvent.click(screen.getByTestId('wiki-number-chip'));
    expect(onCardClick).not.toHaveBeenCalled();
    // …but the copy still happened.
    expect(writeText).toHaveBeenCalledWith('W7');
  });

  it('does not propagate pointerdown', () => {
    const onPointerDown = vi.fn();
    render(
      <div onPointerDown={onPointerDown}>
        <WikiNumberChip number={7} />
      </div>,
    );
    fireEvent.pointerDown(screen.getByTestId('wiki-number-chip'));
    expect(onPointerDown).not.toHaveBeenCalled();
  });

  it('copies via keyboard (Enter) without propagating', () => {
    const onCardClick = vi.fn();
    render(
      <div onClick={onCardClick}>
        <WikiNumberChip number={9} />
      </div>,
    );
    fireEvent.keyDown(screen.getByTestId('wiki-number-chip'), { key: 'Enter' });
    expect(writeText).toHaveBeenCalledWith('W9');
    expect(onCardClick).not.toHaveBeenCalled();
  });
});
