import { useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { actorShort, actorColor } from '../utils/actors'
import { cellsOf, editorsOf, sortedLines } from '../api/paperState'
import type { Block, Op, TableLine } from '../api/types'
import { useTranslation } from '../i18n'
import { Menu, type MenuItem } from '../ui/Menu'
import { renderMarkup } from '../utils/inline'
import type { BlockContext } from './BlockView'
import { RichEditor } from './RichEditor'

// A table: a strip on the right adds a column, one below adds a row, Tab
// past the last cell adds a row; the focused cell has a menu for inserting,
// moving and deleting; row and column grips drag to reorder (spec: R4).

type Kind = 'row' | 'col'

export function TableBlock({ block, ctx }: { block: Block; ctx: BlockContext }) {
  const { t } = useTranslation()
  const rows = sortedLines(block.props.rows)
  const cols = sortedLines(block.props.cols)
  const cells = cellsOf(ctx.state.blocks.values(), block.id)
  const [menu, setMenu] = useState<{ anchor: HTMLElement; row: string; col: string } | null>(null)
  const [hoverCol, setHoverCol] = useState<number>(-1)
  const tableRef = useRef<HTMLTableElement>(null)
  const editingCell = ctx.editing ? ctx.state.blocks.get(ctx.editing.id) : undefined
  const focusRow = editingCell?.parent_id === block.id ? editingCell.props.row_id : undefined
  const focusCol = editingCell?.parent_id === block.id ? editingCell.props.col_id : undefined

  const fresh = () => ctx.actions.current()?.blocks.get(block.id) ?? block
  const cellAt = (row: string | undefined, col: string | undefined) => {
    const s = ctx.actions.current()
    if (!s || !row || !col) return undefined
    return cellsOf(s.blocks.values(), block.id).get(`${row}:${col}`)
  }
  const editCell = (row: string | undefined, col: string | undefined) => {
    const c = cellAt(row, col)
    if (c) ctx.actions.startEdit(c, 'end')
  }

  /** Runs a row / column op, then starts editing the cell named by pick in the new layout. */
  const runLine = async (op: Op, pick?: (rows: TableLine[], cols: TableLine[], before: { rows: Set<string>; cols: Set<string> }) => [string | undefined, string | undefined]) => {
    const before = { rows: new Set(sortedLines(fresh().props.rows).map((r) => r.id)), cols: new Set(sortedLines(fresh().props.cols).map((c) => c.id)) }
    ctx.actions.finishEdit()
    if (!(await ctx.actions.run([op])) || !pick) return
    const tb = fresh()
    const [r, c] = pick(sortedLines(tb.props.rows), sortedLines(tb.props.cols), before)
    editCell(r, c)
  }
  const newRow = (rs: TableLine[], b: { rows: Set<string> }) => rs.find((r) => !b.rows.has(r.id))?.id
  const newCol = (cs: TableLine[], b: { cols: Set<string> }) => cs.find((c) => !b.cols.has(c.id))?.id

  const addRow = (after?: string, col?: string) => void runLine({ op: 'insert_row', block_id: block.id, after }, (rs, cs, b) => [newRow(rs, b), col ?? cs[0]?.id])
  const addCol = (after?: string, row?: string) => void runLine({ op: 'insert_col', block_id: block.id, after }, (rs, cs, b) => [row ?? rs[0]?.id, newCol(cs, b)])
  const moveLine = (kind: Kind, id: string, after: string, keep: [string | undefined, string | undefined]) =>
    void runLine({ op: kind === 'row' ? 'move_row' : 'move_col', block_id: block.id, line_id: id, after }, () => keep)

  const cellMenu = (anchor: HTMLElement, row: string, col: string) => setMenu({ anchor, row, col })
  const menuItems = (row: string, col: string): MenuItem[] => {
    const ri = rows.findIndex((r) => r.id === row)
    const ci = cols.findIndex((c) => c.id === col)
    const prevOf = (lines: TableLine[], i: number) => (i <= 0 ? '' : lines[i - 1]?.id ?? '')
    return [
      { label: t('rowAbove'), icon: '↑', onSelect: () => addRow(prevOf(rows, ri), col) },
      { label: t('rowBelow'), icon: '↓', onSelect: () => addRow(row, col) },
      { label: t('colLeft'), icon: '←', onSelect: () => addCol(prevOf(cols, ci), row) },
      { label: t('colRight'), icon: '→', onSelect: () => addCol(col, row) },
      '-',
      { label: t('rowUp'), icon: '⇡', disabled: ri <= 0, onSelect: () => moveLine('row', row, prevOf(rows, ri - 1), [row, col]) },
      { label: t('rowDown'), icon: '⇣', disabled: ri >= rows.length - 1, onSelect: () => moveLine('row', row, rows[ri + 1]?.id ?? '', [row, col]) },
      { label: t('colLeftMove'), icon: '⇠', disabled: ci <= 0, onSelect: () => moveLine('col', col, prevOf(cols, ci - 1), [row, col]) },
      { label: t('colRightMove'), icon: '⇢', disabled: ci >= cols.length - 1, onSelect: () => moveLine('col', col, cols[ci + 1]?.id ?? '', [row, col]) },
      '-',
      { label: t('headerRow'), icon: block.props.header_row ? '✓' : '', onSelect: () => void ctx.actions.run([{ op: 'update', block_id: block.id, base_rev: fresh().rev, props: { ...fresh().props, header_row: !fresh().props.header_row } }]) },
      '-',
      { label: t('deleteRow'), icon: '🗑', danger: true, disabled: rows.length < 2, onSelect: () => ctx.actions.deleteRow(block, row) },
      { label: t('deleteColumn'), icon: '🗑', danger: true, disabled: cols.length < 2, onSelect: () => ctx.actions.deleteCol(block, col) },
    ]
  }

  // Dragging a row or column grip; a click without moving opens the menu.
  const drag = (e: ReactPointerEvent, kind: Kind, id: string) => {
    if (e.button !== 0) return
    e.preventDefault()
    const tb = tableRef.current
    if (!tb) return
    const anchor = e.currentTarget as HTMLElement
    const sx = e.clientX
    const sy = e.clientY
    const lines = kind === 'row' ? rows : cols
    const from = lines.findIndex((l) => l.id === id)
    let moved = false
    let target = -1
    const line = document.createElement('div')
    line.className = 'tdrop'
    line.hidden = true
    document.body.append(line)
    const sources = () => (kind === 'row' ? [tb.rows[from]] : [...tb.rows].map((r) => r.cells[from]))
    const mv = (ev: PointerEvent) => {
      if (!moved && Math.hypot(ev.clientX - sx, ev.clientY - sy) < 4) return
      if (!moved) {
        moved = true
        sources().forEach((x) => x?.classList.add('dragsrc'))
      }
      const box = tb.getBoundingClientRect()
      const parts = kind === 'row' ? [...tb.rows] : [...(tb.rows[0]?.cells ?? [])]
      let idx = parts.length
      for (let i = 0; i < parts.length; i++) {
        const r = parts[i]?.getBoundingClientRect()
        if (r && (kind === 'row' ? ev.clientY < r.top + r.height / 2 : ev.clientX < r.left + r.width / 2)) {
          idx = i
          break
        }
      }
      target = idx
      const last = parts[parts.length - 1]?.getBoundingClientRect()
      const at = parts[idx]?.getBoundingClientRect()
      line.hidden = idx === from || idx === from + 1
      if (kind === 'row') Object.assign(line.style, { left: `${box.left}px`, width: `${box.width}px`, height: '3px', top: `${(at ? at.top : last?.bottom ?? box.bottom) - 1.5}px` })
      else Object.assign(line.style, { top: `${box.top}px`, height: `${box.height}px`, width: '3px', left: `${(at ? at.left : last?.right ?? box.right) - 1.5}px` })
    }
    const up = () => {
      document.removeEventListener('pointermove', mv)
      document.removeEventListener('pointerup', up)
      line.remove()
      sources().forEach((x) => x?.classList.remove('dragsrc'))
      if (!moved) {
        cellMenu(anchor, kind === 'row' ? id : rows[0]?.id ?? '', kind === 'col' ? id : cols[0]?.id ?? '')
        return
      }
      if (target < 0 || target === from || target === from + 1) return
      const rest = lines.filter((l) => l.id !== id)
      const to = target > from ? target - 1 : target
      moveLine(kind, id, to === 0 ? '' : rest[to - 1]?.id ?? '', [undefined, undefined])
    }
    document.addEventListener('pointermove', mv)
    document.addEventListener('pointerup', up)
  }

  const header = !!block.props.header_row
  return (
    <div className="tbx" onMouseLeave={() => setHoverCol(-1)}>
      <div className="tblw">
        <table className="tb" ref={tableRef}>
          <tbody>
            {rows.map((row, ri) => (
              <tr key={row.id} className={[ri === 0 && header ? 'hdr' : '', focusRow === row.id ? 'focus-row' : ''].filter(Boolean).join(' ') || undefined}>
                {cols.map((col, ci) => {
                  const cell = cells.get(`${row.id}:${col.id}`)
                  const editing = !!cell && ctx.editing?.id === cell.id
                  const others = cell ? editorsOf(ctx.state, cell.id) : []
                  const who = others[0]
                  const cls = [editing ? 'focus' : '', who ? 'pres' : ''].filter(Boolean).join(' ')
                  return (
                    <td key={col.id} className={cls || undefined} style={who ? { ['--pc' as string]: actorColor(who) } : undefined} onMouseEnter={() => setHoverCol(ci)}>
                      {editing && cell && ctx.editing ? (
                        <div className="cell">
                          <RichEditor
                            value={ctx.editing.draft}
                            caret={ctx.editing.caret}
                            label={t('cellMenu')}
                            onChange={ctx.actions.setDraft}
                            onBlur={() => ctx.actions.finishEdit(cell.id)}
                            onEditor={ctx.actions.onEditor}
                            keys={{
                              onEscape: () => ctx.actions.finishEdit(cell.id),
                              onEnter: () => { ctx.actions.finishEdit(cell.id); editCell(rows[ri + 1]?.id, col.id) },
                              onTab: (shift) => {
                                const flat = rows.flatMap((r) => cols.map((c) => [r.id, c.id] as const))
                                const i = flat.findIndex(([r, c]) => r === row.id && c === col.id) + (shift ? -1 : 1)
                                ctx.actions.finishEdit(cell.id)
                                if (i >= flat.length) addRow(rows[rows.length - 1]?.id, cols[0]?.id)
                                else if (i >= 0) editCell(flat[i]?.[0], flat[i]?.[1])
                              },
                            }}
                          />
                        </div>
                      ) : (
                        <div className="cell" onMouseDown={(e) => {
                          if (!ctx.canEdit || !cell || e.button !== 0 || (e.target as HTMLElement).closest('.mention')) return
                          e.preventDefault()
                          ctx.actions.startEdit(cell, { x: e.clientX, y: e.clientY })
                        }}>{cell ? renderMarkup(cell.content) : null}</div>
                      )}
                      {who && <span className="ptag">{t('editing', { name: actorShort(who) })}</span>}
                      {ctx.canEdit && ci === 0 && (
                        <button type="button" className="rgrip" aria-label={t('dragRow')} title={t('dragRow')} onPointerDown={(e) => drag(e, 'row', row.id)}><span /></button>
                      )}
                      {ctx.canEdit && ri === 0 && (
                        <button type="button" className={`cgrip${hoverCol === ci || focusCol === col.id ? ' show' : ''}`} aria-label={t('dragColumn')} title={t('dragColumn')} onPointerDown={(e) => drag(e, 'col', col.id)}><span /></button>
                      )}
                      {ctx.canEdit && editing && (
                        <button type="button" className="cellm" aria-label={t('cellMenu')} title={t('cellMenu')} onMouseDown={(e) => e.preventDefault()}
                          onClick={(e) => cellMenu(e.currentTarget, row.id, col.id)}>▾</button>
                      )}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {ctx.canEdit && (
        <>
          <button type="button" className="tadd tadd-col" aria-label={t('addColumn')} title={t('addColumn')} onClick={() => addCol(cols[cols.length - 1]?.id)}>+</button>
          <button type="button" className="tadd tadd-row" aria-label={t('addRow')} title={`${t('addRow')} (Tab)`} onClick={() => addRow(rows[rows.length - 1]?.id)}>+</button>
        </>
      )}
      {menu && <Menu anchor={menu.anchor} items={menuItems(menu.row, menu.col)} onClose={() => setMenu(null)} label={t('cellMenu')} />}
    </div>
  )
}
