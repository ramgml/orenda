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

import {
  computeReorderSuffix,
  computeTaskPosition,
  neighbourPositions,
  POSITION_GAP,
} from './cardPosition';

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

  // PM review T118: legacy quick-created cards share one position
  // (e.g. all 0). The midpoint of a tie equals the tie value and the
  // server's ORDER BY position, created_at cannot express "between",
  // so the insertion must slot AFTER the neighbour instead.
  it('tie midpoint degenerates to after + GAP (before === after === 0)', () => {
    expect(computeTaskPosition(0, 0)).toEqual({ position: 1024 });
  });

  it('tie at any value slots after the neighbour', () => {
    expect(computeTaskPosition(2048, 2048)).toEqual({ position: 2048 + POSITION_GAP });
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

describe('computeReorderSuffix', () => {
  it('all-tie column: moved card and everything after get GAP-stepped positions', () => {
    // quick-created: A=0 B=0 C=0; drag C between A and B → [A, C, B]
    const updates = computeReorderSuffix(
      ['a', 'c', 'b'],
      new Map([
        ['a', 0],
        ['b', 0],
        ['c', 0],
      ]),
      1,
    );
    expect(updates.get('c')).toBe(POSITION_GAP);
    expect(updates.get('b')).toBe(2 * POSITION_GAP);
    expect(updates.has('a')).toBe(false); // prefix untouched
  });
  it('distinct positions but moved card larger than the rest: suffix re-spaced after it', () => {
    // A=1024 B=2048 C=3072; drag C to the front → [C, A, B]:
    // C itself is fine where it is (nothing before it constrains it
    // — wait, the moved card must sort BEFORE A/B, and C=3072 already
    // does under ORDER BY position, created_at only if A/B get bumped
    // above it. The suffix walk keeps C and bumps A,B past it.
    const updates = computeReorderSuffix(
      ['c', 'a', 'b'],
      new Map([
        ['a', 1024],
        ['b', 2048],
        ['c', 3072],
      ]),
      0,
    );
    expect(updates.has('c')).toBe(false);
    expect(updates.get('a')).toBe(3072 + POSITION_GAP);
    expect(updates.get('b')).toBe(3072 + 2 * POSITION_GAP);
  });

  it('distinct tail kept when moved card slots before without conflict', () => {
    // A=1024 B=2048 C=3072; drag B between A and C → [A, B, C]:
    // prev=A=1024, B kept (2048 > 1024), C kept (3072 > 2048).
    const updates = computeReorderSuffix(
      ['a', 'b', 'c'],
      new Map([
        ['a', 1024],
        ['b', 2048],
        ['c', 3072],
      ]),
      1,
    );
    expect(updates.size).toBe(0);
  });

  it('front insert into an all-tie column starts at GAP from zero', () => {
    const updates = computeReorderSuffix(
      ['x', 'a', 'b'],
      new Map([
        ['a', 0],
        ['b', 0],
      ]),
      0,
    );
    expect(updates.get('x')).toBe(POSITION_GAP);
    expect(updates.get('a')).toBe(2 * POSITION_GAP);
    expect(updates.get('b')).toBe(3 * POSITION_GAP);
  });
});
