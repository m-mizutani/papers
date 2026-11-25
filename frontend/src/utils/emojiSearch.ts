import { isIcon } from './icon'

// Finding emoji by name for the icon picker. The names and keywords come
// from emojibase-data (English and Japanese, built from Unicode CLDR); they
// are loaded only when the picker opens, so the first page load does not
// carry them.

/** One emoji that can be found by name. */
export interface EmojiName {
  emoji: string
  /** Names as shown, for the hint. */
  en: string
  ja: string
  /** Normalized names (English and Japanese) and keywords, for matching. */
  labels: string[]
  tags: string[]
}

/** One record of emojibase-data's compact.json (the fields used here). */
export interface RawEmoji {
  hexcode: string
  unicode: string
  label: string
  tags?: string[]
  group?: number
}

/** The group of skin tone and hair components, which are not emoji on their own. */
const COMPONENT_GROUP = 2

/** The most results shown for one query. */
export const MAX_RESULTS = 80

/**
 * Normalizes text for matching: NFKC (full-width letters to half-width),
 * lower case, and katakana to hiragana, so "ＢＥＥＲ", "beer", "ビール" and
 * "びーる" each match their counterpart.
 */
export function normalizeName(s: string): string {
  return s.normalize('NFKC').toLowerCase().replace(/[ァ-ヶ]/g, (c) => String.fromCharCode(c.charCodeAt(0) - 0x60))
}

/**
 * Joins the English and Japanese records by hexcode. Components and records
 * without a group (the regional indicator letters) are left out; skin tone
 * variants are not in the records' top level, so each emoji appears once.
 * Anything the server would refuse as an icon is left out too.
 */
export function buildEmojiNames(en: RawEmoji[], ja: RawEmoji[]): EmojiName[] {
  const jaByHex = new Map(ja.map((x) => [x.hexcode, x]))
  const out: EmojiName[] = []
  for (const e of en) {
    if (e.group === undefined || e.group === COMPONENT_GROUP || !isIcon(e.unicode)) continue
    const j = jaByHex.get(e.hexcode)
    out.push({
      emoji: e.unicode,
      en: e.label,
      ja: j?.label ?? e.label,
      labels: [e.label, ...(j ? [j.label] : [])].map(normalizeName),
      tags: [...(e.tags ?? []), ...(j?.tags ?? [])].map(normalizeName),
    })
  }
  return out
}

/** How strongly an entry matches the whole query; lower is stronger. */
function rank(x: EmojiName, q: string): number {
  if (x.labels.includes(q)) return 0
  if (x.labels.some((l) => l.startsWith(q))) return 1
  if (x.tags.includes(q)) return 2
  if (x.tags.some((t) => t.startsWith(q))) return 3
  return 4
}

/**
 * The emoji whose names or keywords contain every word of query, strongest
 * match first (exact name, name prefix, exact keyword, keyword prefix, then
 * the rest), in data order within each, at most MAX_RESULTS.
 */
export function searchEmoji(names: EmojiName[], query: string): EmojiName[] {
  const q = normalizeName(query.trim())
  const words = q.split(/\s+/).filter(Boolean)
  if (words.length === 0) return []
  const whole = words.join(' ')
  const hits: { x: EmojiName; r: number; i: number }[] = []
  names.forEach((x, i) => {
    const ok = words.every((w) => x.labels.some((l) => l.includes(w)) || x.tags.some((t) => t.includes(w)))
    if (ok) hits.push({ x, r: rank(x, whole), i })
  })
  hits.sort((a, b) => a.r - b.r || a.i - b.i)
  return hits.slice(0, MAX_RESULTS).map((h) => h.x)
}

/** What the picker's field holds. */
export type IconInput = 'empty' | 'icon' | 'invalid' | 'name'

/** Characters that only emoji have: pictographs, flag letters, keycaps and the emoji variation selector. */
const EMOJI_CHAR = /[\p{Extended_Pictographic}\p{Regional_Indicator}⃣️]/u

/**
 * Sorts the field's text: empty; one emoji (set as typed); emoji that are
 * not one icon (refused); or anything without emoji, which is a name to
 * search for.
 */
export function classifyIconInput(s: string): IconInput {
  const v = s.trim()
  if (!v) return 'empty'
  if (!EMOJI_CHAR.test(v)) return 'name'
  return isIcon(v) ? 'icon' : 'invalid'
}

/**
 * Returns a loader that reads the names once and keeps them; a failed read
 * is not kept, so the next open tries again.
 */
export function createNameLoader(read: () => Promise<[RawEmoji[], RawEmoji[]]>): () => Promise<EmojiName[]> {
  let cached: Promise<EmojiName[]> | null = null
  return () => {
    cached ??= read().then(([en, ja]) => buildEmojiNames(en, ja)).catch((err: unknown) => {
      cached = null
      throw err
    })
    return cached
  }
}

/** Loads the names from emojibase-data, as separate chunks of the bundle. */
export const loadEmojiNames = createNameLoader(async () => {
  const [en, ja] = await Promise.all([import('emojibase-data/en/compact.json'), import('emojibase-data/ja/compact.json')])
  return [en.default as unknown as RawEmoji[], ja.default as unknown as RawEmoji[]]
})

/** The name shown as the hint of an emoji, in the screen's language. */
export function emojiHint(x: EmojiName, lang: 'en' | 'ja'): string {
  return lang === 'ja' ? x.ja : x.en
}
