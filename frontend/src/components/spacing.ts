import type { Block } from '../api/types'

// The space above a block depends on the block before it (spec: block
// rhythm). The returned name is a CSS class `gap-*` whose value is a token
// in styles/global.css.

export type Gap =
  | 'none' | 'list' | 'text' | 'head-after' | 'h1' | 'h2' | 'h3' | 'head-pair' | 'head-box' | 'box' | 'card' | 'card-run' | 'quote'

type Kind = 'list' | 'heading' | 'box' | 'card' | 'divider' | 'quote' | 'text'

const LIST = new Set(['bulleted_list_item', 'numbered_list_item', 'todo'])
const BOX = new Set(['callout', 'code', 'table', 'image', 'svg', 'html', 'mermaid'])

function kindOf(b: Pick<Block, 'type'>): Kind {
  if (LIST.has(b.type)) return 'list'
  if (b.type === 'heading') return 'heading'
  if (BOX.has(b.type)) return 'box'
  if (b.type === 'paper') return 'card'
  if (b.type === 'divider') return 'divider'
  if (b.type === 'quote') return 'quote'
  return 'text'
}

export function gapBetween(prev: Pick<Block, 'type' | 'props'> | undefined, next: Pick<Block, 'type' | 'props'>): Gap {
  if (!prev) return 'none'
  const a = kindOf(prev)
  const b = kindOf(next)
  if (a === 'divider' || b === 'divider') return 'none'
  if (b === 'heading') {
    if (a === 'heading') return 'head-pair'
    const level = Math.min(Math.max(next.props.level ?? 1, 1), 3)
    return level === 1 ? 'h1' : level === 2 ? 'h2' : 'h3'
  }
  if (a === 'heading') return b === 'box' || b === 'card' ? 'head-box' : 'head-after'
  if (a === 'card' && b === 'card') return 'card-run'
  if (a === 'card' || b === 'card') return 'card'
  if (a === 'box' || b === 'box') return 'box'
  if (a === 'list' && b === 'list') return 'list'
  if ((a === 'quote') !== (b === 'quote')) return 'quote'
  return 'text'
}
