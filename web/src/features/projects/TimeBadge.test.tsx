// @vitest-environment jsdom
// Phase 30.12: pure-function tests for TimeBadge. The card-surface
// integration is covered by the existing KanbanBoard test.
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { TimeBadge } from './TimeBadge';

afterEach(cleanup);

describe('TimeBadge', () => {
  it('renders nothing when nothing is tracked', () => {
    const { container } = render(<TimeBadge estimateS={null} spentS={0} timerActive={false} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders spent-only when no estimate is set', () => {
    render(<TimeBadge estimateS={null} spentS={120} timerActive={false} />);
    const el = screen.getByTestId('time-badge');
    expect(el.textContent).toBe('⏱ 2:00');
    expect(el.title).toBe('2:00');
  });

  it('renders spent/estimate when both are set', () => {
    render(<TimeBadge estimateS={1800} spentS={600} timerActive={false} />);
    const el = screen.getByTestId('time-badge');
    expect(el.textContent).toBe('⏱ 10:00 / 30:00');
    expect(el.title).toBe('10:00 of 30:00');
  });

  it('flips the over-budget color class when spent > estimate', () => {
    const { container } = render(<TimeBadge estimateS={60} spentS={120} timerActive={false} />);
    const el = container.querySelector('[data-testid="time-badge"]')!;
    expect(el.className).toContain('border-red-300');
    expect(el.className).toContain('bg-red-50');
    expect(el.className).toContain('text-red-700');
  });

  it('uses H:MM:SS when hours > 0', () => {
    render(<TimeBadge estimateS={3700} spentS={3661} timerActive={false} />);
    const el = screen.getByTestId('time-badge');
    expect(el.textContent).toBe('⏱ 1:01:01 / 1:01:40');
  });

  it('renders the active-timer pulse regardless of spent/estimate', () => {
    render(<TimeBadge estimateS={null} spentS={0} timerActive={true} />);
    const el = screen.getByTestId('timer-active-badge');
    expect(el).toBeTruthy();
    // Title carries the operator-facing hint.
    expect(el.title).toContain('Active timer');
  });

  it('clamps negative seconds to 0 (defensive — backend can return < 0 on race)', () => {
    render(<TimeBadge estimateS={null} spentS={-5} timerActive={false} />);
    // Negative spent with no estimate is treated as "nothing tracked".
    // Negative spent WITH an estimate is clamped to 0 in the formatter.
    expect(screen.queryByTestId('time-badge')).toBeNull();
  });
});
