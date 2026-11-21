import type { BlockProps, BlockType } from '../api/types'
import type { MsgKey } from '../i18n'

// The choices offered in a block's type menu (human-editable types only).
export interface TypeChoice {
  key: string
  label: MsgKey
  type: BlockType
  props: BlockProps
}

export const TYPE_CHOICES: TypeChoice[] = [
  { key: 'paragraph', label: 'typeParagraph', type: 'paragraph', props: {} },
  { key: 'h1', label: 'typeHeading1', type: 'heading', props: { level: 1 } },
  { key: 'h2', label: 'typeHeading2', type: 'heading', props: { level: 2 } },
  { key: 'h3', label: 'typeHeading3', type: 'heading', props: { level: 3 } },
  { key: 'bullet', label: 'typeBullet', type: 'bulleted_list_item', props: {} },
  { key: 'numbered', label: 'typeNumbered', type: 'numbered_list_item', props: {} },
  { key: 'todo', label: 'typeTodo', type: 'todo', props: {} },
  { key: 'quote', label: 'typeQuote', type: 'quote', props: {} },
  { key: 'callout', label: 'typeCallout', type: 'callout', props: { icon: '💡' } },
  { key: 'code', label: 'typeCode', type: 'code', props: {} },
  { key: 'divider', label: 'typeDivider', type: 'divider', props: {} },
]

/** What the "add" menu can insert besides a paragraph. */
export const INSERT_CHOICES: { key: string; label: MsgKey }[] = [
  { key: 'table', label: 'typeTable' },
  { key: 'image', label: 'typeImage' },
  { key: 'paper', label: 'typeChildPaper' },
]

export function choiceKey(type: BlockType, props: BlockProps): string {
  if (type === 'heading') return `h${props.level ?? 1}`
  return TYPE_CHOICES.find((c) => c.type === type)?.key ?? ''
}

export const HUMAN_EDITABLE = new Set<BlockType>([
  'paragraph', 'heading', 'quote', 'bulleted_list_item', 'numbered_list_item', 'todo', 'callout', 'code', 'divider', 'image', 'table', 'table_cell', 'paper',
])

/** Types whose Enter key continues with a new block of the same type. */
export const CONTINUING = new Set<BlockType>(['bulleted_list_item', 'numbered_list_item', 'todo'])

export const MULTILINE = new Set<BlockType>(['code', 'quote', 'callout'])
