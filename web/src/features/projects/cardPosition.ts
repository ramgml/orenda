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
