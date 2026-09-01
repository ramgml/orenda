/**
 * Human-readable activity payload details.
 *
 * Task 113: the activity feeds (ActivityLog in the task card and
 * ProjectActivityTab) used to print the raw payload JSON after the
 * verb — noise like `{"before":[],"after":["epic"]}`. This module
 * maps each action's payload to a short human-readable detail string;
 * actions whose payload holds only ids/flags render nothing. The full
 * JSON stays available on hover via the row's `title` attribute.
 */

interface PayloadShape {
  title?: string;
  filename?: string;
  before?: unknown;
  after?: unknown;
  from?: unknown;
  to?: unknown;
  column_id?: unknown;
  column_name?: unknown;
}

// parsePayload returns the parsed payload object or undefined for
// invalid / non-object JSON. The `try/catch` + shape guard is a named
// contract (callers treat undefined as "no details"), not a rename.
function parsePayload(payload: string): PayloadShape | undefined {
  try {
    const parsed: unknown = JSON.parse(payload);
    if (parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed as PayloadShape;
    }
  } catch {
    // fall through: legacy rows may carry free-form strings.
  }
  return undefined;
}

// isTagArray is a type guard: the narrowing to string[] is required
// by the tags_replaced branch of activityDetails below.
function isTagArray(v: unknown): v is string[] {
  return Array.isArray(v) && v.length > 0 && v.every((t) => typeof t === 'string');
}
/**
 * activityDetails renders the human-readable payload detail for one
 * audit row, or '' when the action carries no user-facing payload.
 *
 * Contract (per task 113):
 *   - `task.commented` — nothing (author_type is visible from the
 *     actor, comment_id is noise).
 *   - `task.attachment_added` — the filename when present, nothing
 *     otherwise.
 *   - `task.tags_replaced` — the incoming tag set, `→ epic, ui` /
 *     `→ —` when cleared.
 *   - everything else — a `from → to` diff when the payload holds
 *     meaningful text (status, priority, colour), the checklist/child
 *     `title` quoted, the target column for moves; nothing otherwise
 *     (ids stay hover-only).
 *
 * Task 117 (moves): the payload carries `column_name` (the target
 * column's name, snapshotted at event time), so a move renders
 * `→ In Review` instead of `→ <uuid>`. Rows written before 117
 * (and rows whose column lookup failed at write time) have no
 * `column_name` — those fall back to the old `→ <column_id>` UUID
 * form: the id stays traceable via the hover JSON, a render-time
 * resolve would show the column's CURRENT name (or nothing for a
 * deleted column), so the UUID fallback preserves the most
 * information. The full JSON remains available on hover: callers
 * pass `payload` to the row's `title` attribute.
 */
export function activityDetails(rawAction: string, payload: string): string {
  if (!payload || payload === '{}') return '';
  // The audit stores both spellings: 27.9 prefixed most actions with
  // `task.`, but `tags_replaced` / `color_changed` rows (and the
  // pre-27.9 status/priority/assignee rows) exist unprefixed —
  // normalize once so both render the same details.
  const action = rawAction.startsWith('task.') ? rawAction.slice(5) : rawAction;
  switch (action) {
    case 'commented':
      return '';
    case 'attachment_added': {
      const f = parsePayload(payload)?.filename;
      if (typeof f !== 'string') return '';
      const t = f.trim();
      return t === '' ? '' : t;
    }
    case 'tags_replaced': {
      const o = parsePayload(payload);
      // Only the incoming tag set is shown: `→ epic, ui`, or the
      // cleared-set dash when `after` is an empty array. The removed
      // set (non-empty `before`) stays hover-only — the cost of a
      // one-line feed.
      if (isTagArray(o?.after)) return `→ ${o.after.join(', ')}`;
      if (Array.isArray(o?.after) && o.after.length === 0) return '→ —';
      return '';
    }
  }
  const o = parsePayload(payload);
  if (!o) return '';
  // from → to renders only for meaningful text/numbers (status,
  // priority, colour). `from`/`to` are `unknown`: id/object payloads
  // (the assignee change) produce no scalars and fall through to
  // nothing.
  const fromVal = o.from;
  const toVal = o.to;
  if (typeof fromVal === 'string' || typeof fromVal === 'number') {
    if (typeof toVal === 'string' || typeof toVal === 'number') {
      const a = String(fromVal).trim();
      const b = String(toVal).trim();
      if (b !== '') {
        if (a === '') return `→ ${b}`; // first assignment (e.g. no colour before)
        return a === b ? '' : `${a} → ${b}`;
      }
    }
    return '';
  }
  // Actions whose payload stores the target value under a dedicated
  // key: child/checklist rows carry `title`, the move row carries
  // `column_name` (task 117, snapshotted at event time) with a
  // legacy fallback to `column_id` (the raw UUID — ids stay
  // hover-only everywhere else). position and the other ids
  // (child_id, item_id, checklist_id, attachment_id) stay
  // hover-only.
  const t = typeof o.title === 'string' ? o.title.trim() : '';
  if (t !== '') return `"${t}"`;
  const n = typeof o.column_name === 'string' ? o.column_name.trim() : '';
  if (n !== '') return `→ ${n}`;
  const c = typeof o.column_id === 'string' ? o.column_id.trim() : '';
  if (c !== '') return `→ ${c}`;
  return '';
}
