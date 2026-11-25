import { useState } from 'react'
import { ApiError, api } from '../api/client'
import { asPeople, nextColor, shownValue } from '../api/databaseState'
import type { BatchResult, Op, Property, PropertyType, PropertyValue, Value } from '../api/types'
import { useTranslation } from '../i18n'
import type { Anchor } from '../ui/Popover'
import { useToast } from '../ui/Toast'
import { isEnter } from '../utils/keyboard'
import { PersonValuePicker } from './UserMention'
import { AddPropertyPopover, DateEditor, ElementPicker, TYPE_ICON, ValueView, type ElementActions } from './PropertyEditors'
import { NO_AUTOFILL } from '../utils/autofill'

// The property values of an item, under its title (spec 2.9 I1, I2). The
// writes go to the database's batch; the definitions come with the item's
// view and with each answer.

export function PropertyPanel({ ws, item, database, properties, values, readOnly, onProperties, onValues }: {
  ws: string
  item: string
  database: string
  properties: Property[]
  values: Map<string, PropertyValue>
  readOnly: boolean
  onProperties: (props: Property[]) => void
  /** Values written (put) and taken away (cleared), from an answer. */
  onValues: (put: PropertyValue[], cleared: string[]) => void
}) {
  const { t } = useTranslation()
  const toast = useToast()
  const [open, setOpen] = useState<{ prop: string; anchor: HTMLElement } | null>(null)
  const [text, setText] = useState<{ prop: string; draft: string } | null>(null)
  const [adding, setAdding] = useState<Anchor | null>(null)

  const send = async (ops: Op[], cleared: string[] = []): Promise<BatchResult | null> => {
    try {
      const res = await api.ops(ws, database, ops)
      if (res.paper?.properties) onProperties(res.paper.properties)
      onValues((res.values ?? []).filter((v) => v.paper_id === item), cleared)
      return res
    } catch (e) {
      if (e instanceof ApiError && e.status === 400) {
        // The definitions may have changed since the page was opened (only
        // the database's feed carries them): read them again.
        toast(t('dbWriteFailed', { message: e.message }))
        void api.getPaper(ws, item).then((v) => { if (v.database) onProperties(v.database.properties) }, () => undefined)
      } else {
        toast(t('sendFailed'))
      }
      return null
    }
  }

  const setValue = (prop: Property, value: Value | null) => {
    const cur = shownValue(prop, values.get(prop.id))
    if (JSON.stringify(cur ?? null) === JSON.stringify(value)) return
    void send([{ op: 'set_values', item, set: { [prop.id]: value } }], value === null ? [prop.id] : [])
  }

  const actions = (p: Property): ElementActions => ({
    create: (value) => {
      const cur = shownValue(p, values.get(p.id))
      const chosen = p.type === 'multi_select' ? [...((cur as string[] | undefined) ?? []), 'ref:new'] : 'ref:new'
      void send([
        { op: 'add_element', property: p.id, value, color: nextColor(p), ref: 'new' },
        { op: 'set_values', item, set: { [p.id]: chosen } },
      ])
    },
    update: (el, change) => void send([{ op: 'update_element', property: p.id, element: el.id, value: change.value, color: change.color }]),
    remove: (el) => void send([{ op: 'delete_element', property: p.id, element: el.id }]).then((r) => { if (r) toast(t('elementDeleted', { value: el.value })) }),
    move: (el, after) => void send([{ op: 'move_element', property: p.id, element: el.id, after: after ?? '' }]),
  })

  const openProp = properties.find((p) => p.id === open?.prop)
  return (
    <div className="props" aria-label={t('properties')}>
      {properties.map((p) => {
        const v = shownValue(p, values.get(p.id))
        return (
          <div key={p.id} style={{ display: 'contents' }}>
            <div className="pn"><span className="ti" aria-hidden>{TYPE_ICON[p.type]}</span><span className="nm">{p.name}</span></div>
            {text?.prop === p.id ? (
              <input {...NO_AUTOFILL} className="pv" autoFocus value={text.draft} aria-label={p.name}
                onChange={(e) => setText({ prop: p.id, draft: e.target.value.replace(/\n/g, '') })}
                onBlur={() => { setValue(p, text.draft.trim() === '' ? null : text.draft); setText(null) }}
                onKeyDown={(e) => {
                  if (isEnter(e)) { e.preventDefault(); e.currentTarget.blur() }
                  if (e.key === 'Escape') setText(null)
                }} />
            ) : readOnly ? (
              <div className={`pv${v === undefined ? ' nil' : ''}`}>{v === undefined ? t('emptyValue') : <ValueView prop={p} value={v} />}</div>
            ) : (
              <button type="button" className={`pv${v === undefined ? ' nil' : ''}`} aria-label={p.name}
                onClick={(e) => (p.type === 'text' ? setText({ prop: p.id, draft: (v as string | undefined) ?? '' }) : setOpen({ prop: p.id, anchor: e.currentTarget }))}>
                {v === undefined ? t('emptyValue') : <ValueView prop={p} value={v} />}
              </button>
            )}
          </div>
        )
      })}
      {!readOnly && <button type="button" className="addp" onClick={(e) => setAdding(e.currentTarget)}>＋ {t('addProperty')}</button>}
      {open && openProp && (openProp.type === 'select' || openProp.type === 'multi_select') && (
        <ElementPicker prop={openProp} value={shownValue(openProp, values.get(openProp.id))} anchor={open.anchor}
          onChange={(v) => setValue(openProp, v)} actions={actions(openProp)} onClose={() => setOpen(null)} />
      )}
      {open && openProp?.type === 'person' && (
        <PersonValuePicker ws={ws} value={asPeople(shownValue(openProp, values.get(openProp.id)))} anchor={open.anchor} label={openProp.name}
          onChange={(v) => setValue(openProp, v)} onClose={() => setOpen(null)} />
      )}
      {open && openProp?.type === 'date' && (
        <DateEditor value={shownValue(openProp, values.get(openProp.id)) as string | undefined} anchor={open.anchor}
          onChange={(v) => setValue(openProp, v)} onClose={() => setOpen(null)} />
      )}
      {adding && (
        <AddPropertyPopover anchor={adding} names={properties.map((p) => p.name)} onClose={() => setAdding(null)}
          onAdd={(name: string, type: PropertyType) => void send([{ op: 'add_property', name, property_type: type }])} />
      )}
    </div>
  )
}
