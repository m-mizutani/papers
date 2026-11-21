import type {
  Actor, BatchResult, Block, BlockProps, BlockType, Conflict, Diff, Op, Paper, PaperEvent, PaperRef, TableLine, VersionSummary,
} from '../api/types'
import { normalize as normalizeMarkup } from '../markup/markup'
import { keyBetween } from './order'
import { ALICE, BOB, ME, claudeFor, human, seed } from './seed'

// An in-browser stand-in for the Go server, used by the demo build. It keeps
// the same rules for ops (server-issued IDs with refs, base revisions,
// conflicts unless forced, moves never conflict), versions saved only on
// request, and simulates two collaborators: Alice (a person) and Claude (an
// AI agent).

class HttpError extends Error {
  status: number
  code: string
  conflicts?: Conflict[]
  constructor(status: number, code: string, message: string, conflicts?: Conflict[]) {
    super(message)
    this.status = status
    this.code = code
    this.conflicts = conflicts
  }
}

interface Version extends VersionSummary {
  blocks: Block[]
}

interface PaperData {
  paper: Paper
  blocks: Map<string, Block>
  versions: Version[]
}

type Socket = { paperId: string; deliver: (msg: object) => void }

const data = seed()
const workspaces = new Map(data.workspaces.map((w) => [w.id, w]))
const papers = new Map<string, PaperData>()
for (const p of data.papers) papers.set(p.paper.id, { paper: p.paper, blocks: new Map(p.blocks.map((b) => [b.id, b])), versions: [] })
const pins = new Map<string, string[]>()
const recents = new Map<string, string[]>()
const sockets = new Set<Socket>()

const ALNUM = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz'
const newId = () => Array.from(crypto.getRandomValues(new Uint8Array(22)), (n) => ALNUM[n % 62]).join('')
const now = () => new Date().toISOString()
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T

const status = (p: Paper): PaperRef['status'] => (p.deleted_at ? 'trashed' : p.archived_at ? 'archived' : p.version_number > 0 ? 'canonical' : 'draft')
const refOf = (p: Paper): PaperRef => ({ id: p.id, workspace_id: p.workspace_id, title: p.title, parent_id: p.parent_id, visibility: p.visibility, status: status(p), version_number: p.version_number, created_by: p.created_by })
const sees = (p: Paper) => (p.visibility === 'private' || p.deleted_at ? p.created_by.user_id === ME.id : true)

function getPaper(ws: string, id: string): PaperData {
  const d = papers.get(id)
  if (!d || d.paper.workspace_id !== ws || !sees(d.paper)) throw new HttpError(404, 'not_found', 'paper not found')
  return d
}

function publish(paperId: string, ev: Omit<PaperEvent, 'workspace_id' | 'paper_id' | 'at'>) {
  const d = papers.get(paperId)
  if (!d) return
  const full = { ...ev, workspace_id: d.paper.workspace_id, paper_id: paperId, at: now() }
  for (const s of sockets) if (s.paperId === paperId) s.deliver({ type: 'event', event: clone(full) })
}

const byOrder = (a: { order: string; id: string }, b: { order: string; id: string }) => (a.order < b.order ? -1 : a.order > b.order ? 1 : a.id < b.id ? -1 : 1)

function childrenOf(blocks: Map<string, Block>, parent: string): Block[] {
  return [...blocks.values()].filter((b) => (b.parent_id ?? '') === parent).sort(byOrder)
}

function normalizeProps(type: BlockType, p: BlockProps): BlockProps {
  switch (type) {
    case 'heading': return { level: p.level || 1 }
    case 'bulleted_list_item': case 'numbered_list_item': return { indent: p.indent }
    case 'todo': return { indent: p.indent, checked: p.checked }
    case 'code': return { language: p.language }
    case 'callout': return { icon: p.icon }
    case 'image': return p.attachment_id ? { attachment_id: p.attachment_id } : { url: p.url }
    case 'table': return { rows: p.rows, cols: p.cols, header_row: p.header_row }
    case 'table_cell': return { row_id: p.row_id, col_id: p.col_id }
    case 'paper': return { paper_id: p.paper_id }
    case 'html': case 'svg': case 'mermaid': return { height: p.height }
    default: return {}
  }
}

const MARKUP = new Set<BlockType>(['paragraph', 'heading', 'quote', 'bulleted_list_item', 'numbered_list_item', 'todo', 'callout', 'table_cell', 'image'])
const sameContents = (x: Block, y: Block) => x.type === y.type && x.content === y.content && JSON.stringify(x.props) === JSON.stringify(y.props)

function lineKey(lines: TableLine[], after: string | undefined, self = ''): string {
  const sorted = lines.filter((l) => l.id !== self).sort(byOrder)
  if (after === undefined) return keyBetween(sorted[sorted.length - 1]?.order ?? '', '')
  if (after === '') return keyBetween('', sorted[0]?.order ?? '')
  const i = sorted.findIndex((l) => l.id === after)
  if (i < 0) throw new HttpError(400, 'invalid_argument', 'anchor line not found')
  const lo = sorted[i]?.order ?? ''
  const hi = sorted[i + 1]?.order ?? ''
  return keyBetween(lo, hi === lo ? '' : hi)
}

/** Applies a batch the way pkg/usecase does (see docs/model.html). */
export function apply(d: PaperData, actor: Actor, ops: Op[], force = false): BatchResult {
  const staged = new Map([...d.blocks].map(([k, v]) => [k, clone(v)]))
  const conflicts: Conflict[] = []
  const created = new Set<string>()
  const touched = new Set<string>()
  const refs: Record<string, string> = {}
  const newPapers: Paper[] = []
  let title = d.paper.title
  const at = now()
  const resolve = (a: string) => (a.startsWith('ref:') ? refs[a.slice(4)] ?? (() => { throw new HttpError(400, 'invalid_argument', 'unknown ref') })() : a)
  const mk = (type: BlockType, content: string, props: BlockProps, order: string, parent?: string): Block => ({
    id: newId(), workspace_id: d.paper.workspace_id, paper_id: d.paper.id, parent_id: parent, order, type,
    content: MARKUP.has(type) ? normalizeMarkup(content) : content, props: normalizeProps(type, props), rev: 1,
    created_by: actor, created_at: at, updated_by: actor, updated_at: at,
  })
  const put = (b: Block) => { staged.set(b.id, b); touched.add(b.id) }
  const drop = (id: string) => { staged.delete(id); touched.add(id) }
  const orderAfter = (after: string | undefined, self = '') => {
    const sib = childrenOf(staged, '').filter((b) => b.id !== self)
    if (after === undefined) return keyBetween(sib[sib.length - 1]?.order ?? '', '')
    const target = resolve(after)
    if (target === '') return keyBetween('', sib[0]?.order ?? '')
    const i = sib.findIndex((b) => b.id === target)
    if (i < 0) throw new HttpError(400, 'invalid_argument', 'anchor block not found')
    return keyBetween(sib[i]?.order ?? '', sib[i + 1]?.order ?? '')
  }
  const bump = (b: Block) => {
    if (!created.has(b.id)) b.rev = (d.blocks.get(b.id)?.rev ?? b.rev) + 1
    b.updated_by = actor
    b.updated_at = at
  }
  // checkBase returns false after recording a conflict.
  const checkBase = (id: string, base: number | undefined): boolean => {
    if (created.has(id)) return true
    const orig = d.blocks.get(id)
    if (!orig) return true
    if (!base || base > orig.rev) throw new HttpError(400, 'invalid_argument', 'base_rev is missing or newer than the block')
    if (base === orig.rev || force) return true
    conflicts.push({ block_id: id, base_rev: base, current: clone(orig) })
    return false
  }
  const cells = (table: Block, row: string, col: string, content = '') => {
    const c = mk('table_cell', content, { row_id: row, col_id: col }, 'V', table.id)
    put(c)
    created.add(c.id)
  }

  for (const op of ops) {
    switch (op.op) {
      case 'insert': {
        const type = op.type ?? 'paragraph'
        const props: BlockProps = { ...op.props }
        let child: Paper | undefined
        if (type === 'paper') {
          child = { id: newId(), workspace_id: d.paper.workspace_id, title: (op.title ?? '').trim(), parent_id: d.paper.id, visibility: 'public', version_number: 0, created_by: actor, created_at: at, updated_by: actor, updated_at: at }
          props.paper_id = child.id
        }
        let grid: string[][] = []
        if (type === 'table') {
          grid = op.cells?.length ? op.cells : Array.from({ length: op.rows || 2 }, () => Array.from({ length: op.cols || 2 }, () => ''))
          let k = ''
          props.cols = Array.from({ length: Math.max(...grid.map((r) => r.length)) }, () => ({ id: newId(), order: (k = keyBetween(k, '')) }))
          k = ''
          props.rows = grid.map(() => ({ id: newId(), order: (k = keyBetween(k, '')) }))
        }
        const b = mk(type, op.content ?? '', props, orderAfter(op.after))
        if (op.ref) refs[op.ref] = b.id
        put(b)
        created.add(b.id)
        if (child) newPapers.push(child)
        props.rows?.forEach((r, ri) => props.cols?.forEach((c, ci) => cells(b, r.id, c.id, grid[ri]?.[ci] ?? '')))
        break
      }
      case 'update': {
        const id = op.block_id ?? ''
        const before = staged.get(id)
        if (!before) {
          if (touched.has(id)) throw new HttpError(400, 'invalid_argument', 'block was deleted earlier in this batch')
          conflicts.push({ block_id: id, base_rev: op.base_rev ?? 0, current: null })
          break
        }
        if (!checkBase(id, op.base_rev)) break
        const b = clone(before)
        if (op.type && op.type !== b.type) {
          if (b.type === 'table') for (const c of childrenOf(staged, b.id)) drop(c.id)
          b.type = op.type
        }
        if (op.content !== undefined) b.content = MARKUP.has(b.type) ? normalizeMarkup(op.content) : op.content
        if (op.props) b.props = { ...op.props, rows: before.props.rows, cols: before.props.cols, row_id: before.props.row_id, col_id: before.props.col_id }
        b.props = normalizeProps(b.type, b.props)
        if (b.type === 'divider' || b.type === 'table') b.content = ''
        if (sameContents(b, before)) break
        bump(b)
        put(b)
        break
      }
      case 'delete': {
        const b = staged.get(op.block_id ?? '')
        if (!b) break // already deleted by someone else
        if (!checkBase(b.id, op.base_rev)) break
        if (b.type === 'paper') {
          const child = papers.get(b.props.paper_id ?? '')
          if (child && child.paper.created_by.user_id !== actor.user_id) {
            child.paper = { ...child.paper, archived_at: at }
            break
          }
          if (child) child.paper = { ...child.paper, deleted_at: at, deleted_with: child.paper.id }
        }
        for (const c of childrenOf(staged, b.id)) drop(c.id)
        drop(b.id)
        break
      }
      case 'move': {
        const before = staged.get(op.block_id ?? '')
        if (!before) break
        const order = orderAfter(op.after, before.id)
        if (order !== before.order) put({ ...clone(before), order })
        break
      }
      case 'set_title':
        title = (op.title ?? '').trim()
        break
      case 'insert_row': case 'insert_col': case 'delete_row': case 'delete_col': case 'move_row': case 'move_col': {
        const t = staged.get(op.block_id ?? '')
        if (!t || t.type !== 'table') break
        const b = clone(t)
        const isRow = op.op.endsWith('row')
        const lines = (isRow ? b.props.rows : b.props.cols) ?? []
        const others = ((isRow ? b.props.cols : b.props.rows) ?? []).sort(byOrder)
        if (op.op.startsWith('insert')) {
          const line = { id: newId(), order: lineKey(lines, op.after) }
          if (op.ref) refs[op.ref] = line.id
          lines.push(line)
          others.forEach((o, i) => cells(b, isRow ? line.id : o.id, isRow ? o.id : line.id, op.values?.[i] ?? ''))
        } else if (op.op.startsWith('delete')) {
          const i = lines.findIndex((l) => l.id === op.line_id)
          if (i < 0 || lines.length === 1) break
          lines.splice(i, 1)
          for (const c of childrenOf(staged, b.id)) if ((isRow ? c.props.row_id : c.props.col_id) === op.line_id) drop(c.id)
        } else {
          const l = lines.find((x) => x.id === op.line_id)
          if (!l) break
          l.order = lineKey(lines, op.after, l.id)
        }
        lines.sort(byOrder)
        bump(b)
        put(b)
        break
      }
      default:
        throw new HttpError(400, 'invalid_argument', 'this op is not available in the demo')
    }
  }
  if (conflicts.length > 0) throw new HttpError(409, 'conflict', `${conflicts.length} block(s) changed since the base revision`, conflicts)
  const blocks: Block[] = []
  const deleted: string[] = []
  for (const id of touched) {
    const b = staged.get(id)
    if (b) blocks.push(b)
    else if (d.blocks.has(id)) deleted.push(id)
  }
  d.blocks = staged
  for (const p of newPapers) papers.set(p.id, { paper: p, blocks: new Map(), versions: [] })
  if (title !== d.paper.title || blocks.length || deleted.length) d.paper = { ...d.paper, title, updated_by: actor, updated_at: at }
  for (const b of blocks) publish(d.paper.id, { type: 'block_put', block: clone(b) })
  for (const id of deleted) publish(d.paper.id, { type: 'block_deleted', block_id: id })
  publish(d.paper.id, { type: 'paper_updated', paper: clone(d.paper) })
  return { paper: d.paper, blocks, deleted, refs }
}

const snapshot = (d: PaperData) => [...d.blocks.values()].sort(byOrder).map(clone)
const signature = (title: string, bs: Block[]) => JSON.stringify([title, bs.map((b) => [b.id, b.rev, b.parent_id ?? '', b.order])])

function saveVersion(d: PaperData, actor: Actor, note: string): VersionSummary | null {
  const latest = d.versions.at(-1)
  const blocks = snapshot(d)
  if (latest && signature(latest.title, latest.blocks) === signature(d.paper.title, blocks)) return null
  const v: Version = { number: d.versions.length + 1, title: d.paper.title, note, created_by: actor, created_at: now(), block_count: blocks.length, blocks }
  d.versions.push(v)
  d.paper = { ...d.paper, version_number: v.number }
  publish(d.paper.id, { type: 'version_saved', version: v.number })
  publish(d.paper.id, { type: 'paper_updated', paper: clone(d.paper) })
  const { blocks: _b, ...summary } = v
  void _b
  return summary
}

function diff(a: { title: string; blocks: Block[] }, b: { title: string; blocks: Block[] }, from: number, to: number): Diff {
  const before = new Map(a.blocks.map((x) => [x.id, x]))
  const after = new Map(b.blocks.map((x) => [x.id, x]))
  const entries: Diff['entries'] = []
  for (const x of a.blocks) {
    const y = after.get(x.id)
    if (!y) entries.push({ block_id: x.id, kind: 'removed', before: x })
    else if (!sameContents(x, y)) entries.push({ block_id: x.id, kind: 'changed', before: x, after: y })
    else if (x.order !== y.order) entries.push({ block_id: x.id, kind: 'moved', after: y })
  }
  for (const y of b.blocks) if (!before.has(y.id)) entries.push({ block_id: y.id, kind: 'added', after: y })
  return { from, to, title_before: a.title, title_after: b.title, entries }
}

function side(d: PaperData, at: string): { title: string; blocks: Block[]; n: number } {
  if (at === '' || at === 'current') return { title: d.paper.title, blocks: snapshot(d), n: 0 }
  const n = at === 'latest' ? d.versions.length : Number(at)
  const v = d.versions[n - 1]
  if (!v) throw new HttpError(404, 'not_found', 'version not found')
  return { title: v.title, blocks: v.blocks, n }
}

function kidsOf(d: PaperData): PaperRef[] {
  return [...papers.values()].filter((x) => x.paper.parent_id === d.paper.id && !x.paper.deleted_at && sees(x.paper)).map((x) => refOf(x.paper))
}

function childRefs(blocks: Block[]): Record<string, PaperRef> {
  const out: Record<string, PaperRef> = {}
  for (const b of blocks) {
    const p = b.type === 'paper' ? papers.get(b.props.paper_id ?? '')?.paper : undefined
    if (p && sees(p) && !p.deleted_at) out[p.id] = refOf(p)
  }
  return out
}

// ---------------------------------------------------------------- HTTP

export function handle(method: string, url: URL, body: unknown): { status: number; json?: unknown } {
  const p = url.pathname
  const b = (body ?? {}) as Record<string, unknown>
  let m: RegExpMatchArray | null
  try {
    if (p === '/api/auth/me') return { status: 200, json: { user: ME, no_authn: true, demo: true } }
    if (p === '/api/auth/logout') return { status: 200, json: { ok: true } }
    if (p === '/api/v1/tokens' && method === 'GET') return { status: 200, json: { tokens: [{ id: 't-claude', name: 'Claude', kind: 'agent', agent_name: 'Claude', scope: 'write', created_at: now(), last_used_at: now() }] } }
    if (p === '/api/v1/tokens' && method === 'POST') return { status: 201, json: { token: { id: newId(), name: b.name, kind: b.kind, scope: b.scope, created_at: now() }, secret: 'pap_demo-token.this-is-not-a-real-secret' } }
    if (p.startsWith('/api/v1/tokens/')) return { status: 204 }
    if (p === '/api/v1/workspaces') return { status: 200, json: { workspaces: [...workspaces.values()], groups: [{ name: 'Teams', workspaces: ['security'] }] } }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/sidebar$/))) {
      const ws = workspaces.get(m[1] ?? '')
      if (!ws) throw new HttpError(404, 'not_found', 'workspace not found')
      const live = (id: string) => papers.get(id)?.paper
      const list = (ids: string[]) => ids.map(live).filter((x): x is Paper => !!x && sees(x) && !x.deleted_at).map(refOf)
      return { status: 200, json: {
        home: refOf(papers.get(ws.home_paper_id)?.paper as Paper),
        pins: list(pins.get(ws.id) ?? []),
        recent: list(recents.get(ws.id) ?? []),
        private: [...papers.values()].filter((x) => x.paper.workspace_id === ws.id && x.paper.visibility === 'private' && x.paper.created_by.user_id === ME.id && !x.paper.deleted_at).map((x) => refOf(x.paper)),
      } }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/search$/))) {
      const q = (url.searchParams.get('q') ?? '').toLowerCase()
      const hits = [...papers.values()].filter((d) => d.paper.workspace_id === m?.[1] && sees(d.paper) && !d.paper.deleted_at && !d.paper.archived_at).flatMap((d) => {
        const out = []
        if (d.paper.title.toLowerCase().includes(q)) out.push({ paper: refOf(d.paper), text: d.paper.title })
        for (const x of d.blocks.values()) if (x.content.toLowerCase().includes(q)) out.push({ paper: refOf(d.paper), block_id: x.id, text: x.content.slice(0, 140) })
        return out
      })
      return { status: 200, json: { hits } }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/trash$/))) {
      return { status: 200, json: { papers: [...papers.values()].filter((d) => d.paper.workspace_id === m?.[1] && d.paper.deleted_at && d.paper.deleted_with === d.paper.id && d.paper.created_by.user_id === ME.id).map((d) => refOf(d.paper)) } }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/papers$/)) && method === 'POST') {
      const ws = workspaces.get(m[1] ?? '')
      if (!ws) throw new HttpError(404, 'not_found', 'workspace not found')
      if (b.visibility === 'private') {
        const paper: Paper = { id: newId(), workspace_id: ws.id, title: String(b.title ?? ''), visibility: 'private', version_number: 0, created_by: human(ME), created_at: now(), updated_by: human(ME), updated_at: now() }
        papers.set(paper.id, { paper, blocks: new Map(), versions: [] })
        return { status: 201, json: paper }
      }
      const parent = papers.get(String(b.parent || ws.home_paper_id)) ?? papers.get(ws.home_paper_id)
      if (!parent) throw new HttpError(400, 'invalid_argument', 'parent not found')
      const r = apply(parent, human(ME), [{ op: 'insert', type: 'paper', title: String(b.title ?? ''), after: (b.after as string | null) ?? undefined }])
      const child = r.blocks?.find((x) => x.type === 'paper')?.props.paper_id ?? ''
      return { status: 201, json: papers.get(child)?.paper }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/papers\/([^/]+)(\/.*)?$/))) {
      const ws = m[1] ?? ''
      const d = getPaper(ws, m[2] ?? '')
      const rest = m[3] ?? ''
      const ok = (json: unknown) => ({ status: 200, json })
      if (rest === '' || rest === '/') {
        const path: PaperRef[] = []
        for (let at = d.paper.parent_id; at;) {
          const q = papers.get(at)?.paper
          if (!q) break
          path.unshift(refOf(q))
          at = q.parent_id
        }
        const w = workspaces.get(ws)
        recents.set(ws, [d.paper.id, ...(recents.get(ws) ?? []).filter((x) => x !== d.paper.id)].slice(0, 10))
        return ok({ workspace: w, paper: d.paper, path, is_home: w?.home_paper_id === d.paper.id })
      }
      if (rest === '/blocks') {
        const blocks = [...d.blocks.values()].sort(byOrder)
        return ok({ blocks, next: '', papers: childRefs(blocks) })
      }
      if (rest === '/children') return ok({ papers: kidsOf(d) })
      if (rest === '/ops') return ok(apply(d, human(ME), (b.ops ?? []) as Op[], !!b.force))
      if (rest === '/status') {
        const latest = d.versions.at(-1)
        return ok({ version_number: d.paper.version_number, changed: !latest || signature(latest.title, latest.blocks) !== signature(d.paper.title, snapshot(d)) })
      }
      if (rest === '/versions' && method === 'GET') return ok({ versions: [...d.versions].reverse().map(({ blocks: _x, ...v }) => { void _x; return v }) })
      if (rest === '/versions' && method === 'POST') return ok({ version: saveVersion(d, human(ME), String(b.note ?? '')) })
      const vm = rest.match(/^\/versions\/(\w+)(\/restore)?$/)
      if (vm) {
        const s = side(d, vm[1] ?? '')
        if (!vm[2]) {
          const v = d.versions[s.n - 1] as Version
          const { blocks: _x, ...summary } = v
          void _x
          return ok({ version: summary, paper: { ...d.paper, title: s.title }, blocks: s.blocks })
        }
        d.blocks = new Map(s.blocks.map((x) => [x.id, { ...clone(x), rev: x.rev + 1000 }]))
        d.paper = { ...d.paper, title: s.title }
        publish(d.paper.id, { type: 'paper_updated', paper: clone(d.paper) })
        return ok({ paper: d.paper, blocks: [...d.blocks.values()], deleted: [], refs: {} })
      }
      if (rest === '/diff') {
        const a = side(d, url.searchParams.get('from') ?? 'latest')
        const z = side(d, url.searchParams.get('to') ?? '')
        return ok(diff(a, z, a.n, z.n))
      }
      if (rest === '/archive' || rest === '/unarchive') {
        d.paper = { ...d.paper, archived_at: rest === '/archive' ? now() : undefined }
        return ok({ paper: d.paper, refs: {} })
      }
      if (rest === '/trash') {
        if (d.paper.created_by.user_id !== ME.id) throw new HttpError(403, 'permission_denied', 'only the creator can delete the paper; archive it instead')
        d.paper = { ...d.paper, deleted_at: now(), deleted_with: d.paper.id }
        const parent = papers.get(d.paper.parent_id ?? '')
        const pb = parent && [...parent.blocks.values()].find((x) => x.props.paper_id === d.paper.id)
        if (parent && pb) apply(parent, human(ME), [{ op: 'delete', block_id: pb.id, base_rev: pb.rev }])
        return ok({ paper: d.paper, refs: {} })
      }
      if (rest === '/restore') {
        d.paper = { ...d.paper, deleted_at: undefined, deleted_with: undefined }
        const parent = papers.get(d.paper.parent_id ?? '')
        if (parent && d.paper.visibility === 'public') {
          // put the paper block back without creating another child
          const order = keyBetween(childrenOf(parent.blocks, '').at(-1)?.order ?? '', '')
          const pb: Block = { id: newId(), workspace_id: ws, paper_id: parent.paper.id, order, type: 'paper', content: '', props: { paper_id: d.paper.id }, rev: 1, created_by: human(ME), created_at: now(), updated_by: human(ME), updated_at: now() }
          parent.blocks.set(pb.id, pb)
          publish(parent.paper.id, { type: 'block_put', block: clone(pb) })
        }
        return ok({ paper: d.paper, refs: {} })
      }
      if (rest === '/pin') {
        const list = (pins.get(ws) ?? []).filter((x) => x !== d.paper.id)
        pins.set(ws, method === 'DELETE' ? list : [...list, d.paper.id])
        return { status: 204 }
      }
      if (rest === '/presence') return { status: 204 }
      if (rest === '/visibility' || rest === '/move' || rest.startsWith('/attachments')) {
        throw new HttpError(400, 'invalid_argument', 'not available in the demo')
      }
    }
    throw new HttpError(404, 'not_found', `no demo route for ${method} ${p}`)
  } catch (e) {
    if (e instanceof HttpError) return { status: e.status, json: { error: { code: e.code, message: e.message, conflicts: e.conflicts } } }
    return { status: 500, json: { error: { code: 'internal', message: String(e) } } }
  }
}

// ---------------------------------------------------------------- live

export function connect(paperId: string, deliver: (msg: object) => void): { send: (raw: string) => void; close: () => void } {
  const s: Socket = { paperId, deliver }
  sockets.add(s)
  deliver({ type: 'hello' })
  const sim = startSimulation(paperId)
  return {
    send: () => deliver({ type: 'error', error: 'the live feed takes no messages' }),
    close: () => {
      sim()
      sockets.delete(s)
    },
  }
}

// ---------------------------------------------------------------- collaborators

const ALICE_LINES = [' Ping #sec-oncall when you start.', ' Keep the incident channel open until the postmortem.', ' Check CI logs too; keys leak there most.']
const CLAUDE_NOTES = [
  'I checked the last three incidents: all keys were found by the secret scanner within 20 minutes.',
  'Suggestion: add the vault rotation command to step 1 so on-call can copy it.',
  'Two steps here have no owner. I left them unassigned instead of guessing.',
]

const sleep = (ms: number) => new Promise((r) => window.setTimeout(r, ms))

// Alice types into a paragraph word by word, telling others she is there.
// Each save is based on the revision she last saw; if someone else changed
// the block in between, the save conflicts and she stops.
async function aliceEdits(d: PaperData) {
  const paragraphs = childrenOf(d.blocks, '').filter((b) => b.type === 'paragraph')
  const target = paragraphs[Math.floor(Math.random() * paragraphs.length)]
  if (!target) return
  const until = () => new Date(Date.now() + 45000).toISOString()
  publish(d.paper.id, { type: 'presence', presence: { block_id: target.id, actor: human(ALICE), until: until() } })
  await sleep(1500)
  const line = ALICE_LINES[Math.floor(Math.random() * ALICE_LINES.length)] ?? ''
  let seen = d.blocks.get(target.id)
  for (const w of line.split(/(?= )/)) {
    await sleep(700)
    if (!seen) break
    try {
      const r = apply(d, human(ALICE), [{ op: 'update', block_id: target.id, content: seen.content + w.replace(/#/g, '\\#'), base_rev: seen.rev }])
      seen = r.blocks?.find((b) => b.id === target.id) ?? seen
    } catch {
      break
    }
  }
  publish(d.paper.id, { type: 'presence', presence: { block_id: target.id, actor: human(ALICE), until: until(), leaving: true } })
}

function claudeWrites(d: PaperData) {
  const agent = claudeFor(ALICE)
  const todos = childrenOf(d.blocks, '').filter((b) => b.type === 'todo' && !b.props.checked)
  try {
    if (Math.random() < 0.35 && todos[0]) {
      apply(d, agent, [{ op: 'update', block_id: todos[0].id, props: { ...todos[0].props, checked: true }, base_rev: todos[0].rev }])
      return
    }
    const top = childrenOf(d.blocks, '')
    const note = CLAUDE_NOTES[Math.floor(Math.random() * CLAUDE_NOTES.length)] ?? ''
    apply(d, agent, [{ op: 'insert', type: 'callout', content: note, props: { icon: '🤖' }, after: top[Math.min(2, top.length - 1)]?.id }])
    if (Math.random() < 0.5) saveVersion(d, agent, 'Added a review note')
  } catch {
    // Someone changed the block first; Claude re-reads on its next turn.
  }
}

const running = new Map<string, number>()

function startSimulation(paperId: string): () => void {
  running.set(paperId, (running.get(paperId) ?? 0) + 1)
  if ((running.get(paperId) ?? 0) > 1) return () => running.set(paperId, (running.get(paperId) ?? 1) - 1)
  let stopped = false
  let turn = 0
  void (async () => {
    await sleep(3500)
    while (!stopped) {
      const d = papers.get(paperId)
      if (!d || d.paper.visibility === 'private') break
      if (turn % 2 === 0) await aliceEdits(d)
      else claudeWrites(d)
      turn++
      await sleep(9000 + Math.random() * 5000)
    }
  })()
  return () => {
    const n = (running.get(paperId) ?? 1) - 1
    running.set(paperId, n)
    if (n <= 0) stopped = true
  }
}

export { BOB }
