import type { Actor, Block, Paper, PaperRef, Presence, TableLine } from './types'

// Client-side model of an open paper. Changes arrive as resulting states
// (from an ops response or the change feed) and are applied idempotently:
// a block with a lower revision than the copy we hold is ignored (a move
// keeps the revision, so equal revisions still update the position).

export interface PaperState {
  paper: Paper
  blocks: Map<string, Block>
  /** Child papers of the paper blocks, as far as the reader may see them. */
  children: Map<string, PaperRef>
  /** Who is editing which block right now (other people and agents). */
  presence: Map<string, Presence>
}

export function initState(paper: Paper, blocks: Block[], children: Record<string, PaperRef> = {}): PaperState {
  return { paper, blocks: new Map(blocks.map((b) => [b.id, b])), children: new Map(Object.entries(children)), presence: new Map() }
}

/** Applies block states and deletions. Returns the same object if nothing changed. */
export function applyBlocks(state: PaperState, put: Block[], deleted: string[] = []): PaperState {
  let blocks: Map<string, Block> | undefined
  const writable = () => (blocks ??= new Map(state.blocks))
  for (const b of put) {
    const cur = (blocks ?? state.blocks).get(b.id)
    if (cur && (cur.rev > b.rev || (cur.rev === b.rev && cur.order === b.order && cur.parent_id === b.parent_id))) continue
    writable().set(b.id, b)
  }
  for (const id of deleted) {
    if ((blocks ?? state.blocks).has(id)) writable().delete(id)
  }
  return blocks ? { ...state, blocks } : state
}

/** Replaces a block shown under a temporary key by the server's block. */
export function replaceTemp(state: PaperState, tempId: string, real: Block | undefined): PaperState {
  if (!state.blocks.has(tempId)) return state
  const blocks = new Map(state.blocks)
  blocks.delete(tempId)
  if (real) blocks.set(real.id, real)
  return { ...state, blocks }
}

export function applyPaper(state: PaperState, paper: Paper): PaperState {
  return { ...state, paper }
}

export function applyChildren(state: PaperState, refs: Record<string, PaperRef>): PaperState {
  return { ...state, children: new Map([...state.children, ...Object.entries(refs)]) }
}

export function applyPresence(state: PaperState, p: Presence, me: string): PaperState {
  if (p.actor.user_id === me && p.actor.kind === 'human') return state
  const presence = new Map(state.presence)
  const key = presenceKey(p.actor)
  if (p.leaving) presence.delete(key)
  else presence.set(key, p)
  return { ...state, presence }
}

export function presenceKey(a: Actor): string {
  return `${a.user_id}:${a.agent_name ?? ''}`
}

/** The people and agents editing a block now. */
export function editorsOf(state: PaperState, blockId: string, now = Date.now()): Actor[] {
  return [...state.presence.values()].filter((p) => p.block_id === blockId && Date.parse(p.until) > now).map((p) => p.actor)
}

export function byOrder(a: { order: string; id: string }, b: { order: string; id: string }): number {
  if (a.order !== b.order) return a.order < b.order ? -1 : 1
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

export function topLevel(state: PaperState): Block[] {
  return [...state.blocks.values()].filter((b) => !b.parent_id).sort(byOrder)
}

export function sortedLines(lines: TableLine[] | undefined): TableLine[] {
  return [...(lines ?? [])].sort(byOrder)
}

/** Cells of a table keyed by "rowId:colId". */
export function cellsOf(blocks: Iterable<Block>, tableId: string): Map<string, Block> {
  const out = new Map<string, Block>()
  for (const b of blocks) {
    if (b.parent_id === tableId) out.set(`${b.props.row_id ?? ''}:${b.props.col_id ?? ''}`, b)
  }
  return out
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

/** "Claude (for Alice)", a person, or a person writing through the API (D7). */
export function actorLabel(a: Actor, viaApi = 'API'): string {
  if (a.kind === 'agent') return `${a.agent_name || 'AI'} (${a.name})`
  if (a.kind === 'system') return 'papers'
  if (a.token_id) return `${a.name} · ${viaApi}`
  return a.name
}
