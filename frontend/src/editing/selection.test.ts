import { describe, expect, it } from 'vitest'
import { dropAfter, moveOps, normalize, overlapping, selectedIds, stepAfter } from './selection'

const list = ['P', 'A', 'B', 'C', 'D', 'E'].map((id) => ({ id }))
const spans = list.map((b, i) => ({ id: b.id, top: i * 30, bottom: i * 30 + 24 }))

describe('selection', () => {
  it('selects the blocks the band overlaps vertically; the anchor is the end where the press started (T62)', () => {
    expect(overlapping(spans, 40, 130)).toEqual({ anchor: 'A', focus: 'D' })
    expect(overlapping(spans, 130, 40)).toEqual({ anchor: 'D', focus: 'A' })
    expect(selectedIds(list, overlapping(spans, 130, 40))).toEqual(['A', 'B', 'C', 'D'])
  })

  it('selects nothing when the band lies between two blocks (T63)', () => {
    expect(overlapping(spans, 25, 29)).toBeNull()
  })

  it('takes the blocks between anchor and focus in list order (T68)', () => {
    expect(selectedIds(list, { anchor: 'D', focus: 'B' })).toEqual(['B', 'C', 'D'])
    expect(selectedIds(list, { anchor: 'C', focus: 'C' })).toEqual(['C'])
    expect(selectedIds(list, null)).toEqual([])
  })

  it('falls back to the end that is left when the other is gone (T69)', () => {
    const rest = list.filter((b) => b.id !== 'B')
    expect(normalize(rest, { anchor: 'B', focus: 'D' })).toEqual({ anchor: 'D', focus: 'D' })
    expect(normalize(rest, { anchor: 'D', focus: 'B' })).toEqual({ anchor: 'D', focus: 'D' })
    expect(normalize(list.filter((b) => !['B', 'D'].includes(b.id)), { anchor: 'B', focus: 'D' })).toBeNull()
  })

  it('moves the selected blocks as one batch keeping their order (T71)', () => {
    expect(moveOps(list, ['B', 'C'], 'E')).toEqual([
      { op: 'move', block_id: 'B', after: 'E' },
      { op: 'move', block_id: 'C', after: 'B' },
    ])
    expect(dropAfter(list, ['B', 'C'], { id: 'E', before: false })).toBe('E')
    expect(dropAfter(list, ['B', 'C'], { id: 'P', before: true })).toBe('')
    expect(stepAfter(list, ['B', 'C'], -1)).toBe('P')
    expect(stepAfter(list, ['B', 'C'], 1)).toBe('D')
    expect(stepAfter(list, ['A', 'B'], -1)).toBe('')
  })

  it('sends nothing for a drop inside or next to the selection, or a step past the ends (T72)', () => {
    expect(dropAfter(list, ['B', 'C'], { id: 'B', before: true })).toBeUndefined()
    expect(dropAfter(list, ['B', 'C'], { id: 'C', before: false })).toBeUndefined()
    expect(dropAfter(list, ['B', 'C'], { id: 'A', before: false })).toBeUndefined()
    expect(dropAfter(list, ['B', 'C'], { id: 'D', before: true })).toBeUndefined()
    expect(moveOps(list, ['B', 'C'], 'A')).toEqual([])
    expect(moveOps(list, ['B', 'C'], 'B')).toEqual([])
    expect(stepAfter(list, ['P', 'A'], -1)).toBeUndefined()
    expect(stepAfter(list, ['D', 'E'], 1)).toBeUndefined()
  })
})
