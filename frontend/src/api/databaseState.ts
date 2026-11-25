import type { Element, ItemRow, Paper, PaperEvent, PersonRef, Property, PropertyValue, Value } from './types'

// The state of an open database: its definitions and the items read so far,
// in the order they were read (the database's order, or a sort). Events
// carry resulting states and may come twice or out of order: a value or an
// item applies only when it is newer than what is held.

export interface DbRow {
  item: Paper
  values: Map<string, PropertyValue>
  /** When each value was last cleared (by property), so an older put that arrives late does not bring it back. */
  cleared?: Map<string, string>
}

export interface DatabaseState {
  db: Paper
  rows: Map<string, DbRow>
  /** Item ids in the order they show. */
  order: string[]
}

export function initDatabase(db: Paper, rows: ItemRow[] = []): DatabaseState {
  return appendRows({ db, rows: new Map(), order: [] }, rows)
}

export function appendRows(s: DatabaseState, rows: ItemRow[]): DatabaseState {
  const map = new Map(s.rows)
  const order = [...s.order]
  for (const r of rows) {
    if (!map.has(r.item.id)) order.push(r.item.id)
    map.set(r.item.id, { item: r.item, values: new Map((r.values ?? []).map((v) => [v.property_id, v])) })
  }
  return { ...s, rows: map, order }
}

const newer = (a: string, b: string) => Date.parse(a) >= Date.parse(b)
const after = (a: string, b: string) => Date.parse(a) > Date.parse(b)

/** Applies one event of the database's feed. */
export function applyDatabaseEvent(s: DatabaseState, ev: PaperEvent): DatabaseState {
  switch (ev.type) {
    case 'paper_updated':
      if (ev.paper && ev.paper.id === s.db.id && newer(ev.paper.updated_at, s.db.updated_at)) return { ...s, db: ev.paper }
      return s
    case 'item_put': {
      const item = ev.paper
      if (!item) return s
      const row = s.rows.get(item.id)
      if (row && !newer(item.updated_at, row.item.updated_at)) return s
      const rows = new Map(s.rows)
      rows.set(item.id, { item, values: row?.values ?? new Map() })
      return { ...s, rows }
    }
    case 'item_deleted': {
      if (!ev.item_id || !s.rows.has(ev.item_id)) return s
      const rows = new Map(s.rows)
      rows.delete(ev.item_id)
      return { ...s, rows, order: s.order.filter((id) => id !== ev.item_id) }
    }
    case 'property_value_put': {
      const v = ev.property_value
      if (!v) return s
      return putValue(s, v)
    }
    case 'property_value_deleted': {
      if (!ev.item_id || !ev.property_id) return s
      const row = s.rows.get(ev.item_id)
      if (!row) return s
      const cur = row.values.get(ev.property_id)
      // A clearing older than the value held (a repeat, or out of order) changes nothing.
      if (cur && ev.at && !newer(ev.at, cur.updated_at)) return s
      const was = row.cleared?.get(ev.property_id)
      if (!cur && (!ev.at || (was && newer(was, ev.at)))) return s
      const values = new Map(row.values)
      values.delete(ev.property_id)
      const cleared = new Map(row.cleared)
      if (ev.at) cleared.set(ev.property_id, ev.at)
      const rows = new Map(s.rows)
      rows.set(ev.item_id, { ...row, values, cleared })
      return { ...s, rows }
    }
  }
  return s
}

/** Puts a value (an event's or an answer's) unless an equal or newer one is held, or it was cleared since. */
export function putValue(s: DatabaseState, v: PropertyValue): DatabaseState {
  const row = s.rows.get(v.paper_id)
  if (!row) return s
  const cur = row.values.get(v.property_id)
  if (cur && !newer(v.updated_at, cur.updated_at)) return s
  const was = row.cleared?.get(v.property_id)
  if (was && !after(v.updated_at, was)) return s
  const values = new Map(row.values)
  values.set(v.property_id, v)
  const rows = new Map(s.rows)
  rows.set(v.paper_id, { ...row, values })
  return { ...s, rows }
}

/** Takes a value away (my own clearing, answered). */
export function clearValue(s: DatabaseState, item: string, property: string): DatabaseState {
  return applyDatabaseEvent(s, { type: 'property_value_deleted', workspace_id: s.db.workspace_id, paper_id: s.db.id, at: '', item_id: item, property_id: property })
}

/** Adds rows of new items (my inserts) at their place in the database's order. */
export function placeItems(s: DatabaseState, items: Paper[], dbOrder: string[]): DatabaseState {
  const rows = new Map(s.rows)
  for (const it of items) if (!rows.has(it.id)) rows.set(it.id, { item: it, values: new Map() })
  const shown = new Set(s.order)
  items.forEach((it) => shown.add(it.id))
  return { ...s, rows, order: dbOrder.filter((id) => shown.has(id)) }
}

/** The rows that show: in order, not trashed or archived. */
export function visibleRows(s: DatabaseState): DbRow[] {
  return s.order.map((id) => s.rows.get(id)).filter((r): r is DbRow => !!r && !r.item.deleted_at && !r.item.archived_at)
}

/**
 * What a stored value shows under the current definitions: nothing when
 * its property or (all of) its elements are gone.
 */
export function shownValue(prop: Property, v: PropertyValue | undefined): Value | undefined {
  if (!v) return undefined
  if (prop.type === 'select') {
    return typeof v.value === 'string' && (prop.elements ?? []).some((e) => e.id === v.value) ? v.value : undefined
  }
  if (prop.type === 'multi_select') {
    if (!Array.isArray(v.value)) return undefined
    const keep = (v.value as unknown[]).filter((id): id is string => typeof id === 'string' && (prop.elements ?? []).some((e) => e.id === id))
    return keep.length > 0 ? keep : undefined
  }
  if (prop.type === 'person') {
    const people = asPeople(v.value)
    return people.length > 0 ? people : undefined
  }
  return typeof v.value === 'string' ? v.value : undefined
}

/** The people of a person value; anything else reads as none. */
export function asPeople(v: Value | undefined): PersonRef[] {
  if (!Array.isArray(v)) return []
  return (v as unknown[]).filter((x): x is PersonRef => typeof x === 'object' && x !== null && typeof (x as PersonRef).user_id === 'string' && typeof (x as PersonRef).name === 'string')
}

/** The elements a select or multi_select value chooses, in the property's order. */
export function chosenElements(prop: Property, v: Value | undefined): Element[] {
  if (v === undefined) return []
  const ids = new Set<unknown>(Array.isArray(v) ? v : [v])
  return (prop.elements ?? []).filter((e) => ids.has(e.id))
}

/** The next palette colour for a new element: the least used one, in palette order. */
export const ELEMENT_COLORS = ['gray', 'brown', 'orange', 'yellow', 'green', 'blue', 'purple', 'pink', 'red'] as const

export function nextColor(prop: Property): string {
  const used = new Map<string, number>(ELEMENT_COLORS.map((c) => [c, 0]))
  for (const e of prop.elements ?? []) used.set(e.color, (used.get(e.color) ?? 0) + 1)
  let best: string = ELEMENT_COLORS[0]
  for (const c of ELEMENT_COLORS) if ((used.get(c) ?? 0) < (used.get(best) ?? 0)) best = c
  return best
}

/** Moves an id within a list to just after another (undefined: the top). */
export function moveAfter(list: string[], id: string, after: string | undefined): string[] {
  const rest = list.filter((x) => x !== id)
  const i = after === undefined ? -1 : rest.indexOf(after)
  return [...rest.slice(0, i + 1), id, ...rest.slice(i + 1)]
}

/** One item's values and when each was last cleared (the item page). */
export interface ItemValues {
  values: Map<string, PropertyValue>
  cleared: Map<string, string>
}

/** Applies a value event of an item's own feed, with the same rules as the table. */
export function applyItemValueEvent(iv: ItemValues, item: string, ev: PaperEvent): ItemValues {
  const row: DbRow = { item: { id: item } as Paper, values: iv.values, cleared: iv.cleared }
  const s: DatabaseState = { db: { id: '' } as Paper, rows: new Map([[item, row]]), order: [item] }
  const n = applyDatabaseEvent(s, ev)
  if (n === s) return iv
  const r = n.rows.get(item)
  return r ? { values: r.values, cleared: r.cleared ?? new Map() } : iv
}
