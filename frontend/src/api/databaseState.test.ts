import { describe, expect, it } from 'vitest'
import { applyDatabaseEvent, applyItemValueEvent, chosenElements, initDatabase, moveAfter, nextColor, putValue, shownValue, visibleRows } from './databaseState'
import type { Actor, Paper, PaperEvent, Property, PropertyValue } from './types'

const actor: Actor = { kind: 'human', user_id: 'u', name: 'Alice' }

function paper(id: string, extra: Partial<Paper> = {}): Paper {
  return {
    id, workspace_id: 'w', title: id, visibility: 'public', version_number: 0, created_by: actor, created_at: '2026-01-01T00:00:00Z',
    updated_by: actor, updated_at: '2026-01-01T00:00:00Z', ...extra,
  }
}

const status: Property = { id: 'pA', name: '状態', type: 'select', elements: [{ id: 'e1', value: '未着手', color: 'gray' }, { id: 'e2', value: '進行中', color: 'blue' }] }
const tags: Property = { id: 'pT', name: 'タグ', type: 'multi_select', elements: [{ id: 'g1', value: '検索', color: 'green' }] }

function value(item: string, prop: string, v: string | string[], at: string): PropertyValue {
  return { workspace_id: 'w', database_id: 'db', paper_id: item, property_id: prop, value: v, updated_by: actor, updated_at: at }
}

const db = paper('db', { kind: 'database', properties: [status, tags], items: ['a', 'b'] })
const base = () => initDatabase(db, [
  { item: paper('a', { database_id: 'db' }), values: [value('a', 'pA', 'e2', '2026-01-01T00:00:01Z')] },
  { item: paper('b', { database_id: 'db' }), values: [] },
])

describe('database state', () => {
  // T34: events apply by the newer state, and twice is the same as once
  it('applies a value only when newer, and idempotently', () => {
    const ev: PaperEvent = { type: 'property_value_put', workspace_id: 'w', paper_id: 'db', at: '', property_value: value('a', 'pA', 'e1', '2026-01-01T00:00:02Z') }
    const once = applyDatabaseEvent(base(), ev)
    const twice = applyDatabaseEvent(once, ev)
    expect(once.rows.get('a')?.values.get('pA')?.value).toBe('e1')
    expect(twice).toEqual(once)
    const stale = applyDatabaseEvent(once, { ...ev, property_value: value('a', 'pA', 'e2', '2026-01-01T00:00:01Z') })
    expect(stale.rows.get('a')?.values.get('pA')?.value).toBe('e1')
  })

  it('removes a value, an item and takes an item update', () => {
    let s = applyDatabaseEvent(base(), { type: 'property_value_deleted', workspace_id: 'w', paper_id: 'db', at: '', item_id: 'a', property_id: 'pA' })
    expect(s.rows.get('a')?.values.size).toBe(0)
    s = applyDatabaseEvent(s, { type: 'item_put', workspace_id: 'w', paper_id: 'db', at: '', paper: paper('b', { database_id: 'db', title: 'B2', updated_at: '2026-01-02T00:00:00Z' }) })
    expect(s.rows.get('b')?.item.title).toBe('B2')
    s = applyDatabaseEvent(s, { type: 'item_put', workspace_id: 'w', paper_id: 'db', at: '', paper: paper('b', { database_id: 'db', archived_at: '2026-01-03T00:00:00Z', updated_at: '2026-01-03T00:00:00Z' }) })
    expect(visibleRows(s).map((r) => r.item.id)).toEqual(['a'])
    s = applyDatabaseEvent(s, { type: 'item_deleted', workspace_id: 'w', paper_id: 'db', at: '', item_id: 'a' })
    expect(s.order).toEqual(['b'])
  })

  it('orders a clearing and a put by their times, whichever comes first', () => {
    const put: PaperEvent = { type: 'property_value_put', workspace_id: 'w', paper_id: 'db', at: '', property_value: value('a', 'pA', 'e1', '2026-01-01T00:00:03Z') }
    const clear: PaperEvent = { type: 'property_value_deleted', workspace_id: 'w', paper_id: 'db', at: '2026-01-01T00:00:02Z', item_id: 'a', property_id: 'pA' }
    // the newer put first: the older clearing changes nothing
    const a = applyDatabaseEvent(applyDatabaseEvent(base(), put), clear)
    expect(a.rows.get('a')?.values.get('pA')?.value).toBe('e1')
    // the clearing first: an older put that arrives late does not bring the value back
    const stale = { ...put, property_value: value('a', 'pA', 'e2', '2026-01-01T00:00:01Z') }
    const b = applyDatabaseEvent(applyDatabaseEvent(base(), clear), stale)
    expect(b.rows.get('a')?.values.has('pA')).toBe(false)
    // and the same clearing twice is the same as once
    expect(applyDatabaseEvent(applyDatabaseEvent(base(), clear), clear)).toEqual(applyDatabaseEvent(base(), clear))
    // an item page applies the same rules
    const iv = applyItemValueEvent({ values: new Map(), cleared: new Map() }, 'a', clear)
    expect(applyItemValueEvent(iv, 'a', stale).values.size).toBe(0)
  })

  it('takes a newer database record', () => {
    const renamed = { ...db, properties: [{ ...status, name: 'ステータス' }, tags], updated_at: '2026-01-05T00:00:00Z' }
    const s = applyDatabaseEvent(base(), { type: 'paper_updated', workspace_id: 'w', paper_id: 'db', at: '', paper: renamed })
    expect(s.db.properties?.[0]?.name).toBe('ステータス')
  })

  it('shows nothing for a deleted element', () => {
    expect(shownValue(status, value('a', 'pA', 'gone', ''))).toBeUndefined()
    expect(shownValue(status, value('a', 'pA', 'e1', ''))).toBe('e1')
    expect(shownValue(tags, value('a', 'pT', ['gone', 'g1'], ''))).toEqual(['g1'])
    expect(shownValue(tags, value('a', 'pT', ['gone'], ''))).toBeUndefined()
    expect(chosenElements(status, 'e2').map((e) => e.value)).toEqual(['進行中'])
  })

  it('ignores a value of an item not read yet', () => {
    const s = putValue(base(), value('zzz', 'pA', 'e1', '2026-01-09T00:00:00Z'))
    expect(s.rows.has('zzz')).toBe(false)
  })

  it('picks the least used colour and moves ids', () => {
    expect(nextColor(status)).toBe('brown')
    expect(moveAfter(['a', 'b', 'c'], 'c', undefined)).toEqual(['c', 'a', 'b'])
    expect(moveAfter(['a', 'b', 'c'], 'a', 'b')).toEqual(['b', 'a', 'c'])
  })
})
