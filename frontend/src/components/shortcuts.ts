import type { BlockProps, BlockType } from '../api/types'

export interface Conversion {
  type: BlockType
  props: BlockProps
  content: string
}

// Markdown-style shortcuts typed at the start of a text block.
const RULES: { re: RegExp; type: BlockType; props?: (m: RegExpMatchArray) => BlockProps }[] = [
  { re: /^(#{1,3}) /, type: 'heading', props: (m) => ({ level: m[1]?.length ?? 1 }) },
  { re: /^\[( |x)?\] /, type: 'todo', props: (m) => ({ checked: m[1] === 'x' }) },
  { re: /^[-*] /, type: 'bulleted_list_item' },
  { re: /^1[.)] /, type: 'numbered_list_item' },
  { re: /^> /, type: 'quote' },
  { re: /^```(\w*)$/, type: 'code', props: (m) => ({ language: m[1] ?? '' }) },
  { re: /^---$/, type: 'divider' },
]

export function detectShortcut(text: string): Conversion | undefined {
  for (const r of RULES) {
    const m = text.match(r.re)
    if (m) {
      return { type: r.type, props: r.props?.(m) ?? {}, content: r.type === 'divider' ? '' : text.slice(m[0].length) }
    }
  }
  return undefined
}
