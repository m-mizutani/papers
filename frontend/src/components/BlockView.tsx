import type { Editor as TiptapEditor } from '@tiptap/core'
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import { editorsOf, type PaperState } from '../api/paperState'
import type { Block, BlockProps, BlockType } from '../api/types'
import { useTranslation } from '../i18n'
import { plainText } from '../markup/markup'
import { actorColor, actorName, actorShort } from '../utils/actors'
import { renderMarkup, safeHref } from '../utils/inline'
import { TEXT_TYPES } from './blockTypes'
import { ConflictPanel, type ConflictChoice, type ConflictState } from './ConflictPanel'
import { Editor } from './Editor'
import { Mermaid } from './Mermaid'
import { RichEditor, type Caret, type Slash } from './RichEditor'
import { RichFrame, frameTitle } from './RichFrame'
import { gapBetween } from './spacing'
import { TableBlock } from './TableBlock'

export interface EditingState {
  id: string
  draft: string
  type: BlockType
  props: BlockProps
  /** The block revision the edit is based on; sent as base_rev. */
  base: number
  /** The block's contents at that revision, to tell whether the draft changed. */
  orig: { content: string; type: BlockType; props: BlockProps }
  textChanged: boolean
  /** One editing session: its saves are undone together. */
  session: string
  caret: Caret
}

export type Mark = 'added' | 'changed' | 'removed'

export interface BlockActions {
  startEdit: (b: Block, caret?: Caret) => void
  setDraft: (text: string, changed: boolean) => void
  setDraftProps: (props: BlockProps) => void
  /** Ends the edit; with a block ID, only if that block is the one being edited. */
  finishEdit: (block?: string) => void
  split: (before: string, after: string) => void
  join: (text: string) => void
  arrowOut: (dir: 'up' | 'down') => void
  indent: (shift: boolean) => void
  pasteLines: (lines: string[]) => void
  pasteImage: (file: File) => void
  pickImage: (b: Block) => void
  run: (ops: import('../api/types').Op[]) => Promise<boolean>
  openBlockMenu: (b: Block, anchor: HTMLElement) => void
  startDrag: (e: React.PointerEvent, b: Block) => void
  resolveConflict: (choice: ConflictChoice) => void
  slash: (s: Slash | null) => void
  slashKey: (key: 'ArrowUp' | 'ArrowDown' | 'Enter' | 'Escape') => boolean
  setField: (b: Block, name: string, value: string) => void
  select: (id: string | null) => void
  emojiMenu: (b: Block, anchor: HTMLElement) => void
  onEditor: (ed: TiptapEditor | null) => void
  deleteRow: (table: Block, rowId: string) => void
  deleteCol: (table: Block, colId: string) => void
  /** The latest state, for reading a block right after a write. */
  current: () => PaperState | undefined
}

export interface BlockContext {
  ws: string
  state: PaperState
  canEdit: boolean
  editing: EditingState | null
  conflict: ConflictState | null
  recent: Set<string>
  numbers: Map<string, number>
  selected: string | null
  menuFor: string | null
  marks?: Map<string, Mark>
  slashOpen: boolean
  actions: BlockActions
}

function placeholderFor(type: BlockType, t: ReturnType<typeof useTranslation>['t']): string {
  switch (type) {
    case 'heading': return t('headingPlaceholder')
    case 'bulleted_list_item': case 'numbered_list_item': return t('listPlaceholder')
    case 'todo': return t('todoPlaceholder')
    case 'quote': return t('quotePlaceholder')
    default: return t('blockPlaceholder')
  }
}

function kindClass(type: BlockType, props: BlockProps): string {
  return type === 'heading' ? `t-heading${Math.min(Math.max(props.level ?? 1, 1), 3)}` : `t-${type}`
}

function ChildCard({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const ref = ctx.state.children.get(block.props.paper_id ?? '')
  if (!ref) return <div className="child arch"><span className="pi" aria-hidden>▤</span><span className="ct"><span className="cn">{t('childHidden')}</span></span></div>
  const status = ref.version_number > 0 ? `v${ref.version_number}` : t('draft')
  return (
    <Link className={`child${ref.status === 'archived' ? ' arch' : ''}`} to={`/ws/${ctx.ws}/p/${ref.id}`} onClick={(e) => e.stopPropagation()}>
      <span className="pi" aria-hidden>▤</span>
      <span className="ct">
        <span className="cn" style={{ display: 'block' }}>{ref.title || t('untitled')}</span>
        <span className="cs">{[status, ref.status === 'archived' ? t('archived') : null].filter(Boolean).join(' · ')}</span>
      </span>
      <span className="muted" aria-hidden>→</span>
    </Link>
  )
}

function ImageView({ block, ctx, caption }: { block: Block; ctx: BlockContext; caption: ReactNode }) {
  const { t } = useTranslation()
  const props = ctx.editing?.id === block.id ? ctx.editing.props : block.props
  const src = props.attachment_id ? api.attachmentURL(ctx.ws, block.paper_id, props.attachment_id) : safeHref(props.url ?? '')
  return (
    <div className="img">
      <figure>
        {src
          ? <img src={src} alt={plainText(block.content) || t('typeImage')} loading="lazy" referrerPolicy="no-referrer" />
          : (
            <button type="button" className="empty" style={{ width: '100%', border: '1px dashed var(--line-2)', borderRadius: 'var(--r-md)', padding: 'var(--sp-8)' }}
              disabled={!ctx.canEdit} onClick={(e) => { e.stopPropagation(); ctx.actions.pickImage(block) }}>🖼 {t('pickImage')}</button>
          )}
        <figcaption>{caption}</figcaption>
      </figure>
    </div>
  )
}

/** The text of a block, shown or edited in place. */
function TextBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const ed = ctx.editing?.id === block.id ? ctx.editing : null
  const type = ed?.type ?? block.type
  const props = ed?.props ?? block.props
  const content = ed ? null : (block.content ? renderMarkup(block.content) : null)
  const text = ed ? (
    <RichEditor
      value={ed.draft}
      caret={ed.caret}
      placeholder={type === 'image' ? t('captionPlaceholder') : placeholderFor(type, t)}
      onChange={ctx.actions.setDraft}
      onBlur={() => ctx.actions.finishEdit(block.id)}
      onSlash={type === 'paragraph' ? ctx.actions.slash : undefined}
      onEditor={ctx.actions.onEditor}
      keys={{
        onEscape: () => ctx.actions.finishEdit(block.id),
        onSplit: type === 'image' ? undefined : ctx.actions.split,
        onJoin: type === 'image' ? undefined : ctx.actions.join,
        onArrowOut: ctx.actions.arrowOut,
        onTab: ['bulleted_list_item', 'numbered_list_item', 'todo'].includes(type) ? ctx.actions.indent : undefined,
        slashKey: ctx.slashOpen ? ctx.actions.slashKey : undefined,
      }}
      onPasteText={type === 'image' ? undefined : ctx.actions.pasteLines}
      onPasteImage={ctx.actions.pasteImage}
    />
  ) : (
    <div className="txt" onMouseDown={(e) => {
      if (!ctx.canEdit || e.button !== 0 || (e.target as HTMLElement).closest('a')) return
      e.preventDefault()
      ctx.actions.startEdit(block, { x: e.clientX, y: e.clientY })
    }}>{content}</div>
  )
  // One structure for every text kind, so the editor is not remounted (and
  // keeps the caret and what is being typed) when "- " turns a block into a list.
  const list = type === 'bulleted_list_item' || type === 'numbered_list_item' || type === 'todo'
  if (type === 'image') return <ImageView block={block} ctx={ctx} caption={text} />
  const marker = list
    ? (type === 'todo'
      ? <input type="checkbox" className="cb" aria-label={t('typeTodo')} checked={!!props.checked} disabled={!ctx.canEdit}
          onChange={(e) => void ctx.actions.run([{ op: 'update', block_id: block.id, props: { ...block.props, checked: e.target.checked }, base_rev: block.rev }])} />
      : <span className="mk">{type === 'numbered_list_item' ? `${(props.indent ?? 0) % 2 ? String.fromCharCode(96 + (ctx.numbers.get(block.id) ?? 1)) : ctx.numbers.get(block.id) ?? 1}.` : ['•', '◦', '▪'][(props.indent ?? 0) % 3]}</span>)
    : type === 'callout'
      ? <button type="button" className="emo" aria-label={t('typeCallout')} disabled={!ctx.canEdit} onClick={(e) => { e.stopPropagation(); ctx.actions.emojiMenu(block, e.currentTarget) }}>{props.icon || '💡'}</button>
      : null
  return (
    <div className={list ? 'li' : type === 'callout' ? 'callout' : 'tb-plain'} style={list ? { ['--ind' as string]: props.indent ?? 0 } : undefined}>
      {marker}
      <div className="grow">{text}</div>
    </div>
  )
}

function CodeBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const ed = ctx.editing?.id === block.id ? ctx.editing : null
  return (
    <div className="code">
      <div className="bar">
        {ed
          ? <input value={ed.props.language ?? ''} placeholder={t('codeLanguage')} aria-label={t('codeLanguage')} onMouseDown={(e) => e.stopPropagation()}
              onChange={(e) => ctx.actions.setDraftProps({ ...ed.props, language: e.target.value })} />
          : <span>{block.props.language || ' '}</span>}
        <button type="button" className="btn ghost sm" onMouseDown={(e) => e.preventDefault()} onClick={(e) => { e.stopPropagation(); void navigator.clipboard?.writeText(block.content).catch(() => undefined) }}>{t('copy')}</button>
      </div>
      {ed
        ? <Editor value={ed.draft} label={t('codeText')} onChange={(v) => ctx.actions.setDraft(v, v !== ed.orig.content)} onBlur={() => ctx.actions.finishEdit(block.id)}
            keys={{ onEscape: () => ctx.actions.finishEdit(block.id), multiline: true }} />
        : <pre onMouseDown={(e) => { if (ctx.canEdit && e.button === 0) { e.preventDefault(); ctx.actions.startEdit(block, 'end') } }}><code>{block.content}</code></pre>}
    </div>
  )
}

function Body({ block, ctx }: { block: Block; ctx: BlockContext }) {
  if (TEXT_TYPES.has(ctx.editing?.id === block.id ? ctx.editing.type : block.type) || block.type === 'image') return <TextBody block={block} ctx={ctx} />
  switch (block.type) {
    case 'code':
      return <CodeBody block={block} ctx={ctx} />
    case 'divider':
      return <div className="divider" onClick={() => ctx.actions.select(block.id)}><hr /></div>
    case 'paper':
      return <ChildCard block={block} ctx={ctx} />
    case 'table':
      return <TableBlock block={block} ctx={ctx} />
    case 'html':
    case 'svg': {
      const title = frameTitle(block.content)
      return (
        <div className="rich">
          {title && <div className="bar">{title}</div>}
          <RichFrame kind={block.type} content={block.content} height={block.props.height} title={title ?? actorName(block.created_by)}
            fields={block.props.fields} onField={ctx.canEdit ? (name, value) => ctx.actions.setField(block, name, value) : undefined} />
        </div>
      )
    }
    case 'mermaid':
      return <div className="rich"><Mermaid source={block.content} /></div>
    default:
      return <div className="txt">{renderMarkup(block.content)}</div>
  }
}

export function BlockView({ block, prev, ctx }: { block: Block; prev?: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const editing = ctx.editing?.id === block.id
  const type = editing && ctx.editing ? ctx.editing.type : block.type
  const props = editing && ctx.editing ? ctx.editing.props : block.props
  const others = editorsOf(ctx.state, block.id)
  const mark = ctx.marks?.get(block.id)
  const classes = ['blk', kindClass(type, props), `gap-${gapBetween(prev, { type, props })}`]
  if (editing) classes.push('editing')
  if (ctx.recent.has(block.id)) classes.push('flash')
  if (others.length) classes.push('pres')
  if (ctx.selected === block.id) classes.push('sel')
  if (ctx.menuFor === block.id) classes.push('menu-open')
  if (block.id.startsWith('tmp:')) classes.push('pending')
  if (mark) classes.push(`mark-${mark}`)
  if (props.checked && type === 'todo') classes.push('todo-done')
  const presenter = others[0]
  return (
    <div className={classes.join(' ')} data-block-id={block.id} style={presenter ? { ['--pc' as string]: actorColor(presenter) } : undefined}
      tabIndex={ctx.selected === block.id ? -1 : undefined}>
      <div className="g">
        {ctx.canEdit && !mark && (
          <button type="button" className="hdl" aria-label={t('blockMenu')} title={t('blockMenu')}
            onPointerDown={(e) => ctx.actions.startDrag(e, block)}>⋮⋮</button>
        )}
      </div>
      <div className="bd">
        <Body block={block} ctx={ctx} />
        {presenter && <span className="ptag">{presenter.kind === 'agent' ? '✦ ' : ''}{t('editing', { name: actorShort(presenter) })}</span>}
        {editing && ctx.conflict && ctx.conflict.blockId === block.id && <ConflictPanel conflict={ctx.conflict} onChoose={ctx.actions.resolveConflict} />}
      </div>
    </div>
  )
}
