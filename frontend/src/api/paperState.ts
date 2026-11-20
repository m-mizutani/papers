import type { Block, BlockLock, Paper } from './types'

// Client-side model of an open paper. Changes arrive as resulting states
// (from a commit response or the change feed) and are applied
// idempotently: a block older than the copy we hold, by version, is ignored.

export interface PaperState {
  paper: Paper
  blocks: Map<string, Block>
  locks: Map<string, BlockLock>
}

export function initState(paper: Paper, blocks: Block[], locks: BlockLock[]): PaperState {
  return {
    paper,
    blocks: new Map(blocks.map((b) => [b.id, b])),
    locks: new Map(locks.map((l) => [l.block_id, l])),
  }
}

/** Applies block states and deletions. Returns the same object if nothing changed. */
export function applyBlocks(state: PaperState, put: Block[], deleted: string[] = []): PaperState {
  let blocks: Map<string, Block> | undefined
  const writable = () => (blocks ??= new Map(state.blocks))
  for (const b of put) {
    const cur = (blocks ?? state.blocks).get(b.id)
    if (cur && cur.version >= b.version) continue
    writable().set(b.id, b)
  }
  for (const id of deleted) {
    if ((blocks ?? state.blocks).has(id)) writable().delete(id)
  }
  return blocks ? { ...state, blocks } : state
}

export function applyPaper(state: PaperState, paper: Paper): PaperState {
  return { ...state, paper }
}

export function applyLock(state: PaperState, lock: BlockLock): PaperState {
  const locks = new Map(state.locks)
  locks.set(lock.block_id, lock)
  return { ...state, locks }
}

export function applyUnlock(state: PaperState, blockId: string, holderKey: string): PaperState {
  const cur = state.locks.get(blockId)
  if (!cur || cur.holder_key !== holderKey) return state
  const locks = new Map(state.locks)
  locks.delete(blockId)
  return { ...state, locks }
}

function byOrder(a: Block, b: Block): number {
  if (a.order !== b.order) return a.order < b.order ? -1 : 1
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

export function topLevel(state: PaperState): Block[] {
  return [...state.blocks.values()].filter((b) => !b.parent_id).sort(byOrder)
}

export function cellsOf(state: PaperState, tableId: string): Map<string, Block> {
  const out = new Map<string, Block>()
  for (const b of state.blocks.values()) {
    if (b.parent_id === tableId) out.set(`${b.props.row ?? 0}:${b.props.col ?? 0}`, b)
  }
  return out
}

/** Returns the lock on a block if it is active and held by someone else. */
export function foreignLock(state: PaperState, blockId: string, myKey: string, now: number): BlockLock | undefined {
  const l = state.locks.get(blockId)
  if (!l || l.holder_key === myKey) return undefined
  return Date.parse(l.expires_at) > now ? l : undefined
}

/** Numbering for consecutive numbered list items at the same indent. */
export function listNumbers(blocks: Block[]): Map<string, number> {
  const out = new Map<string, number>()
  const counters: number[] = []
  for (const b of blocks) {
    if (b.type !== 'numbered_list_item') {
      if (b.type !== 'bulleted_list_item' && b.type !== 'todo') counters.length = 0
      continue
    }
    const indent = b.props.indent ?? 0
    counters.length = indent + 1
    counters[indent] = (counters[indent] ?? 0) + 1
    out.set(b.id, counters[indent] ?? 1)
  }
  return out
}

export function actorLabel(a: { kind: string; name: string; agent_name?: string }): string {
  return a.kind === 'agent' ? (a.agent_name || 'AI') : a.name
}
