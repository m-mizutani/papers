import type { ReactNode } from 'react'
import type { Block, BlockProps, BlockType, Op } from '../api/types'
import { actorLabel, cellsOf, foreignLock, type PaperState } from '../api/paperState'
import { useTranslation } from '../i18n'
import { renderLines, safeHref } from '../utils/inline'
import { Editor } from './Editor'
import { Mermaid } from './Mermaid'
import { RichFrame } from './RichFrame'
import { HUMAN_EDITABLE, MULTILINE, TYPE_CHOICES, choiceKey } from './blockTypes'

export interface EditingState {
  id: string
  draft: string
  type: BlockType
  props: BlockProps
  forced: boolean
}

export interface BlockActions {
  startEdit: (b: Block, force?: boolean) => void
  setDraft: (text: string) => void
  setDraftProps: (props: BlockProps) => void
  finishEdit: () => void
  splitAfter: () => void
  removeEmpty: () => void
  run: (ops: Op[], message?: string) => Promise<boolean>
  move: (b: Block, dir: -1 | 1) => void
}

export interface BlockContext {
  state: PaperState
  myKey: string
  canEdit: boolean
  now: number
  editing: EditingState | null
  recent: Set<string>
  numbers: Map<string, number>
  actions: BlockActions
}

function headingClass(level?: number) {
  return `b-heading-${Math.min(Math.max(level ?? 1, 1), 3)}`
}

/** Read-only rendering of a block's body. */
function Body({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const text = renderLines(block.content)
  const indent = { paddingLeft: `${(block.props.indent ?? 0) * 1.5}rem` }
  switch (block.type) {
    case 'heading': {
      const level = Math.min(Math.max(block.props.level ?? 1, 1), 3)
      const Tag = (`h${level}`) as 'h1' | 'h2' | 'h3'
      return <Tag className={headingClass(level)}>{text}</Tag>
    }
    case 'quote':
      return <blockquote className="b-quote">{text}</blockquote>
    case 'callout':
      return (
        <div className="b-callout">
          <span className="b-callout-icon" aria-hidden>{block.props.icon || '💡'}</span>
          <div>{text}</div>
        </div>
      )
    case 'bulleted_list_item':
    case 'numbered_list_item':
      return (
        <div className="b-list" style={indent}>
          <span className="b-list-marker">{block.type === 'numbered_list_item' ? `${ctx.numbers.get(block.id) ?? 1}.` : ['•', '◦', '▪'][(block.props.indent ?? 0) % 3]}</span>
          <div className="b-list-body">{text}</div>
        </div>
      )
    case 'todo':
      return (
        <div className={`b-list${block.props.checked ? ' b-todo-done' : ''}`} style={indent}>
          <input
            type="checkbox"
            className="b-todo-box"
            checked={!!block.props.checked}
            disabled={!ctx.canEdit}
            onClick={(e) => e.stopPropagation()}
            onChange={(e) => void ctx.actions.run([{ op: 'update', block_id: block.id, props: { ...block.props, checked: e.target.checked }, base_version: block.version }])}
          />
          <div className="b-list-body">{text}</div>
        </div>
      )
    case 'code':
      return (
        <pre className="b-code">
          {block.props.language && <span className="b-code-lang">{block.props.language}</span>}
          <code>{block.content}</code>
        </pre>
      )
    case 'divider':
      return <hr className="b-divider" />
    case 'image': {
      const src = safeHref(block.props.url ?? '')
      return (
        <figure className="b-image">
          {src && <img src={src} alt={block.content} loading="lazy" referrerPolicy="no-referrer" />}
          {block.content && <figcaption>{block.content}</figcaption>}
        </figure>
      )
    }
    case 'html':
    case 'svg':
      return (
        <div className="b-rich">
          <RichFrame kind={block.type} content={block.content} height={block.props.height} title={actorLabel(block.updated_by)} />
        </div>
      )
    case 'mermaid':
      return <div className="b-rich"><Mermaid source={block.content} /></div>
    case 'table':
      return <TableBody block={block} ctx={ctx} />
    default:
      return <p className="b-paragraph">{text.length ? text : ' '}</p>
  }
}

function placeholderFor(type: BlockType, t: ReturnType<typeof useTranslation>['t']): string | undefined {
  return type === 'paragraph' ? t('addBlock') : undefined
}

/** The in-place editor of a text-like block (or table cell). */
function EditBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const ed = ctx.editing as EditingState
  const editor = (
    <Editor
      value={ed.draft}
      label={t('blockType')}
      placeholder={placeholderFor(ed.type, t)}
      onChange={ctx.actions.setDraft}
      onBlur={ctx.actions.finishEdit}
      keys={{
        onEscape: ctx.actions.finishEdit,
        onEnter: block.type === 'table_cell' ? ctx.actions.finishEdit : ctx.actions.splitAfter,
        onBackspaceEmpty: block.type === 'table_cell' ? undefined : ctx.actions.removeEmpty,
        multiline: MULTILINE.has(ed.type),
      }}
    />
  )
  const indent = { paddingLeft: `${(ed.props.indent ?? 0) * 1.5}rem` }
  switch (ed.type) {
    case 'heading':
      return <div className={headingClass(ed.props.level)}>{editor}</div>
    case 'quote':
      return <div className="b-quote">{editor}</div>
    case 'callout':
      return (
        <div className="b-callout">
          <span className="b-callout-icon" aria-hidden>{ed.props.icon || '💡'}</span>
          <div style={{ flex: 1 }}>{editor}</div>
        </div>
      )
    case 'bulleted_list_item':
    case 'numbered_list_item':
    case 'todo':
      return (
        <div className="b-list" style={indent}>
          <span className="b-list-marker">{ed.type === 'numbered_list_item' ? `${ctx.numbers.get(block.id) ?? 1}.` : ed.type === 'todo' ? '☐' : '•'}</span>
          <div className="b-list-body">{editor}</div>
        </div>
      )
    case 'code':
      return (
        <div>
          <div className="b-code">{editor}</div>
          <div className="editor-meta">
            <input
              className="field"
              value={ed.props.language ?? ''}
              placeholder={t('codeLanguage')}
              aria-label={t('codeLanguage')}
              onMouseDown={(e) => e.stopPropagation()}
              onChange={(e) => ctx.actions.setDraftProps({ ...ed.props, language: e.target.value })}
            />
          </div>
        </div>
      )
    case 'image':
      return (
        <div>
          <Body block={block} ctx={ctx} />
          {editor}
          <div className="editor-meta">
            <input
              className="field"
              style={{ flex: 1 }}
              value={ed.props.url ?? ''}
              placeholder={t('imageUrl')}
              aria-label={t('imageUrl')}
              onChange={(e) => ctx.actions.setDraftProps({ ...ed.props, url: e.target.value })}
            />
          </div>
        </div>
      )
    case 'table_cell':
      return <div className="cell editing">{editor}</div>
    default:
      return <div className="b-paragraph">{editor}</div>
  }
}

function TableBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const cells = cellsOf(ctx.state, block.id)
  const rows = block.props.rows ?? 0
  const cols = block.props.cols ?? 0
  const resize = (r: number, c: number) =>
    void ctx.actions.run([{ op: 'update', block_id: block.id, props: { ...block.props, rows: r, cols: c }, base_version: block.version }])

  return (
    <div className="b-table">
      <table>
        <tbody>
          {Array.from({ length: rows }, (_, r) => (
            <tr key={r} className={r === 0 && block.props.header_row ? 'header' : undefined}>
              {Array.from({ length: cols }, (_, c) => {
                const cell = cells.get(`${r}:${c}`)
                if (!cell) return <td key={c} />
                const editing = ctx.editing?.id === cell.id
                const held = foreignLock(ctx.state, cell.id, ctx.myKey, ctx.now)
                return (
                  <td key={c} onClick={() => !editing && ctx.canEdit && ctx.actions.startEdit(cell, false)}>
                    {held && <span className="held-badge">{t('editedBy', { name: actorLabel(held.holder) })}</span>}
                    {editing ? <EditBody block={cell} ctx={ctx} /> : <div className={`cell${held ? ' held' : ''}`}>{renderLines(cell.content)}</div>}
                  </td>
                )
              })}
            </tr>
          ))}
        </tbody>
      </table>
      {ctx.canEdit && (
        <div className="table-tools">
          <button type="button" className="btn btn-quiet" onClick={() => resize(rows + 1, cols)}>{t('addRow')}</button>
          <button type="button" className="btn btn-quiet" onClick={() => resize(rows, cols + 1)}>{t('addColumn')}</button>
          <button type="button" className="btn btn-quiet" disabled={rows <= 1} onClick={() => resize(rows - 1, cols)}>{t('removeRow')}</button>
          <button type="button" className="btn btn-quiet" disabled={cols <= 1} onClick={() => resize(rows, cols - 1)}>{t('removeColumn')}</button>
        </div>
      )}
    </div>
  )
}

function Tools({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const human = HUMAN_EDITABLE.has(block.type)
  const setType = (key: string) => {
    const choice = TYPE_CHOICES.find((c) => c.key === key)
    if (!choice) return
    const content = choice.type === 'divider' || choice.type === 'table' ? '' : block.content
    void ctx.actions.run([{ op: 'update', block_id: block.id, type: choice.type, props: { ...choice.props, indent: block.props.indent }, content, base_version: block.version }])
  }
  return (
    <div className="block-tools" onMouseDown={(e) => e.stopPropagation()}>
      {human && block.type !== 'table' && (
        <select className="type-select" aria-label={t('blockType')} value={choiceKey(block.type, block.props)} onChange={(e) => setType(e.target.value)}>
          {TYPE_CHOICES.filter((c) => c.type !== 'table').map((c) => (
            <option key={c.key} value={c.key}>{t(c.label)}</option>
          ))}
        </select>
      )}
      <button type="button" className="tool" title={t('moveUp')} aria-label={t('moveUp')} onClick={() => ctx.actions.move(block, -1)}>↑</button>
      <button type="button" className="tool" title={t('moveDown')} aria-label={t('moveDown')} onClick={() => ctx.actions.move(block, 1)}>↓</button>
      <button type="button" className="tool" title={t('deleteBlock')} aria-label={t('deleteBlock')} onClick={() => void ctx.actions.run([{ op: 'delete', block_id: block.id }])}>×</button>
    </div>
  )
}

export function BlockView({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const editing = ctx.editing?.id === block.id
  const held = foreignLock(ctx.state, block.id, ctx.myKey, ctx.now)
  const human = HUMAN_EDITABLE.has(block.type)
  const byAgent = block.updated_by.kind === 'agent'

  const classes = ['block']
  if (byAgent) classes.push('by-agent')
  if (editing) classes.push('editing')
  else if (held) classes.push('held')
  if (ctx.recent.has(block.id)) classes.push('recent')

  // Tables edit per cell; dividers and rich blocks have no text editor.
  const textEditable = ctx.canEdit && human && block.type !== 'table' && block.type !== 'divider'

  let badge: ReactNode = null
  if (held && !editing) {
    badge = (
      <span className={`held-badge${held.holder.kind === 'agent' ? ' agent' : ''}`}>
        {t('editedBy', { name: actorLabel(held.holder) })}
      </span>
    )
  }

  return (
    <div
      className={classes.join(' ')}
      data-block-id={block.id}
      onClick={() => {
        if (!editing && textEditable && !held) ctx.actions.startEdit(block, false)
      }}
    >
      {badge}
      {editing ? <EditBody block={block} ctx={ctx} /> : <Body block={block} ctx={ctx} />}
      {held && !editing && textEditable && (
        <button type="button" className="btn btn-quiet" onClick={(e) => { e.stopPropagation(); ctx.actions.startEdit(block, true) }}>
          {t('editAnyway')}
        </button>
      )}
      {!human && <div className="ai-note">{t('aiOnlyBlock')}</div>}
      {ctx.canEdit && !editing && !held && <Tools block={block} ctx={ctx} />}
    </div>
  )
}

