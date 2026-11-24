import type { BlockProps, BlockType } from '../api/types'
import type { MsgKey } from '../i18n'

// The kinds of block people can make: the "/" menu, "Turn into" and the
// Markdown shortcuts all use this list.
export interface TypeChoice {
  key: string
  label: MsgKey
  icon: string
  /** Words the "/" menu matches, in English and Japanese. */
  keywords: string
  group: 'basic' | 'media' | 'paper'
  type: BlockType
  props: BlockProps
  /** A short note next to the label (the "/" menu's paper group). */
  hint?: MsgKey
  /** For a paper block: what kind of paper it creates. */
  paperKind?: 'database'
}

export const TYPE_CHOICES: TypeChoice[] = [
  { key: 'paragraph', label: 'typeParagraph', icon: 'T', keywords: 'text paragraph テキスト 本文', group: 'basic', type: 'paragraph', props: {} },
  { key: 'h1', label: 'typeHeading1', icon: 'H1', keywords: 'heading h1 見出し', group: 'basic', type: 'heading', props: { level: 1 } },
  { key: 'h2', label: 'typeHeading2', icon: 'H2', keywords: 'heading h2 見出し', group: 'basic', type: 'heading', props: { level: 2 } },
  { key: 'h3', label: 'typeHeading3', icon: 'H3', keywords: 'heading h3 見出し', group: 'basic', type: 'heading', props: { level: 3 } },
  { key: 'bullet', label: 'typeBullet', icon: '•', keywords: 'bullet list ul 箇条書き リスト', group: 'basic', type: 'bulleted_list_item', props: { indent: 0 } },
  { key: 'numbered', label: 'typeNumbered', icon: '1.', keywords: 'numbered ordered ol 番号 リスト', group: 'basic', type: 'numbered_list_item', props: { indent: 0 } },
  { key: 'todo', label: 'typeTodo', icon: '☐', keywords: 'todo task check やること タスク チェック', group: 'basic', type: 'todo', props: { indent: 0, checked: false } },
  { key: 'quote', label: 'typeQuote', icon: '❝', keywords: 'quote 引用', group: 'basic', type: 'quote', props: {} },
  { key: 'callout', label: 'typeCallout', icon: '💡', keywords: 'callout note 注意 コールアウト', group: 'basic', type: 'callout', props: { icon: '💡' } },
  { key: 'code', label: 'typeCode', icon: '</>', keywords: 'code コード', group: 'basic', type: 'code', props: {} },
  { key: 'divider', label: 'typeDivider', icon: '—', keywords: 'divider hr line 区切り 線', group: 'basic', type: 'divider', props: {} },
  { key: 'table', label: 'typeTable', icon: '▦', keywords: 'table 表 テーブル', group: 'media', type: 'table', props: { header_row: true } },
  { key: 'image', label: 'typeImage', icon: '🖼', keywords: 'image picture 画像 写真', group: 'media', type: 'image', props: {} },
  { key: 'paper', label: 'typePage', hint: 'typeChildPaperHint', icon: '▤', keywords: 'page paper child 子 ペーパー ページ', group: 'paper', type: 'paper', props: {} },
  { key: 'database', label: 'typeDatabase', hint: 'typeDatabaseHint', icon: '⊞', keywords: 'database table db データベース 表', group: 'paper', type: 'paper', props: {}, paperKind: 'database' },
]

/** The choices of "Turn into": text kinds only. */
export const TURN_INTO = TYPE_CHOICES.filter((c) => c.group === 'basic' && c.type !== 'divider')

export function matchChoices(query: string, label: (k: MsgKey) => string, allowPaper: boolean): TypeChoice[] {
  const q = query.trim().toLowerCase()
  return TYPE_CHOICES.filter((c) => (c.type !== 'paper' || allowPaper)
    && (!q || label(c.label).toLowerCase().includes(q) || c.keywords.toLowerCase().includes(q)))
}

export const HUMAN_EDITABLE = new Set<BlockType>([
  'paragraph', 'heading', 'quote', 'bulleted_list_item', 'numbered_list_item', 'todo', 'callout', 'code', 'divider', 'image', 'table', 'table_cell', 'paper',
])

/** Types edited with the rich text editor. */
export const TEXT_TYPES = new Set<BlockType>(['paragraph', 'heading', 'quote', 'bulleted_list_item', 'numbered_list_item', 'todo', 'callout'])

/** Types whose Enter key continues with a new block of the same type. */
export const CONTINUING = new Set<BlockType>(['bulleted_list_item', 'numbered_list_item', 'todo'])

/** Enter adds a line break; Shift+Enter (code) or Mod+Enter leaves. */
export const MULTILINE = new Set<BlockType>(['code'])
