import { describe, expect, it } from 'vitest'
import { History, type Lookup, type Snap } from './history'

const p = (content: string): Snap => ({ type: 'paragraph', content, props: {} })

function store(init: Record<string, { rev: number; snap: Snap }>) {
  const blocks = new Map(Object.entries(init))
  const lookup: Lookup = (id) => blocks.get(id)
  return { blocks, lookup }
}

describe('History', () => {
  it('merges the saves of one editing session and turns a split into one entry (T23)', () => {
    const h = new History()
    h.record([{ k: 'update', id: 'A', before: p('x'), after: p('x AAA') }], 's1')
    h.record([{ k: 'update', id: 'A', before: p('x AAA'), after: p('x AAA BBB') }], 's1')
    h.record([
      { k: 'update', id: 'A', before: p('x AAA BBB'), after: p('x AAA') },
      { k: 'insert', id: 'B', after: 'A', snap: p(' BBB') },
    ])
    expect(h.done).toHaveLength(2)
    const { lookup } = store({ A: { rev: 4, snap: p('x AAA') }, B: { rev: 1, snap: p(' BBB') } })
    const plan = h.plan('undo', lookup)
    expect(plan.ok && plan.ops).toEqual([
      { op: 'delete', block_id: 'B', base_rev: 1 },
      { op: 'update', block_id: 'A', type: 'paragraph', content: 'x AAA BBB', props: {}, base_rev: 4 },
    ])
  })

  it('undoing the first entry restores the text from before the whole session', () => {
    const h = new History()
    h.record([{ k: 'update', id: 'A', before: p('x'), after: p('x AAA') }], 's1')
    h.record([{ k: 'update', id: 'A', before: p('x AAA'), after: p('x AAA BBB') }], 's1')
    const plan = h.plan('undo', store({ A: { rev: 3, snap: p('x AAA BBB') } }).lookup)
    expect(plan.ok && plan.ops[0]).toMatchObject({ op: 'update', content: 'x', base_rev: 3 })
  })

  it('follows the new ID of a block brought back by undo (T24)', () => {
    const h = new History()
    h.record([{ k: 'delete', id: 'Q', after: 'P', snap: p('quote') }])
    const s = store({ P: { rev: 1, snap: p('p') } })
    const undo = h.plan('undo', s.lookup)
    expect(undo.ok).toBe(true)
    if (!undo.ok) return
    expect(undo.ops[0]).toMatchObject({ op: 'insert', after: 'P', content: 'quote', ref: 'h0' })
    h.applied('undo', undo, { h0: 'Q2' })
    s.blocks.set('Q2', { rev: 1, snap: p('quote') })
    const redo = h.plan('redo', s.lookup)
    expect(redo.ok && redo.ops).toEqual([{ op: 'delete', block_id: 'Q2', base_rev: 1 }])
  })

  it('refuses when someone else changed the block since (T25)', () => {
    const h = new History()
    h.record([{ k: 'update', id: 'B', before: p(''), after: p('CCC') }], 's2')
    const plan = h.plan('undo', store({ B: { rev: 5, snap: p('CCC Ken') } }).lookup)
    expect(plan).toEqual({ ok: false, reason: 'changed', blockId: 'B' })
  })

  it('has nothing to undo at first, and a new edit clears redo', () => {
    const h = new History()
    expect(h.plan('undo', () => undefined)).toEqual({ ok: false, reason: 'empty' })
    h.record([{ k: 'title', before: 'a', after: 'b' }])
    const plan = h.plan('undo', () => undefined)
    if (!plan.ok) throw new Error('expected a plan')
    expect(plan.ops).toEqual([{ op: 'set_title', title: 'a' }])
    h.applied('undo', plan, null)
    expect(h.undone).toHaveLength(1)
    h.record([{ k: 'title', before: 'a', after: 'c' }])
    expect(h.undone).toHaveLength(0)
  })
})
