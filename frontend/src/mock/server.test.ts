import { describe, expect, it } from 'vitest'
import type { Block, CommitResult, Conflict, PaperView } from '../api/types'
import { handle } from './server'

const call = (method: string, path: string, body?: unknown) => handle(method, new URL(`http://demo${path}`), body)

function setup() {
  const ws = (call('GET', '/api/v1/workspaces').json as { workspaces: { id: string }[] }).workspaces[0]?.id ?? ''
  const view = call('POST', `/api/v1/workspaces/${ws}/papers`, { title: 'T' }).json as PaperView
  const base = `/api/v1/workspaces/${ws}/papers/${view.paper.id}`
  const commit = (ops: unknown[], force = false) => call('POST', `${base}/commits`, { ops, force, message: '' })
  const r = commit([{ op: 'insert', type: 'paragraph', content: 'v1' }]).json as CommitResult
  return { commit, block: r.blocks[0] as Block }
}

describe('demo commits', () => {
  it('rejects a stale base with the current state, and overwrites when forced', () => {
    const { commit, block } = setup()
    expect(commit([{ op: 'update', block_id: block.id, content: 'x' }]).status).toBe(400)
    expect(commit([{ op: 'update', block_id: block.id, content: 'theirs', base_version: 1 }]).status).toBe(200)

    const res = commit([{ op: 'update', block_id: block.id, content: 'mine', base_version: 1 }])
    expect(res.status).toBe(409)
    const conflicts = (res.json as { error: { conflicts: Conflict[] } }).error.conflicts
    expect(conflicts[0]?.current?.content).toBe('theirs')

    const forced = commit([{ op: 'update', block_id: block.id, content: 'mine', base_version: 1 }], true).json as CommitResult
    expect(forced.commit?.changes[0]?.overwrote_version).toBe(2)
  })

  it('does not treat moves as conflicts and restores old versions', () => {
    const { commit, block } = setup()
    commit([{ op: 'insert', type: 'paragraph', content: 'other' }])
    commit([{ op: 'move', block_id: block.id }])
    expect(commit([{ op: 'update', block_id: block.id, content: 'v3', base_version: 1 }]).status).toBe(200)

    const r = commit([{ op: 'restore', block_id: block.id, version: 1, base_version: 3 }]).json as CommitResult
    expect(r.blocks[0]?.content).toBe('v1')
    commit([{ op: 'delete', block_id: block.id, base_version: 4 }])
    const back = commit([{ op: 'restore', block_id: block.id, version: 3 }]).json as CommitResult
    expect(back.blocks[0]?.content).toBe('v3')
  })
})
