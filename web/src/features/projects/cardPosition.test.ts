// @vitest-environment jsdom
/**
 * T118: card reorder within a column. The board sends a `position` on
 * same-column moves so the backend can slot the card between its new
 * neighbours (spacing 1024, midpoints between them) — the same math
 * reorderColumns already uses for column drags and the backend's
 * derivePosition implements server-side.
 *
 * Pinned here (pure math, no React/dnd-kit):
 *   - midpoint between neighbours,
 *   - end append = last + GAP,
 *   - front insert = first - GAP,
 *   - empty/single-card column = GAP,
 *   - neighbour lookup from the reordered id list.
 */
import { describe, expect, it } from 'vitest';

import { computeTaskPosition, neighbourPositions, POSITION_GAP } from './cardPosition';

describe('computeTaskPosition', () => {
  it('midpoint between two neighbours', () => {
    expect(computeTaskPosition(1024, 2048)).toEqual({ position: 1536 });
  });

  it('dropped at the very end: last + GAP', () => {
    expect(computeTaskPosition(4096, null)).toEqual({ position: 4096 + POSITION_GAP });
  });

  it('dropped at the very front: first - GAP', () => {
    expect(computeTaskPosition(null, 1024)).toEqual({ position: 0 });
  });

  it('empty column: GAP (backend append default)', () => {
    expect(computeTaskPosition(null, null)).toEqual({ position: POSITION_GAP });
  });

  it('midpoints stay unique enough for repeated halves', () => {
    // A card repeatedly moved between two fixed neighbours converges
    // but never collides within realistic depth (floats, not ints).
    const a = computeTaskPosition(0, 1).position;
    const b = computeTaskPosition(0, a!).position;
    expect(b).toBeGreaterThan(0);
    expect(b).toBeLessThan(a!);
  });
});

describe('neighbourPositions', () => {
  const positions = new Map([
    ['a', 0],
    ['b', 1024],
    ['c', 2048],
  ]);

  it('middle of the list: both neighbours found', () => {
    // order [a, x, c] with x at index 1 → before=a, after=c
    expect(neighbourPositions(['a', 'x', 'c'], positions, 1)).toEqual({
      before: 0,
      after: 2048,
    });
  });

  it('front insert: no before', () => {
    expect(neighbourPositions(['x', 'a', 'b'], positions, 0)).toEqual({
      before: null,
      after: 0,
    });
  });

  it('end append: no after', () => {
    expect(neighbourPositions(['a', 'b', 'x'], positions, 2)).toEqual({
      before: 1024,
      after: null,
    });
  });

  it('unknown neighbour id yields null (treated as boundary)', () => {
    expect(neighbourPositions(['a', 'x', 'zzz'], positions, 1)).toEqual({
      before: 0,
      after: null,
    });
  });
});
