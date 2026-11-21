import { describe, expect, it } from 'vitest'
import { actorLabel, applyBlocks, applyPaper, applyPresence, cellsOf, editorsOf, initState, listNumbers, replaceTemp, sortedLines, topLevel } from './paperState'
import type { Actor, Block, Paper } from './types'

const actor: Actor = { kind: 'human', user_id: 'u', name: 'U' }
const paper: Paper = {
  id: 'p', workspace_id: 'w', title: 'T', visibility: 'public', version_number: 0,
  created_by: actor, created_at: '', updated_by: actor, updated_at: '',
}
const block = (id: string, order: string, extra: Partial<Block> = {}): Block => ({
  id, workspace_id: 'w', paper_id: 'p', order, type: 'paragraph', content: id, props: {}, rev: 1,
  created_by: actor, created_at: '', updated_by: actor, updated_at: '', ...extra,
})

describe('applyBlocks', () => {
  it('upserts newer revisions and deletes', () => {
    const s = initState(paper, [block('a', 'V'), block('c', 'k')])
    const next = applyBlocks(s, [block('b', 'X'), block('a', 'V', { content: 'A', rev: 2 })], ['c'])
    expect(topLevel(next).map((b) => b.content)).toEqual(['A', 'b'])
  })

  it('ignores stale or repeated states but takes moves', () => {
    const s = initState(paper, [block('a', 'V', { content: 'new', rev: 3 })])
    expect(applyBlocks(s, [block('a', 'V', { content: 'old', rev: 2 })])).toBe(s)
    expect(applyBlocks(s, [block('a', 'V', { content: 'new', rev: 3 })])).toBe(s)
    expect(applyBlocks(s, [], ['missing'])).toBe(s)
    expect(applyBlocks(s, [block('a', 'W', { content: 'new', rev: 3 })]).blocks.get('a')?.order).toBe('W')
  })

  it('swaps a temporary block for the real one', () => {
    const s = initState(paper, [block('tmp:1', 'V')])
    const next = replaceTemp(s, 'tmp:1', block('real', 'V'))
    expect([...next.blocks.keys()]).toEqual(['real'])
  })

  it('replaces the paper', () => {
    const s = initState(paper, [])
    expect(applyPaper(s, { ...paper, title: 'New' }).paper.title).toBe('New')
  })
})

describe('helpers', () => {
  it('places table cells by row and column id, in line order', () => {
    const t = block('t', 'V', { type: 'table', props: { rows: [{ id: 'r2', order: 'b' }, { id: 'r1', order: 'a' }], cols: [{ id: 'c1', order: 'a' }] } })
    const s = initState(paper, [t, block('c', 'V', { parent_id: 't', type: 'table_cell', props: { row_id: 'r1', col_id: 'c1' } })])
    expect(cellsOf(s.blocks.values(), 't').get('r1:c1')?.id).toBe('c')
    expect(sortedLines(t.props.rows).map((l) => l.id)).toEqual(['r1', 'r2'])
    expect(topLevel(s).map((b) => b.id)).toEqual(['t'])
  })

  it('numbers lists per indent and restarts after other blocks', () => {
    const n = (id: string, indent = 0) => block(id, 'V', { type: 'numbered_list_item', props: { indent } })
    const nums = listNumbers([n('a'), n('b'), n('b1', 1), n('c'), block('p', 'V'), n('d')])
    expect([...nums.entries()]).toEqual([['a', 1], ['b', 2], ['b1', 1], ['c', 3], ['d', 1]])
  })

  it('tracks who edits what, without myself', () => {
    const bob: Actor = { kind: 'human', user_id: 'bob', name: 'Bob' }
    const until = new Date(Date.now() + 10000).toISOString()
    let s = initState(paper, [])
    s = applyPresence(s, { block_id: 'a', actor: bob, until }, 'u')
    s = applyPresence(s, { block_id: 'a', actor, until }, 'u')
    expect(editorsOf(s, 'a').map((a) => a.name)).toEqual(['Bob'])
    s = applyPresence(s, { block_id: 'a', actor: bob, until, leaving: true }, 'u')
    expect(editorsOf(s, 'a')).toEqual([])
  })

  it('labels agents and API writes', () => {
    expect(actorLabel({ kind: 'agent', user_id: 'u', name: 'Alice', agent_name: 'Claude' })).toBe('Claude (Alice)')
    expect(actorLabel({ kind: 'human', user_id: 'u', name: 'Alice', token_id: 't' }, 'API')).toBe('Alice · API')
  })
})
