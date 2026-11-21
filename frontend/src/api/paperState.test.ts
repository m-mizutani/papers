import { describe, expect, it } from 'vitest'
import { applyBlocks, applyPaper, cellsOf, initState, listNumbers, topLevel } from './paperState'
import type { Actor, Block, Paper } from './types'

const actor: Actor = { kind: 'human', user_id: 'u', name: 'U' }
const paper: Paper = {
  id: 'p', workspace_id: 'w', title: 'T',
  access: { restricted: false, bindings: [] },
  created_by: actor, created_at: '', updated_by: actor, updated_at: '',
}
const block = (id: string, order: string, extra: Partial<Block> = {}): Block => ({
  id, workspace_id: 'w', paper_id: 'p', order, type: 'paragraph', content: id, props: {}, version: 1,
  created_by: actor, created_at: '', updated_by: actor, updated_at: '', ...extra,
})

describe('applyBlocks', () => {
  it('upserts newer versions and deletes', () => {
    const s = initState(paper, [block('a', 'V'), block('c', 'k')])
    const next = applyBlocks(s, [block('b', 'X'), block('a', 'V', { content: 'A', version: 2 })], ['c'])
    expect(topLevel(next).map((b) => b.content)).toEqual(['A', 'b'])
  })

  it('ignores stale or repeated states', () => {
    const s = initState(paper, [block('a', 'V', { content: 'new', version: 3 })])
    expect(applyBlocks(s, [block('a', 'V', { content: 'old', version: 2 })])).toBe(s)
    expect(applyBlocks(s, [block('a', 'V', { content: 'new', version: 3 })])).toBe(s)
    expect(applyBlocks(s, [], ['missing'])).toBe(s)
  })

  it('replaces the paper', () => {
    const s = initState(paper, [])
    expect(applyPaper(s, { ...paper, title: 'New' }).paper.title).toBe('New')
  })
})

describe('helpers', () => {
  it('groups table cells by position', () => {
    const s = initState(paper, [block('t', 'V', { type: 'table' }), block('c', 'V', { parent_id: 't', type: 'table_cell', props: { row: 1, col: 2 } })])
    expect(cellsOf(s, 't').get('1:2')?.id).toBe('c')
    expect(topLevel(s).map((b) => b.id)).toEqual(['t'])
  })

  it('numbers lists per indent and restarts after other blocks', () => {
    const n = (id: string, indent = 0) => block(id, 'V', { type: 'numbered_list_item', props: { indent } })
    const nums = listNumbers([n('a'), n('b'), n('b1', 1), n('c'), block('p', 'V'), n('d')])
    expect([...nums.entries()]).toEqual([['a', 1], ['b', 2], ['b1', 1], ['c', 3], ['d', 1]])
  })
})
