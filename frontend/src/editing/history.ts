import type { BlockProps, BlockType, Op } from '../api/types'

// Undo and redo of one's own edits in one paper (spec: R10). This is not a
// stored history: undoing sends the inverse of a recorded step as ordinary
// writes. A step whose block someone else changed since is refused, so
// another writer's change is never overwritten silently. The record lives
// in the page only.

export interface Snap {
  type: BlockType
  content: string
  props: BlockProps
}

export type Step =
  | { k: 'update'; id: string; before: Snap; after: Snap }
  /** after: the block before it ('' = top), as when it was inserted. */
  | { k: 'insert'; id: string; after: string; snap: Snap }
  | { k: 'delete'; id: string; after: string; snap: Snap }
  /** from / to: the block before it ('' = top) before and after the move. */
  | { k: 'move'; id: string; from: string; to: string }
  | { k: 'title'; before: string; after: string }

export interface Entry {
  steps: Step[]
  /** Saves of one editing session merge into one entry. */
  session?: string
}

/** The current contents of a block, or undefined when it does not exist. */
export type Lookup = (id: string) => { rev: number; snap: Snap } | undefined

export type Plan =
  | { ok: true; ops: Op[]; entry: Entry; refs: Map<string, string> }
  | { ok: false; reason: 'empty' }
  | { ok: false; reason: 'changed'; blockId: string }

const MAX = 200

function stable(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(stable).join(',')}]`
  if (v && typeof v === 'object') {
    return `{${Object.keys(v).filter((k) => (v as Record<string, unknown>)[k] !== undefined).sort().map((k) => `${k}:${stable((v as Record<string, unknown>)[k])}`).join(',')}}`
  }
  return JSON.stringify(v)
}

export function sameSnap(a: Snap | undefined, b: Snap | undefined): boolean {
  return !!a && !!b && a.type === b.type && a.content === b.content && stable(a.props) === stable(b.props)
}

export class History {
  done: Entry[] = []
  undone: Entry[] = []
  /** A block re-inserted by undo or redo gets a new ID from the server. */
  private ids = new Map<string, string>()

  record(steps: Step[], session?: string) {
    if (steps.length === 0) return
    this.undone = []
    const top = this.done[this.done.length - 1]
    const only = steps[0]
    const topOnly = top?.steps[0]
    if (session && top?.session === session && steps.length === 1 && top.steps.length === 1 && only?.k === 'update' && topOnly?.k === 'update' && topOnly.id === only.id) {
      topOnly.after = only.after
      return
    }
    this.done.push({ steps, session })
    if (this.done.length > MAX) this.done.shift()
  }

  clear() {
    this.done = []
    this.undone = []
    this.ids.clear()
  }

  resolve(id: string): string {
    let cur = id
    for (let i = 0; i < 1000 && this.ids.has(cur); i++) cur = this.ids.get(cur) ?? cur
    return cur
  }

  /** The ops for undoing (or redoing) the latest entry, or why it cannot be done. */
  plan(dir: 'undo' | 'redo', lookup: Lookup, title?: string): Plan {
    const stack = dir === 'undo' ? this.done : this.undone
    const entry = stack[stack.length - 1]
    if (!entry) return { ok: false, reason: 'empty' }
    const steps = dir === 'undo' ? [...entry.steps].reverse() : entry.steps
    const ops: Op[] = []
    const refs = new Map<string, string>()
    // Blocks brought back in this plan, by their recorded ID: one can be the
    // anchor of another (undoing the delete of adjacent blocks), so inserts
    // go last, each after the one it follows, referring to it by ref.
    const inserts: { ref: string; id: string; after: string; snap: Snap }[] = []
    const back = new Map<string, string>()
    const existing = (id: string) => {
      if (id === '') return ''
      const real = this.resolve(id)
      return lookup(real) ? real : undefined
    }
    const anchor = (id: string) => {
      const r = back.get(id)
      return r ? `ref:${r}` : existing(id)
    }
    for (const [i, s] of steps.entries()) {
      if (s.k === 'title') {
        const want = dir === 'undo' ? s.after : s.before
        if (title !== undefined && title !== want) return { ok: false, reason: 'changed', blockId: '' }
        ops.push({ op: 'set_title', title: dir === 'undo' ? s.before : s.after })
        continue
      }
      const id = this.resolve(s.id)
      const cur = lookup(id)
      if (s.k === 'update') {
        const want = dir === 'undo' ? s.after : s.before
        const to = dir === 'undo' ? s.before : s.after
        if (!cur || !sameSnap(cur.snap, want)) return { ok: false, reason: 'changed', blockId: id }
        ops.push({ op: 'update', block_id: id, type: to.type, content: to.content, props: to.props, base_rev: cur.rev })
        continue
      }
      if (s.k === 'move') {
        if (!cur) return { ok: false, reason: 'changed', blockId: id }
        ops.push({ op: 'move', block_id: id, after: existing(dir === 'undo' ? s.from : s.to) })
        continue
      }
      const removes = (s.k === 'insert') === (dir === 'undo')
      if (removes) {
        if (!cur) continue
        if (!sameSnap(cur.snap, s.snap)) return { ok: false, reason: 'changed', blockId: id }
        ops.push({ op: 'delete', block_id: id, base_rev: cur.rev })
      } else {
        const ref = `h${i}`
        refs.set(ref, s.id)
        back.set(s.id, ref)
        inserts.push({ ref, id: s.id, after: s.after, snap: s.snap })
      }
    }
    // An insert goes after the insert it is anchored on.
    const pending = [...inserts]
    while (pending.length) {
      const k = pending.findIndex((x) => !pending.some((y) => y !== x && y.id === x.after))
      const [next] = pending.splice(k < 0 ? 0 : k, 1)
      if (!next) break
      ops.push({ op: 'insert', ref: next.ref, type: next.snap.type, content: next.snap.content, props: next.snap.props, after: anchor(next.after) })
    }
    return { ok: true, ops, entry, refs }
  }

  /** Moves the entry to the other stack once its ops were applied. */
  applied(dir: 'undo' | 'redo', plan: Extract<Plan, { ok: true }>, serverRefs: Record<string, string> | null) {
    const from = dir === 'undo' ? this.done : this.undone
    const to = dir === 'undo' ? this.undone : this.done
    const i = from.lastIndexOf(plan.entry)
    if (i >= 0) from.splice(i, 1)
    to.push({ ...plan.entry, session: undefined })
    for (const [ref, original] of plan.refs) {
      const fresh = serverRefs?.[ref]
      if (fresh && fresh !== this.resolve(original)) this.ids.set(this.resolve(original), fresh)
    }
  }
}
