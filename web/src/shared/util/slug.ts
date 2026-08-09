// Slug generation from a human title.
//
// Rules:
//   1. Lowercase.
//   2. Transliterate common Cyrillic (Russian + Ukrainian + Belarusian
//      digraphs) and a few non-ASCII letters people type in titles
//      (ü, ñ, ö, etc).
//   3. Replace every other non-[a-z0-9] run with a single "-".
//   4. Trim leading/trailing "-".
//   5. If the result is empty (e.g. title is only emoji or CJK),
//      fall back to "page-<unix seconds>" so the user always gets a
//      usable URL and the create flow never produces a 422.
//
// The transliteration table here is intentionally short and
// predictable — we are not building a full Unicode CLDR map, just
// giving the common Cyrillic words a readable Latin form. Anything we
// don't recognise still passes the fallback path.

const CYRILLIC: Record<string, string> = {
  а: 'a', б: 'b', в: 'v', г: 'g', д: 'd', е: 'e', ё: 'yo', ж: 'zh',
  з: 'z', и: 'i', й: 'i', к: 'k', л: 'l', м: 'm', н: 'n', о: 'o',
  п: 'p', р: 'r', с: 's', т: 't', у: 'u', ф: 'f', х: 'h', ц: 'ts',
  ч: 'ch', ш: 'sh', щ: 'shch', ъ: '', ы: 'y', ь: '', э: 'e', ю: 'yu',
  я: 'ya',
  // Ukrainian / Belarusian specifics
  і: 'i', ї: 'yi', є: 'ye', ґ: 'g',
}

const OTHER: Record<string, string> = {
  à: 'a', á: 'a', â: 'a', ã: 'a', ä: 'a', å: 'a',
  è: 'e', é: 'e', ê: 'e', ë: 'e',
  ì: 'i', í: 'i', î: 'i', ï: 'i',
  ò: 'o', ó: 'o', ô: 'o', õ: 'o', ö: 'o', ø: 'o',
  ù: 'u', ú: 'u', û: 'u', ü: 'u',
  ñ: 'n', ß: 'ss', ç: 'c',
}

/**
 * Transliterate one character. Returns:
 *   - a non-empty Latin string for known chars (e.g. 'а' → 'a')
 *   - '' for known-but-silent chars (ъ, ь) — they should NOT add a
 *     separator; they're just dropped.
 *   - undefined for chars we don't know. The caller decides what to
 *     do with those (typically fall through to "strip").
 *
 * The sentinel is important: an empty string is *not* the same as
 * "unknown" — we explicitly distinguish "drop silently" from "this
 * letter isn't ours".
 */
function transliterate(ch: string): string | undefined {
  const lower = ch.toLowerCase()
  if (Object.prototype.hasOwnProperty.call(CYRILLIC, lower)) return CYRILLIC[lower]
  if (Object.prototype.hasOwnProperty.call(OTHER, lower)) return OTHER[lower]
  return undefined
}

/**
 * Slugify a title for use as a wiki URL segment.
 *
 *   slugify("Моя первая страница") → "moya-pervaya-stranitsa"
 *   slugify("Hello, World!")        → "hello-world"
 *   slugify("")                     → "page-1700000000"
 */
export function slugify(title: string): string {
  let out = ''
  for (const ch of title) {
    const t = transliterate(ch)
    if (t !== undefined) {
      // Known letter — keep the transliteration verbatim (including
      // empty strings for silent letters like ъ / ь).
      if (t) out += t
      // empty string → just drop, no separator
    } else if (/[a-z0-9]/i.test(ch)) {
      out += ch.toLowerCase()
    } else if (/\s/.test(ch)) {
      out += ' '
    } else {
      // Emoji, CJK, punctuation — drop. Adjacent drops collapse to a
      // single "-" in the next pass.
      out += ' '
    }
  }
  // Whitespace runs → "-"; trim.
  const slug = out
    .trim()
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-+|-+$/g, '')
    .toLowerCase()

  return slug || `page-${Math.floor(Date.now() / 1000)}`
}