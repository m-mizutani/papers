import { useMemo, useState } from 'react'
import { chosenElements, ELEMENT_COLORS } from '../api/databaseState'
import type { Element, Property, PropertyType, Value } from '../api/types'
import { useTranslation, type MsgKey } from '../i18n'
import { Popover, type Anchor } from '../ui/Popover'
import { isEnter } from '../utils/keyboard'

// Editors of property values and definitions, shared by the database table
// and the property panel of an item. Values are written as element ids;
// element texts and colours live only in the property definition.

export const TYPE_ICON: Record<PropertyType, string> = { text: 'T', date: '▦', select: '◉', multi_select: '☰' }
export const TYPE_LABEL: Record<PropertyType, MsgKey> = { text: 'propText', date: 'propDate', select: 'propSelect', multi_select: 'propMultiSelect' }
const COLOR_LABEL: Record<(typeof ELEMENT_COLORS)[number], MsgKey> = {
  gray: 'colorGray', brown: 'colorBrown', orange: 'colorOrange', yellow: 'colorYellow', green: 'colorGreen',
  blue: 'colorBlue', purple: 'colorPurple', pink: 'colorPink', red: 'colorRed',
}

export function Chip({ el, onRemove }: { el: Element; onRemove?: () => void }) {
  const { t } = useTranslation()
  return (
    <span className={`chip c-${el.color}`}>
      {el.value}
      {onRemove && <button type="button" className="x" aria-label={t('unchoose')} onClick={(e) => { e.stopPropagation(); onRemove() }}>✕</button>}
    </span>
  )
}

/** How a value reads in a cell or the panel. */
export function ValueView({ prop, value }: { prop: Property; value: Value | undefined }) {
  if (value === undefined) return null
  if (prop.type === 'select' || prop.type === 'multi_select') {
    return <span className="chips">{chosenElements(prop, value).map((e) => <Chip key={e.id} el={e} />)}</span>
  }
  return <span className="vt">{value as string}</span>
}

/** What the element picker asks of the page that writes. */
export interface ElementActions {
  /** Creates an element and chooses it, in one batch. */
  create: (value: string) => void
  update: (el: Element, change: { value?: string; color?: string }) => void
  remove: (el: Element) => void
  move: (el: Element, after: string | undefined) => void
}

/** Chooses (or creates) elements of a select or multi_select property. */
export function ElementPicker({ prop, value, anchor, onChange, actions, onClose }: {
  prop: Property
  value: Value | undefined
  anchor: Anchor
  onChange: (v: Value | null) => void
  actions: ElementActions
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [q, setQ] = useState('')
  const [hi, setHi] = useState(0)
  const [edit, setEdit] = useState<{ el: Element; anchor: HTMLElement } | null>(null)
  const [drag, setDrag] = useState<string | null>(null)
  const [over, setOver] = useState<string | null>(null)
  const multi = prop.type === 'multi_select'
  const chosen = chosenElements(prop, value)
  const elements = prop.elements ?? []
  const shown = elements.filter((e) => e.value.toLowerCase().includes(q.trim().toLowerCase()))
  const exact = elements.some((e) => e.value === q.trim())
  const canCreate = q.trim() !== '' && !exact
  const count = shown.length + (canCreate ? 1 : 0)

  const choose = (el: Element) => {
    if (multi) {
      const ids = chosen.map((e) => e.id)
      const next = ids.includes(el.id) ? ids.filter((id) => id !== el.id) : [...ids, el.id]
      onChange(next.length ? next : null)
      setQ('')
    } else {
      onChange(chosen[0]?.id === el.id ? null : el.id)
      onClose()
    }
  }
  const create = () => {
    if (!canCreate) return
    actions.create(q.trim())
    setQ('')
    if (!multi) onClose()
  }
  const unchoose = (el: Element) => {
    const next = chosen.filter((e) => e.id !== el.id).map((e) => e.id)
    onChange(multi ? (next.length ? next : null) : null)
  }

  return (
    <Popover anchor={anchor} onClose={() => { if (!edit) onClose() }} className="dbpop" label={t('chooseOrCreate')}>
      <div className="tagin">
        {chosen.map((e) => <Chip key={e.id} el={e} onRemove={() => unchoose(e)} />)}
        <input autoFocus value={q} aria-label={t('chooseOrCreate')}
          onChange={(e) => { setQ(e.target.value); setHi(0) }}
          onKeyDown={(e) => {
            if (e.key === 'ArrowDown') { e.preventDefault(); setHi((h) => Math.min(h + 1, count - 1)) }
            else if (e.key === 'ArrowUp') { e.preventDefault(); setHi((h) => Math.max(h - 1, 0)) }
            else if (e.key === 'Backspace' && q === '') { const last = chosen[chosen.length - 1]; if (last) unchoose(last) }
            else if (isEnter(e)) {
              e.preventDefault()
              const el = shown[hi]
              if (el) choose(el)
              else create()
            }
          }} />
      </div>
      <div className="hd">{t('chooseOrCreate')}</div>
      <div role="listbox" aria-multiselectable={multi}>
        {shown.map((el, i) => (
          <div key={el.id} role="option" aria-selected={chosen.some((c) => c.id === el.id)}
            className={`opt${i === hi ? ' hi' : ''}${over === el.id && drag && drag !== el.id ? ' drop' : ''}`}
            draggable onDragStart={() => setDrag(el.id)} onDragEnd={() => { setDrag(null); setOver(null) }}
            onDragOver={(e) => { e.preventDefault(); setOver(el.id) }}
            onDrop={(e) => {
              e.preventDefault()
              const moved = elements.find((x) => x.id === drag)
              if (moved && moved.id !== el.id) {
                // Dropped on an element: the moved one goes just before it.
                const rest = elements.filter((x) => x.id !== moved.id)
                actions.move(moved, rest[rest.findIndex((x) => x.id === el.id) - 1]?.id)
              }
              setDrag(null)
              setOver(null)
            }}
            onMouseMove={() => setHi(i)} onClick={() => choose(el)}>
            <span className="grip" aria-hidden>⋮⋮</span>
            <Chip el={el} />
            <button type="button" className="dots" aria-label={t('more')} onClick={(e) => { e.stopPropagation(); setEdit({ el, anchor: e.currentTarget }) }}>⋯</button>
          </div>
        ))}
        {canCreate && (
          <button type="button" className={`opt${hi === shown.length ? ' hi' : ''}`} onMouseMove={() => setHi(shown.length)} onClick={create}>
            {t('createElement')} <Chip el={{ id: '', value: q.trim(), color: 'gray' }} /><span className="k">Enter</span>
          </button>
        )}
      </div>
      {edit && (
        <ElementEditor el={edit.el} anchor={edit.anchor} taken={elements.filter((e) => e.id !== edit.el.id).map((e) => e.value)}
          onChange={(change) => actions.update(edit.el, change)}
          onDelete={() => { actions.remove(edit.el); setEdit(null) }}
          onClose={() => setEdit(null)} />
      )}
    </Popover>
  )
}

/** Renames, recolours or deletes one element. */
function ElementEditor({ el, anchor, taken, onChange, onDelete, onClose }: {
  el: Element
  anchor: Anchor
  taken: string[]
  onChange: (change: { value?: string; color?: string }) => void
  onDelete: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [value, setValue] = useState(el.value)
  const [color, setColor] = useState(el.color)
  const dup = taken.includes(value.trim())
  const commit = () => {
    const v = value.trim()
    if (v && v !== el.value && !dup) onChange({ value: v })
  }
  return (
    <Popover anchor={anchor} onClose={() => { commit(); onClose() }} className="dbpop" label={el.value}>
      <input className="inp" autoFocus value={value} aria-label={t('colName')}
        onChange={(e) => setValue(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); commit() } }} />
      {dup && <p className="err">{t('duplicateElementValue')}</p>}
      <div className="hd">{t('color')}</div>
      {ELEMENT_COLORS.map((c) => (
        <button key={c} type="button" className={`opt${c === color ? ' on' : ''}`} onClick={() => { setColor(c); onChange({ color: c }) }}>
          <span className={`swatch chip c-${c}`} aria-hidden />{t(COLOR_LABEL[c])}{c === color && <span className="k">✓</span>}
        </button>
      ))}
      <div className="msep" />
      <button type="button" className="opt" style={{ color: 'var(--danger)' }} onClick={onDelete}>🗑 {t('deleteElement')}</button>
    </Popover>
  )
}

const pad = (n: number) => String(n).padStart(2, '0')
const ymd = (y: number, m: number, d: number) => `${y}-${pad(m + 1)}-${pad(d)}`

/** A date: a field for YYYY-MM-DD and a month to pick from. */
export function DateEditor({ value, anchor, onChange, onClose }: {
  value: string | undefined
  anchor: Anchor
  onChange: (v: string | null) => void
  onClose: () => void
}) {
  const { t, lang } = useTranslation()
  const start = value && /^\d{4}-\d{2}-\d{2}$/.test(value) ? new Date(`${value}T00:00:00`) : new Date()
  const [month, setMonth] = useState({ y: start.getFullYear(), m: start.getMonth() })
  const [text, setText] = useState(value ?? '')
  const days = useMemo(() => {
    const first = new Date(month.y, month.m, 1).getDay()
    const n = new Date(month.y, month.m + 1, 0).getDate()
    return [...Array(first).fill(0), ...Array.from({ length: n }, (_, i) => i + 1)] as number[]
  }, [month])
  const weekdays = lang === 'ja' ? ['日', '月', '火', '水', '木', '金', '土'] : ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa']
  const title = lang === 'ja' ? `${month.y}年${month.m + 1}月` : new Date(month.y, month.m, 1).toLocaleDateString('en', { year: 'numeric', month: 'long' })
  const shift = (d: number) => setMonth(({ y, m }) => ({ y: m + d < 0 ? y - 1 : m + d > 11 ? y + 1 : y, m: (m + d + 12) % 12 }))
  const submitText = () => {
    const v = text.trim()
    if (v === '') onChange(null)
    else if (/^\d{4}-\d{2}-\d{2}$/.test(v)) onChange(v)
  }
  return (
    <Popover anchor={anchor} onClose={onClose} className="dbpop" label={t('propDate')}>
      <input className="inp" autoFocus value={text} placeholder="YYYY-MM-DD" aria-label={t('propDate')}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); submitText(); onClose() } }} />
      <div className="calhead">
        <button type="button" aria-label="‹" onClick={() => shift(-1)}>‹</button>
        <span>{title}</span>
        <button type="button" aria-label="›" onClick={() => shift(1)}>›</button>
      </div>
      <div className="cal">
        {weekdays.map((w) => <span key={w} className="h">{w}</span>)}
        {days.map((d, i) => (d === 0 ? <span key={`b${i}`} /> : (
          <button key={d} type="button" className={ymd(month.y, month.m, d) === value ? 'on' : ''}
            onClick={() => { onChange(ymd(month.y, month.m, d)); onClose() }}>{d}</button>
        )))}
      </div>
      <button type="button" className="opt" style={{ color: 'var(--fg-3)' }} onClick={() => { onChange(null); onClose() }}>{t('clearValue')}</button>
    </Popover>
  )
}

/** Adds a property: a name and one of the four types. */
export function AddPropertyPopover({ anchor, names, onAdd, onClose }: {
  anchor: Anchor
  names: string[]
  onAdd: (name: string, type: PropertyType) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState('')
  const [type, setType] = useState<PropertyType>('text')
  const dup = names.includes(name.trim())
  const ok = name.trim() !== '' && !dup
  const add = () => {
    if (!ok) return
    onAdd(name.trim(), type)
    onClose()
  }
  return (
    <Popover anchor={anchor} onClose={onClose} className="dbpop" label={t('addProperty')}>
      <input className="inp" autoFocus value={name} placeholder={t('propertyName')} aria-label={t('propertyName')}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); add() } }} />
      {dup && <p className="err">{t('duplicatePropertyName')}</p>}
      <div className="hd">{t('propertyType')}</div>
      <div className="typegrid" role="radiogroup" aria-label={t('propertyType')}>
        {(['text', 'date', 'select', 'multi_select'] as PropertyType[]).map((ty) => (
          <button key={ty} type="button" role="radio" aria-checked={ty === type} className={ty === type ? 'on' : ''} onClick={() => setType(ty)}>
            <span aria-hidden>{TYPE_ICON[ty]}</span>{t(TYPE_LABEL[ty])}
          </button>
        ))}
      </div>
      <div className="acts">
        <button type="button" className="btn ghost sm" onClick={onClose}>{t('cancel')}</button>
        <button type="button" className="btn primary sm" disabled={!ok} onClick={add}>{t('addAction')}</button>
      </div>
    </Popover>
  )
}

/** A column header's menu: rename in place, move, delete. */
export function ColumnMenu({ prop, anchor, names, canLeft, canRight, onRename, onMove, onDelete, onClose }: {
  prop: Property
  anchor: Anchor
  names: string[]
  canLeft: boolean
  canRight: boolean
  onRename: (name: string) => void
  onMove: (dir: -1 | 1) => void
  onDelete: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [name, setName] = useState(prop.name)
  const dup = name.trim() !== prop.name && names.includes(name.trim())
  const commit = () => {
    const n = name.trim()
    if (n && n !== prop.name && !dup) onRename(n)
  }
  return (
    <Popover anchor={anchor} onClose={() => { commit(); onClose() }} className="dbpop" label={prop.name}>
      <input className="inp" autoFocus value={name} aria-label={t('propertyName')}
        onChange={(e) => setName(e.target.value)} onBlur={commit}
        onKeyDown={(e) => { if (isEnter(e)) { e.preventDefault(); commit(); onClose() } }} />
      {dup && <p className="err">{t('duplicatePropertyName')}</p>}
      <div className="hd">{t(TYPE_LABEL[prop.type])}</div>
      <button type="button" className="opt" disabled={!canLeft} onClick={() => { onMove(-1); onClose() }}>← {t('moveLeft')}</button>
      <button type="button" className="opt" disabled={!canRight} onClick={() => { onMove(1); onClose() }}>→ {t('moveRight')}</button>
      <div className="msep" />
      <button type="button" className="opt" style={{ color: 'var(--danger)' }} onClick={() => { onClose(); onDelete() }}>🗑 {t('deleteProperty')}</button>
    </Popover>
  )
}
