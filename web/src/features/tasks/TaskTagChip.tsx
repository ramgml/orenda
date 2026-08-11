import type { Tag } from '@/shared/api/client'

/**
 * Small coloured chip representing one task tag.
 *
 * Dumb component: takes a Tag, renders a pill. No state, no events.
 * The kanban card and the task sidebar both reuse it; if Phase 17
 * wants to inline the rendering into TaskCard.tsx, it can — the
 * surface is intentionally tiny (one prop, one JSX node).
 *
 * Colour handling:
 *   - When the tag has a colour, the chip background is that colour.
 *     The text colour is auto-picked (white on dark backgrounds,
 *     slate-900 on light backgrounds) using a luminance check so
 *     chips stay readable on any background.
 *   - Without a colour the chip falls back to a neutral slate pill
 *     so the UI still renders something for untagged work.
 */
export function TaskTagChip({ tag }: { tag: Tag }): JSX.Element {
  const bg = tag.color && tag.color.length > 0 ? tag.color : undefined
  const fg = bg ? (isDarkHex(bg) ? '#ffffff' : '#0f172a') : undefined

  return (
    <span
      title={tag.name}
      style={{
        backgroundColor: bg ?? 'rgb(241 245 249)', // slate-100 fallback
        color: fg ?? 'rgb(51 65 85)', // slate-700 fallback
        borderColor: bg ?? 'rgb(226 232 240)', // slate-200 fallback
      }}
      className="inline-flex items-center px-1.5 py-0.5 text-[10px] font-medium rounded border max-w-[8rem] truncate"
    >
      {tag.name}
    </span>
  )
}

/**
 * List-of-chips wrapper. Renders nothing when the list is empty so
 * callers don't have to gate the import.
 */
export function TaskTagChips({ tags, className }: { tags: Tag[] | undefined; className?: string }): JSX.Element {
  if (!tags || tags.length === 0) {
    return <></>
  }
  return (
    <div className={`flex flex-wrap gap-1 ${className ?? ''}`}>
      {tags.map((t) => (
        <TaskTagChip key={t.id} tag={t} />
      ))}
    </div>
  )
}

// isDarkHex returns true when the perceived luminance of the hex
// colour is below the 0.55 threshold — meaning white text would be
// more readable than dark text on top of it.
//
// We parse the hex string directly (no colour library) since the
// tag colour is already validated as #RGB or #RRGGBB by the backend
// (see task.Tag.Validate). If the format is unexpected, we err on
// the side of "light" (dark text), which matches Tailwind's default
// body text colour.
function isDarkHex(hex: string): boolean {
  const h = hex.startsWith('#') ? hex.slice(1) : hex
  let r = 0
  let g = 0
  let b = 0
  if (h.length === 3) {
    r = parseInt(h[0] + h[0], 16)
    g = parseInt(h[1] + h[1], 16)
    b = parseInt(h[2] + h[2], 16)
  } else if (h.length === 6) {
    r = parseInt(h.slice(0, 2), 16)
    g = parseInt(h.slice(2, 4), 16)
    b = parseInt(h.slice(4, 6), 16)
  } else {
    return false
  }
  // Standard sRGB luminance (ITU-R BT.601).
  const lum = (0.299 * r + 0.587 * g + 0.114 * b) / 255
  return lum < 0.55
}