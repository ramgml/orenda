/**
 * T118: pure helpers for the kanban card reorder — no React, no dnd-kit,
 * so the position math is unit-testable in isolation (same pattern as
 * the backend's derivePosition spacing of 1024, mirrored by
 * reorderColumns in KanbanBoard for column drags).
 */

/** Options the move endpoint accepts for positioning a card. */
export interface MovePosition {
  position?: number;
}

/**
 * Where a dragged card should land relative to its new neighbours.
 * Mirrors the backend's spacing (1024) and reorderColumns midpoint:
 *   - between two cards: midpoint of their positions;
 *   - at the very end:   last position + GAP;
 *   - at the very front: first position - GAP;
 *   - empty column:      GAP (the backend's own default for appends).
 * The previous position of the moved card itself is irrelevant here —
 * the backend only needs the target-column neighbourhood.
 */
export const POSITION_GAP = 1024;

export function computeTaskPosition(before: number | null, after: number | null): MovePosition {
  if (before != null && after != null) {
    // Degenerate tie (legacy quick-created cards all share one
    // position, e.g. 0): the midpoint equals the tie value and the
    // server's `ORDER BY position, created_at` cannot express
    // "between", so the drag would silently revert on reload. Slot
    // after the neighbour instead; KanbanBoard rebalances the tied
    // suffix via computeReorderSuffix so the visible order sticks.
    if (before === after) {
      return { position: after + POSITION_GAP };
    }
    return { position: (before + after) / 2 };
  }
  if (before != null) {
    return { position: before + POSITION_GAP };
  }
  if (after != null) {
    return { position: after - POSITION_GAP };
  }
  // Single-card (or empty) column — position is moot; match the
  // backend's append default.
  return { position: POSITION_GAP };
}

/**
 * Position lookup for the card that now sits BEFORE the moved card in
 * the target column, and the one that sits AFTER it. `neighbours` must
 * be the target column's cards in their (already reordered) display
 * order with `movedId` spliced in at its new index.
 */
export function neighbourPositions(
  orderedIds: string[],
  positions: Map<string, number>,
  movedIndex: number,
): { before: number | null; after: number | null } {
  const beforeId = movedIndex > 0 ? orderedIds[movedIndex - 1] : null;
  const afterId = movedIndex < orderedIds.length - 1 ? orderedIds[movedIndex + 1] : null;
  return {
    before: beforeId != null ? (positions.get(beforeId) ?? null) : null,
    after: afterId != null ? (positions.get(afterId) ?? null) : null,
  };
}

/**
 * Rebalance the suffix of a reordered column when the insertion point
 * sits inside a position tie (before === after): every card from the
 * moved one onward gets a strictly increasing position (GAP steps), so
 * the server's `ORDER BY position, created_at` reproduces the exact
 * post-drag order after reload. Cards before the moved index keep
 * their positions. Returns only the cards whose position actually
 * changed (the moved card included when bumped).
 */
export function computeReorderSuffix(
  orderedIds: string[],
  positions: Map<string, number>,
  movedIndex: number,
  gap: number = POSITION_GAP,
): Map<string, number> {
  const updates = new Map<string, number>();
  let prev = movedIndex > 0 ? (positions.get(orderedIds[movedIndex - 1]) ?? 0) : 0;
  for (let i = movedIndex; i < orderedIds.length; i++) {
    const id = orderedIds[i];
    const old = positions.get(id);
    if (old == null || old <= prev) {
      prev += gap;
      updates.set(id, prev);
    } else {
      prev = old;
    }
  }
  return updates;
}
