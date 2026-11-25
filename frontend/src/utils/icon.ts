import type { PaperKind } from '../api/types'

// A paper's icon: the emoji set for it, or a default chosen by its kind.
// Defaults are shown only; they are never stored.

/** What screens show for a paper without an icon. */
export const DEFAULT_ICONS = {
  home: '🧭',
  blocks: '📄',
  database: '🗃️',
  html: '🧩',
} as const

/** The emoji the picker offers. Any other one can be typed. */
export const PICKER_ICONS = [
  '📄', '📝', '📌', '📎', '📚', '📖', '🗂️', '🗃️',
  '📊', '📈', '📅', '✅', '💡', '🎯', '🚀', '⭐',
  '🔥', '⚠️', '❗', '❓', '🔒', '🛡️', '🔑', '🐛',
  '🧪', '🛠️', '⚙️', '🧭', '🌐', '💬', '📣', '🤖',
  '👥', '🏠', '🍳', '🍝', '✈️', '🎉', '❤️', '🌱',
]

/** The longest icon in bytes (the server checks the same). */
export const MAX_ICON_BYTES = 32

/** The icon a paper shows: its own, else the default for the home or its kind. */
export function paperIcon(p: { id: string; icon?: string; kind?: PaperKind }, homeId?: string): string {
  if (p.icon) return p.icon
  if (homeId && p.id === homeId) return DEFAULT_ICONS.home
  if (p.kind === 'database') return DEFAULT_ICONS.database
  if (p.kind === 'html') return DEFAULT_ICONS.html
  return DEFAULT_ICONS.blocks
}

/**
 * Whether s (trimmed) can be an icon: one grapheme cluster (one character on
 * screen) of at most 32 bytes, without spaces or control characters, and
 * not made only of invisible characters (format characters and combining
 * marks). Empty is not an icon here; removing one is a separate action.
 */
export function isIcon(s: string): boolean {
  const v = s.trim()
  if (!v || new TextEncoder().encode(v).length > MAX_ICON_BYTES) return false
  if (/[\s\p{Cc}]/u.test(v)) return false
  // a cluster of only format characters and combining marks shows nothing
  if (!/[^\p{Cf}\p{Mn}\p{Me}]/u.test(v)) return false
  const seg = new Intl.Segmenter(undefined, { granularity: 'grapheme' })
  let n = 0
  for (const _ of seg.segment(v)) {
    void _
    if (++n > 1) return false
  }
  return n === 1
}
