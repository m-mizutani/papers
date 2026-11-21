import { Link } from 'react-router'
import type { Block, BlockProps, BlockType, Conflict, Op, TableLine } from '../api/types'
import { api } from '../api/client'
import { actorLabel, cellsOf, editorsOf, sortedLines, type PaperState } from '../api/paperState'
import { useTranslation } from '../i18n'
import { plainText } from '../markup/markup'
import { renderMarkup, safeHref } from '../utils/inline'
import { Editor } from './Editor'
import { Mermaid } from './Mermaid'
import { RichEditor } from './RichEditor'
import { RichFrame } from './RichFrame'
import { HUMAN_EDITABLE, MULTILINE, TYPE_CHOICES, choiceKey } from './blockTypes'

export interface EditingState {
  id: string
  draft: string
  type: BlockType
  props: BlockProps
  /** The block revision the edit is based on; sent as base_rev. */
  base: number
  /** The block's contents at that revision, to tell whether the draft changed. */
  orig: { content: string; type: BlockType; props: BlockProps }
  /** The rich editor reports whether the text changed at all. */
  textChanged: boolean
}

/** A batch the server rejected because blocks changed since their base. */
export interface ConflictState {
  ops: Op[]
  conflicts: Conflict[]
  /** The draft of the block being edited when the conflict happened. */
  draft?: string
}

export interface BlockActions {
  startEdit: (b: Block) => void
  setDraft: (text: string, changed: boolean) => void
  setDraftProps: (props: BlockProps) => void
  /** Ends the edit; with a block ID, only if that block is the one being edited. */
  finishEdit: (block?: string) => void
  split: (before: string, after: string) => void
  join: (text: string) => void
  pasteParagraphs: (paragraphs: string[]) => void
  pasteImage: (file: File) => void
  run: (ops: Op[]) => Promise<boolean>
  move: (b: Block, dir: -1 | 1) => void
  remove: (b: Block) => void
  resolveConflict: (choice: 'mine' | 'theirs' | 'continue') => void
}

export interface BlockContext {
  ws: string
  state: PaperState
  canEdit: boolean
  editing: EditingState | null
  conflict: ConflictState | null
  recent: Set<string>
  numbers: Map<string, number>
  /** Set when showing a saved version: blocks are read-only. */
  version?: number
  actions: BlockActions
}

function headingClass(level?: number) {
  return `b-heading-${Math.min(Math.max(level ?? 1, 1), 3)}`
}

function ImageBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const src = block.props.attachment_id ? api.attachmentURL(ctx.ws, block.paper_id, block.props.attachment_id) : safeHref(block.props.url ?? '')
  return (
    <figure className="b-image">
      {src && <img src={src} alt={plainText(block.content)} loading="lazy" referrerPolicy="no-referrer" />}
      {block.content && <figcaption>{renderMarkup(block.content)}</figcaption>}
    </figure>
  )
}

function PaperBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const ref = ctx.state.children.get(block.props.paper_id ?? '')
  if (!ref) return <div className="b-paper b-paper-hidden">{t('childHidden')}</div>
  return (
    <Link className={`b-paper status-${ref.status}`} to={`/ws/${ctx.ws}/p/${ref.id}`} onClick={(e) => e.stopPropagation()}>
      <span className="b-paper-icon" aria-hidden>▤</span>
      <span className="b-paper-title">{ref.title || t('untitled')}</span>
      {ref.status === 'archived' && <span className="badge">{t('statusArchived')}</span>}
      {ref.status === 'draft' && <span className="badge badge-quiet">{t('statusDraft')}</span>}
    </Link>
  )
}

/** Read-only rendering of a block's body. */
function Body({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const text = renderMarkup(block.content)
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
            onChange={(e) => void ctx.actions.run([{ op: 'update', block_id: block.id, props: { ...block.props, checked: e.target.checked }, base_rev: block.rev }])}
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
    case 'image':
      return <ImageBody block={block} ctx={ctx} />
    case 'paper':
      return <PaperBody block={block} ctx={ctx} />
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

/** The in-place editor of a text-like block (or table cell). */
function EditBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const ed = ctx.editing as EditingState
  const isCell = block.type === 'table_cell'
  const rich = (
    <RichEditor
      value={ed.draft}
      placeholder={ed.type === 'paragraph' ? t('typeHere') : undefined}
      onChange={ctx.actions.setDraft}
      onBlur={() => ctx.actions.finishEdit(block.id)}
      keys={{
        onEscape: () => ctx.actions.finishEdit(block.id),
        onSplit: isCell || MULTILINE.has(ed.type) ? undefined : ctx.actions.split,
        onJoin: isCell ? undefined : ctx.actions.join,
        multiline: MULTILINE.has(ed.type),
      }}
      onPasteText={isCell ? undefined : ctx.actions.pasteParagraphs}
      onPasteImage={ctx.actions.pasteImage}
    />
  )
  const indent = { paddingLeft: `${(ed.props.indent ?? 0) * 1.5}rem` }
  switch (ed.type) {
    case 'heading':
      return <div className={headingClass(ed.props.level)}>{rich}</div>
    case 'quote':
      return <div className="b-quote">{rich}</div>
    case 'callout':
      return (
        <div className="b-callout">
          <span className="b-callout-icon" aria-hidden>{ed.props.icon || '💡'}</span>
          <div style={{ flex: 1 }}>{rich}</div>
        </div>
      )
    case 'bulleted_list_item':
    case 'numbered_list_item':
    case 'todo':
      return (
        <div className="b-list" style={indent}>
          <span className="b-list-marker">{ed.type === 'numbered_list_item' ? `${ctx.numbers.get(block.id) ?? 1}.` : ed.type === 'todo' ? '☐' : '•'}</span>
          <div className="b-list-body">{rich}</div>
        </div>
      )
    case 'code':
      return (
        <div>
          <div className="b-code">
            <Editor
              value={ed.draft}
              label={t('typeCode')}
              onChange={(v) => ctx.actions.setDraft(v, v !== ed.orig.content)}
              onBlur={() => ctx.actions.finishEdit(block.id)}
              keys={{ onEscape: () => ctx.actions.finishEdit(block.id), multiline: true }}
            />
          </div>
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
          <ImageBody block={{ ...block, props: ed.props }} ctx={ctx} />
          {rich}
          {!ed.props.attachment_id && (
            <div className="editor-meta">
              <input
                className="field"
                style={{ flex: 1 }}
                value={ed.props.url ?? ''}
                placeholder={t('imageUrl')}
                aria-label={t('imageUrl')}
                onMouseDown={(e) => e.stopPropagation()}
                onChange={(e) => ctx.actions.setDraftProps({ ...ed.props, url: e.target.value })}
              />
            </div>
          )}
        </div>
      )
    case 'table_cell':
      return <div className="cell editing">{rich}</div>
    default:
      return <div className="b-paragraph">{rich}</div>
  }
}

function TableBody({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const cells = cellsOf(ctx.state.blocks.values(), block.id)
  const rows = sortedLines(block.props.rows)
  const cols = sortedLines(block.props.cols)
  const run = (op: Op) => void ctx.actions.run([op])
  const lastRow = rows[rows.length - 1]
  const lastCol = cols[cols.length - 1]
  const renderRow = (row: TableLine, r: number) => (
            <tr key={row.id} className={r === 0 && block.props.header_row ? 'header' : undefined}>
              {cols.map((col) => {
                const cell = cells.get(`${row.id}:${col.id}`)
                if (!cell) return <td key={col.id} />
                const editing = ctx.editing?.id === cell.id
                const others = editorsOf(ctx.state, cell.id)
                return (
                  <td key={col.id} className={others.length ? 'being-edited' : undefined} title={others.map((a) => actorLabel(a)).join(', ') || undefined}
                    onClick={(e) => { e.stopPropagation(); if (!editing && ctx.canEdit) ctx.actions.startEdit(cell) }}>
                    {editing ? <EditState block={cell} ctx={ctx} /> : <div className="cell">{renderMarkup(cell.content)}</div>}
                  </td>
                )
              })}
              {ctx.canEdit && (
                <td className="line-tools" onClick={(e) => e.stopPropagation()}>
                  <button type="button" className="tool" title={t('insertRowBelow')} aria-label={t('insertRowBelow')} onClick={() => run({ op: 'insert_row', block_id: block.id, after: row.id })}>+</button>
                  <button type="button" className="tool" title={t('removeRow')} aria-label={t('removeRow')} disabled={rows.length <= 1} onClick={() => run({ op: 'delete_row', block_id: block.id, line_id: row.id })}>×</button>
                </td>
              )}
            </tr>
          )

  return (
    <div className="b-table">
      <table>
        {block.props.header_row && rows[0] && <thead>{renderRow(rows[0], 0)}</thead>}
        <tbody>
          {rows.map((row, r) => (r === 0 && block.props.header_row ? null : renderRow(row, r)))}
          {ctx.canEdit && (
            <tr className="line-tools" onClick={(e) => e.stopPropagation()}>
              {cols.map((col) => (
                <td key={col.id}>
                  <button type="button" className="tool" title={t('insertColumnRight')} aria-label={t('insertColumnRight')} onClick={() => run({ op: 'insert_col', block_id: block.id, after: col.id })}>+</button>
                  <button type="button" className="tool" title={t('removeColumn')} aria-label={t('removeColumn')} disabled={cols.length <= 1} onClick={() => run({ op: 'delete_col', block_id: block.id, line_id: col.id })}>×</button>
                </td>
              ))}
              <td />
            </tr>
          )}
        </tbody>
      </table>
      {ctx.canEdit && (
        <div className="table-tools" onClick={(e) => e.stopPropagation()}>
          <button type="button" className="btn btn-quiet" onClick={() => run({ op: 'insert_row', block_id: block.id, after: lastRow?.id })}>{t('addRow')}</button>
          <button type="button" className="btn btn-quiet" onClick={() => run({ op: 'insert_col', block_id: block.id, after: lastCol?.id })}>{t('addColumn')}</button>
          <label className="check">
            <input type="checkbox" checked={!!block.props.header_row} onChange={(e) => run({ op: 'update', block_id: block.id, base_rev: block.rev, props: { ...block.props, header_row: e.target.checked } })} />
            {t('headerRow')}
          </label>
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
    const content = choice.type === 'divider' ? '' : block.content
    void ctx.actions.run([{ op: 'update', block_id: block.id, type: choice.type, props: { ...choice.props, indent: block.props.indent }, content, base_rev: block.rev }])
  }
  const typeMenu = human && block.type !== 'table' && block.type !== 'paper' && block.type !== 'image'
  return (
    <div className="block-tools" onMouseDown={(e) => e.stopPropagation()} onClick={(e) => e.stopPropagation()}>
      {typeMenu && (
        <select className="type-select" aria-label={t('blockType')} value={choiceKey(block.type, block.props)} onChange={(e) => setType(e.target.value)}>
          {TYPE_CHOICES.map((c) => (
            <option key={c.key} value={c.key}>{t(c.label)}</option>
          ))}
        </select>
      )}
      <button type="button" className="tool" title={t('moveUp')} aria-label={t('moveUp')} onClick={() => ctx.actions.move(block, -1)}>↑</button>
      <button type="button" className="tool" title={t('moveDown')} aria-label={t('moveDown')} onClick={() => ctx.actions.move(block, 1)}>↓</button>
      <button type="button" className="tool" title={t('deleteBlock')} aria-label={t('deleteBlock')} onClick={() => ctx.actions.remove(block)}>×</button>
    </div>
  )
}

/**
 * The choice offered when a save was rejected because someone changed the
 * block first: their contents next to mine, and what to keep.
 */
export function ConflictBox({ conflict, ctx }: { conflict: ConflictState; ctx: BlockContext }) {
  const { t } = useTranslation()
  const editingId = ctx.editing?.id
  const hit = conflict.conflicts.find((c) => c.block_id === editingId) ?? conflict.conflicts[0]
  if (!hit) return null
  const theirs = hit.current
  const mine = hit.block_id === editingId ? ctx.editing?.draft : conflict.draft
  const deleted = !theirs
  const title = deleted
    ? t('conflictDeleted')
    : mine === undefined
      ? t('conflictOther')
      : t('conflictTitle', { name: actorLabel(theirs.updated_by) })
  // Keep focus in the editor: a blur would try to save again.
  const keepFocus = (e: { preventDefault: () => void; stopPropagation: () => void }) => {
    e.preventDefault()
    e.stopPropagation()
  }
  return (
    <div className="conflict" role="alert" onMouseDown={keepFocus} onClick={(e) => e.stopPropagation()}>
      <p className="conflict-title">{title}</p>
      {(theirs || mine !== undefined) && (
        <div className="conflict-compare">
          {theirs && (
            <div>
              <div className="conflict-label">{t('conflictTheirs')}</div>
              <div className="conflict-text">{theirs.content ? renderMarkup(theirs.content) : '—'}</div>
            </div>
          )}
          {mine !== undefined && (
            <div>
              <div className="conflict-label">{t('conflictMine')}</div>
              <div className="conflict-text">{mine ? renderMarkup(mine) : '—'}</div>
            </div>
          )}
        </div>
      )}
      <div className="conflict-actions">
        <button type="button" className="btn btn-primary" onClick={() => ctx.actions.resolveConflict('mine')}>
          {deleted ? t('conflictReinsert') : t('conflictOverwrite')}
        </button>
        <button type="button" className="btn" onClick={() => ctx.actions.resolveConflict('theirs')}>
          {deleted || mine === undefined ? t('conflictDiscard') : t('conflictKeepTheirs')}
        </button>
        {!deleted && hit.block_id === editingId && (
          <button type="button" className="btn btn-quiet" onClick={() => ctx.actions.resolveConflict('continue')}>{t('conflictContinue')}</button>
        )}
      </div>
    </div>
  )
}

/** The editor of a block plus what happened to the block meanwhile. */
function EditState({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const ed = ctx.editing as EditingState
  const conflict = ctx.conflict?.conflicts.some((c) => c.block_id === block.id) ? ctx.conflict : null
  return (
    <>
      {!conflict && block.rev > ed.base && (
        <div className="updated-note" role="status">{t('updatedWhileEditing', { name: actorLabel(block.updated_by) })}</div>
      )}
      <EditBody block={block} ctx={ctx} />
      {conflict && <ConflictBox conflict={conflict} ctx={ctx} />}
    </>
  )
}

export function BlockView({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const editing = ctx.editing?.id === block.id
  const human = HUMAN_EDITABLE.has(block.type)
  const byAgent = block.updated_by?.kind === 'agent'
  const others = editorsOf(ctx.state, block.id)
  const pending = block.id.startsWith('tmp:')

  const classes = ['block', `t-${block.type}`]
  if (byAgent) classes.push('by-agent')
  if (editing) classes.push('editing')
  if (ctx.recent.has(block.id)) classes.push('recent')
  if (others.length) classes.push('being-edited')
  if (pending) classes.push('pending')

  // Tables edit per cell; dividers, child papers and rich blocks have no
  // text editor.
  const textEditable = ctx.canEdit && human && !['table', 'divider', 'paper'].includes(block.type)

  return (
    <div
      className={classes.join(' ')}
      data-block-id={block.id}
      onClick={() => {
        if (!editing && textEditable) ctx.actions.startEdit(block)
      }}
    >
      {others.length > 0 && !editing && (
        <div className="presence-note" role="status">{t('beingEdited', { names: others.map((a) => actorLabel(a)).join(', ') })}</div>
      )}
      {editing ? <EditState block={block} ctx={ctx} /> : <Body block={block} ctx={ctx} />}
      {!human && <div className="ai-note">{t('aiOnlyBlock')}</div>}
      {ctx.canEdit && !editing && !pending && <Tools block={block} ctx={ctx} />}
    </div>
  )
}
