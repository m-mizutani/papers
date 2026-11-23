import { describe, expect, it } from 'vitest'
import type { BatchResult, Block, Conflict, Paper } from '../api/types'
import { handle } from './server'

const call = (method: string, path: string, body?: unknown) => handle(method, new URL(`http://demo${path}`), body)

function setup() {
  const paper = call('POST', '/api/v1/workspaces/security/papers', { title: 'T' }).json as Paper
  const base = `/api/v1/workspaces/security/papers/${paper.id}`
  const ops = (list: unknown[], force = false) => call('POST', `${base}/ops`, { ops: list, force })
  const r = ops([{ op: 'insert', ref: 'n1', type: 'paragraph', content: 'v1' }]).json as BatchResult
  return { base, ops, block: r.blocks?.[0] as Block, refs: r.refs ?? {} }
}

describe('demo ops', () => {
  it('issues IDs for refs', () => {
    const { block, refs } = setup()
    expect(refs.n1).toBe(block.id)
    expect(block.id).toHaveLength(22)
  })

  it('rejects a stale base with the current state, and overwrites when forced', () => {
    const { ops, block } = setup()
    expect(ops([{ op: 'update', block_id: block.id, content: 'x' }]).status).toBe(400)
    expect(ops([{ op: 'update', block_id: block.id, content: 'theirs', base_rev: 1 }]).status).toBe(200)
    const res = ops([{ op: 'update', block_id: block.id, content: 'mine', base_rev: 1 }])
    expect(res.status).toBe(409)
    const conflicts = (res.json as { error: { conflicts: Conflict[] } }).error.conflicts
    expect(conflicts[0]?.current?.content).toBe('theirs')
    const forced = ops([{ op: 'update', block_id: block.id, content: 'mine', base_rev: 1 }], true).json as BatchResult
    expect(forced.blocks?.[0]?.rev).toBe(3)
  })

  it('saves versions only when asked and when something changed', () => {
    const { base, ops, block } = setup()
    const v1 = call('POST', `${base}/versions`, { note: 'first' }).json as { version: { number: number } }
    expect(v1.version.number).toBe(1)
    expect((call('POST', `${base}/versions`, {}).json as { version: unknown }).version).toBeNull()
    ops([{ op: 'update', block_id: block.id, content: 'changed', base_rev: 1 }])
    expect((call('GET', `${base}/status`).json as { changed: boolean }).changed).toBe(true)
  })
})

describe('demo papers', () => {
  const ws = '/api/v1/workspaces/security'
  it('creates papers without a parent as unfiled and lists them', () => {
    const paper = call('POST', `${ws}/papers`, { title: 'Loose note' }).json as Paper
    expect(paper.parent_id).toBeUndefined()
    const list = call('GET', `${ws}/papers?place=unfiled&creator=me`).json as { papers: { id: string }[] }
    expect(list.papers.some((x) => x.id === paper.id)).toBe(true)
    const sidebar = call('GET', `${ws}/sidebar`).json as { unfiled_count: number; favorites: unknown[] }
    expect(sidebar.unfiled_count).toBeGreaterThan(0)
    expect(sidebar.favorites).toEqual([])
  })

  it('moves a paper into the tree and back out', () => {
    const paper = call('POST', `${ws}/papers`, { title: 'To file' }).json as Paper
    expect(call('POST', `${ws}/papers/${paper.id}/move`, { parent: 'p-home-sec' }).status).toBe(200)
    const filed = call('GET', `${ws}/papers?place=unfiled&creator=me`).json as { papers: { id: string }[] }
    expect(filed.papers.some((x) => x.id === paper.id)).toBe(false)
    const kids = call('GET', `${ws}/papers/p-home-sec/children`).json as { papers: { id: string }[] }
    expect(kids.papers.some((x) => x.id === paper.id)).toBe(true)
    expect(call('POST', `${ws}/papers/${paper.id}/move`, { unfiled: true }).status).toBe(200)
    const back = call('GET', `${ws}/papers?place=unfiled&creator=me`).json as { papers: { id: string }[] }
    expect(back.papers.some((x) => x.id === paper.id)).toBe(true)
  })

  it('keeps favorites, but not the home paper', () => {
    expect(call('PUT', `${ws}/papers/p-runbook/favorite`, {}).status).toBe(204)
    const sidebar = call('GET', `${ws}/sidebar`).json as { favorites: { id: string }[] }
    expect(sidebar.favorites.map((x) => x.id)).toContain('p-runbook')
    expect(call('PUT', `${ws}/papers/p-home-sec/favorite`, {}).status).toBe(400)
    expect(call('DELETE', `${ws}/papers/p-runbook/favorite`).status).toBe(204)
  })
})
