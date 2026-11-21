import type { Actor, Block, BlockProps, BlockType, Change, Commit, Conflict, Op, Paper, PaperEvent, Role, RoleBinding, Workspace } from '../api/types'
import { keyBetween } from './order'
import { ALICE, BOB, ME, claudeFor, human, seed } from './seed'

// An in-browser stand-in for the Go server, used by the demo build. It keeps
// the same commit semantics (an op based on an older version of a block
// whose contents changed since is a conflict unless forced; moves never
// conflict) and simulates two collaborators: Alice (a person) and Claude
// (an AI agent).

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

interface PaperData {
  paper: Paper
  blocks: Map<string, Block>
  versions: Map<string, Block[]>
  commits: Commit[]
}

type Socket = { id: string; paperKey: string; deliver: (msg: object) => void }

const data = seed()
const workspaces = new Map(data.workspaces.map((w) => [w.id, w]))
const papers = new Map<string, PaperData>()
for (const p of data.papers) {
  papers.set(p.paper.id, {
    paper: p.paper,
    blocks: new Map(p.blocks.map((b) => [b.id, b])),
    versions: new Map(p.blocks.map((b) => [b.id, [b]])),
    commits: [{ id: `${p.paper.id}-c1`, actor: p.paper.created_by, message: 'Initial content', changes: p.blocks.map((b) => ({ kind: 'insert', block_id: b.id, to_version: 1 })), created_at: p.paper.created_at }],
  })
}
const sockets = new Set<Socket>()
let seq = 0
const uid = () => `${Date.now().toString(36)}-${(seq++).toString(36)}-${Math.random().toString(36).slice(2, 8)}`
const now = () => new Date().toISOString()
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T

function roleIn(bs: RoleBinding[], userId: string): Role {
  const rank = { '': 0, viewer: 1, editor: 2, owner: 3 }
  return bs.filter((b) => b.subject.kind === 'authenticated' || b.subject.id === userId).reduce<Role>((r, b) => (rank[b.role] > rank[r] ? b.role : r), '')
}

function paperRole(p: Paper): Role {
  const ws = workspaces.get(p.workspace_id)
  const wsRole = ws ? roleIn(ws.bindings, ME.id) : ''
  if (wsRole === 'owner' || p.created_by.user_id === ME.id) return 'owner'
  return p.access.restricted ? roleIn(p.access.bindings, ME.id) : wsRole
}

function getPaper(ws: string, id: string): PaperData {
  const d = papers.get(id)
  if (!d || d.paper.workspace_id !== ws) throw new HttpError(404, 'not_found', 'paper not found')
  return d
}

function publish(d: PaperData, ev: Omit<PaperEvent, 'workspace_id' | 'paper_id'>) {
  const full = { ...ev, workspace_id: d.paper.workspace_id, paper_id: d.paper.id }
  for (const s of sockets) if (s.paperKey === d.paper.id) s.deliver({ type: 'event', event: clone(full) })
}

function children(d: PaperData, parent: string | undefined): Block[] {
  return [...d.blocks.values()].filter((b) => (b.parent_id ?? '') === (parent ?? '')).sort((a, b) => (a.order < b.order ? -1 : a.order > b.order ? 1 : a.id < b.id ? -1 : 1))
}

function orderAfter(d: PaperData, after: string | undefined, self = ''): string {
  const sib = children(d, '').filter((b) => b.id !== self)
  if (after === undefined) return keyBetween(sib[sib.length - 1]?.order ?? '', '')
  if (after === '') return keyBetween('', sib[0]?.order ?? '')
  const i = sib.findIndex((b) => b.id === after)
  if (i < 0) throw new HttpError(400, 'invalid_argument', 'anchor block not found')
  return keyBetween(sib[i]?.order ?? '', sib[i + 1]?.order ?? '')
}

function normalize(type: BlockType, p: BlockProps): BlockProps {
  switch (type) {
    case 'heading': return { level: p.level || 1 }
    case 'bulleted_list_item': case 'numbered_list_item': return { indent: p.indent }
    case 'todo': return { indent: p.indent, checked: p.checked }
    case 'code': return { language: p.language }
    case 'callout': return { icon: p.icon }
    case 'image': return { url: p.url }
    case 'table': return { rows: p.rows, cols: p.cols, header_row: p.header_row }
    case 'table_cell': return { row: p.row, col: p.col }
    case 'html': case 'svg': case 'mermaid': return { height: p.height }
    default: return {}
  }
}

interface CommitOutcome {
  commit: Commit | null
  blocks: Block[]
  deleted: string[]
}

const sameContents = (x: Block, y: Block) => x.type === y.type && x.content === y.content && JSON.stringify(x.props) === JSON.stringify(y.props)

export function commit(d: PaperData, actor: Actor, ops: Op[], opts: { message?: string; force?: boolean }): CommitOutcome {
  const staged = new Map([...d.blocks].map(([k, v]) => [k, clone(v)]))
  const view: PaperData = { ...d, blocks: staged }
  const changes: Change[] = []
  const conflicts: Conflict[] = []
  const touched = new Set<string>()
  let title = d.paper.title
  const at = now()
  const mk = (id: string, type: BlockType, content: string, props: BlockProps, order: string, parent?: string): Block => ({
    id, workspace_id: d.paper.workspace_id, paper_id: d.paper.id, parent_id: parent, order, type, content, props: normalize(type, props), version: 1,
    created_by: actor, created_at: at, updated_by: actor, updated_at: at,
  })
  const syncCells = (table: Block, cells?: string[][]) => {
    const rows = table.props.rows ?? 0
    const cols = table.props.cols ?? 0
    const have = new Set<string>()
    for (const c of children(view, table.id)) {
      if ((c.props.row ?? 0) >= rows || (c.props.col ?? 0) >= cols) {
        staged.delete(c.id)
        changes.push({ kind: 'delete', block_id: c.id, from_version: c.version })
      } else have.add(`${c.props.row}:${c.props.col}`)
    }
    for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) {
      if (have.has(`${r}:${c}`)) continue
      const cell = mk(uid(), 'table_cell', cells?.[r]?.[c] ?? '', { row: r, col: c }, 'V', table.id)
      staged.set(cell.id, cell)
      touched.add(cell.id)
      changes.push({ kind: 'insert', block_id: cell.id, to_version: 1 })
    }
  }
  const versionOf = (id: string, v: number) => (d.versions.get(id) ?? []).find((x) => x.version === v)
  const latestOf = (id: string) => (d.versions.get(id) ?? []).at(-1)
  // existing returns the block an op edits, or undefined after recording a
  // conflict for a block someone else deleted.
  const existing = (id: string, base: number | undefined): Block | undefined => {
    const b = staged.get(id)
    if (b) return b
    if (!touched.has(id) && latestOf(id)) {
      conflicts.push({ block_id: id, base_version: base ?? 0, current: null })
      return undefined
    }
    throw new HttpError(400, 'invalid_argument', 'block does not exist')
  }
  // checkBase returns the overwritten version and its writer, or 'skip'
  // after recording a conflict.
  const checkBase = (id: string, base: number | undefined): Pick<Change, 'overwrote_version' | 'overwrote_by'> | 'skip' => {
    if (!base) throw new HttpError(400, 'invalid_argument', 'base_version is required')
    const orig = d.blocks.get(id)
    if (!orig || touched.has(id) && !d.blocks.has(id)) return {}
    if (base > orig.version) throw new HttpError(400, 'invalid_argument', 'base_version is newer than the block')
    if (base === orig.version) return {}
    const snap = versionOf(id, base)
    if (snap && sameContents(snap, orig)) return {}
    if (opts.force) return { overwrote_version: orig.version, overwrote_by: orig.updated_by }
    conflicts.push({ block_id: id, base_version: base, current: clone(orig) })
    return 'skip'
  }

  for (const op of ops) {
    switch (op.op) {
      case 'insert': {
        const id = op.block_id || uid()
        if (staged.has(id)) throw new HttpError(409, 'already_exists', 'block already exists')
        const props = { ...op.props }
        if (op.type === 'table' && !props.rows) props.rows = op.cells?.length ?? 2
        if (op.type === 'table' && !props.cols) props.cols = Math.max(2, ...(op.cells ?? []).map((r) => r.length))
        const b = mk(id, op.type, op.content ?? '', props, orderAfter(view, op.after))
        staged.set(id, b)
        touched.add(id)
        changes.push({ kind: 'insert', block_id: id, to_version: 1 })
        if (b.type === 'table') syncCells(b, op.cells)
        break
      }
      case 'update': {
        const before = existing(op.block_id, op.base_version)
        if (!before) break
        const ow = touched.has(before.id) && !d.blocks.has(before.id) ? {} : checkBase(before.id, op.base_version)
        if (ow === 'skip') break
        const b = clone(before)
        if (op.type && op.type !== b.type) {
          if (b.type === 'table') for (const c of children(view, b.id)) { staged.delete(c.id); changes.push({ kind: 'delete', block_id: c.id, from_version: c.version }) }
          b.type = op.type
        }
        if (op.content !== undefined) b.content = op.content
        if (op.props) b.props = b.type === 'table_cell' ? { ...op.props, row: before.props.row, col: before.props.col } : op.props
        b.props = normalize(b.type, b.props)
        if (b.type === 'divider' || b.type === 'table') b.content = ''
        if (sameContents(b, before)) break
        b.version = before.version + 1
        b.updated_by = actor
        b.updated_at = at
        staged.set(b.id, b)
        touched.add(b.id)
        changes.push({ kind: 'update', block_id: b.id, from_version: before.version, to_version: b.version, ...ow })
        if (b.type === 'table') syncCells(b)
        break
      }
      case 'delete': {
        const b = staged.get(op.block_id)
        if (!b) {
          if (touched.has(op.block_id) || !latestOf(op.block_id)) throw new HttpError(400, 'invalid_argument', 'block does not exist')
          break // already deleted by someone else
        }
        const ow = checkBase(b.id, op.base_version)
        if (ow === 'skip') break
        for (const c of children(view, b.id)) { staged.delete(c.id); changes.push({ kind: 'delete', block_id: c.id, from_version: c.version }) }
        staged.delete(b.id)
        touched.add(b.id)
        changes.push({ kind: 'delete', block_id: b.id, from_version: b.version, ...ow })
        break
      }
      case 'move': {
        const before = staged.get(op.block_id)
        if (!before) break // moving a deleted block is a no-op
        const order = orderAfter(view, op.after, before.id)
        if (order === before.order) break
        const b = { ...clone(before), order, version: before.version + 1, updated_by: actor, updated_at: at }
        staged.set(b.id, b)
        touched.add(b.id)
        changes.push({ kind: 'move', block_id: b.id, from_version: before.version, to_version: b.version })
        break
      }
      case 'restore': {
        const snap = versionOf(op.block_id, op.version)
        if (!snap) throw new HttpError(400, 'invalid_argument', 'block version does not exist')
        const cur = staged.get(op.block_id)
        if (cur) {
          const ow = checkBase(cur.id, op.base_version)
          if (ow === 'skip' || sameContents(snap, cur)) break
          const b = { ...clone(cur), type: snap.type, content: snap.content, props: snap.props, version: cur.version + 1, updated_by: actor, updated_at: at }
          staged.set(b.id, b)
          touched.add(b.id)
          changes.push({ kind: 'restore', block_id: b.id, from_version: cur.version, to_version: b.version, restored_version: op.version, ...ow })
          break
        }
        const last = latestOf(op.block_id)
        if (!last || last.parent_id || snap.type === 'table' || snap.type === 'table_cell') throw new HttpError(400, 'invalid_argument', 'deleted tables and table cells cannot be restored')
        const b = { ...clone(snap), parent_id: undefined, order: last.order, version: last.version + 1, updated_by: actor, updated_at: at }
        staged.set(b.id, b)
        touched.add(b.id)
        changes.push({ kind: 'restore', block_id: b.id, from_version: last.version, to_version: b.version, restored_version: op.version })
        break
      }
      case 'set_title':
        if (op.title.trim() !== title) {
          changes.push({ kind: 'set_title', title_before: title, title_after: op.title.trim() })
          title = op.title.trim()
        }
        break
      default:
        throw new HttpError(400, 'invalid_argument', 'this op is not available in the demo')
    }
  }
  if (conflicts.length > 0) throw new HttpError(409, 'conflict', `${conflicts.length} block(s) changed since the base version`, conflicts)
  if (changes.length === 0) return { commit: null, blocks: [], deleted: [] }
  const put: Block[] = []
  const deleted: string[] = []
  for (const id of new Set(changes.map((c) => c.block_id).filter((x): x is string => !!x))) {
    const b = staged.get(id)
    if (b) {
      put.push(b)
      d.versions.set(id, [...(d.versions.get(id) ?? []), clone(b)])
    } else if (d.blocks.has(id)) deleted.push(id)
  }
  d.blocks = staged
  d.paper = { ...d.paper, title, updated_by: actor, updated_at: at }
  const c: Commit = { id: uid(), actor, message: opts.message, changes, created_at: at }
  d.commits.push(c)
  for (const b of put) publish(d, { type: 'block_put', block: clone(b) })
  for (const id of deleted) publish(d, { type: 'block_deleted', block_id: id })
  publish(d, { type: 'paper_updated', paper: clone(d.paper) })
  return { commit: c, blocks: put, deleted }
}

// ---------------------------------------------------------------- HTTP

const wsJSON = (w: Omit<Workspace, 'role'>): Workspace => ({ ...w, role: roleIn(w.bindings, ME.id) })

export function handle(method: string, url: URL, body: unknown): { status: number; json?: unknown; text?: string } {
  const p = url.pathname
  const b = (body ?? {}) as Record<string, unknown>
  let m: RegExpMatchArray | null
  try {
    if (p === '/api/auth/me') return { status: 200, json: { user: ME, system_admin: true, no_authn: true, demo: true } }
    if (p === '/api/auth/logout') return { status: 200, json: { ok: true } }
    if (p === '/api/v1/users') return { status: 200, json: { users: data.users } }
    if (p === '/api/v1/groups') return { status: 200, json: { groups: [{ id: 'g-oncall', name: 'On-call', members: [ALICE.id, BOB.id] }] } }
    if (p === '/api/v1/tokens' && method === 'GET') return { status: 200, json: { tokens: [{ id: 't-claude', name: 'Claude', kind: 'agent', agent_name: 'Claude', scope: 'write', created_at: now(), last_used_at: now() }] } }
    if (p === '/api/v1/tokens' && method === 'POST') return { status: 201, json: { token: { id: uid(), name: b.name, kind: b.kind, scope: b.scope, created_at: now() }, secret: 'pap_demo-token.this-is-not-a-real-secret' } }
    if ((m = p.match(/^\/api\/v1\/tokens\/(.+)$/))) return { status: 204 }
    if (p === '/api/v1/workspaces' && method === 'GET') return { status: 200, json: { workspaces: [...workspaces.values()].map(wsJSON) } }
    if (p === '/api/v1/workspaces' && method === 'POST') {
      const w = { id: `ws-${uid()}`, name: String(b.name ?? 'Workspace'), emoji: String(b.emoji ?? ''), bindings: [{ subject: { kind: 'user' as const, id: ME.id }, role: 'owner' as const }] }
      workspaces.set(w.id, w)
      return { status: 201, json: wsJSON(w) }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)$/))) {
      const w = workspaces.get(m[1] ?? '')
      if (!w) throw new HttpError(404, 'not_found', 'workspace not found')
      if (method === 'PATCH') Object.assign(w, b)
      return { status: 200, json: wsJSON(w) }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/search$/))) {
      const q = (url.searchParams.get('q') ?? '').toLowerCase()
      const hits = [...papers.values()].filter((d) => d.paper.workspace_id === m?.[1]).flatMap((d) => {
        if (d.paper.title.toLowerCase().includes(q)) return [{ paper: d.paper, snippet: d.paper.title }]
        const hit = [...d.blocks.values()].find((x) => x.content.toLowerCase().includes(q))
        return hit ? [{ paper: d.paper, block_id: hit.id, snippet: hit.content.slice(0, 140) }] : []
      })
      return { status: 200, json: { hits } }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/papers$/))) {
      const ws = m[1] ?? ''
      if (method === 'POST') {
        const id = `p-${uid()}`
        const a = human(ME)
        const paper: Paper = { id, workspace_id: ws, title: String(b.title ?? ''), access: { restricted: false, bindings: [] }, created_by: a, created_at: now(), updated_by: a, updated_at: now() }
        papers.set(id, { paper, blocks: new Map(), versions: new Map(), commits: [] })
        return { status: 201, json: { paper, blocks: [], role: 'owner' } }
      }
      const list = [...papers.values()].filter((d) => d.paper.workspace_id === ws).map((d) => ({ ...d.paper, role: paperRole(d.paper) }))
      list.sort((x, y) => (x.updated_at < y.updated_at ? 1 : -1))
      return { status: 200, json: { papers: list } }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/papers\/([^/]+)(\/.*)?$/))) {
      const d = getPaper(m[1] ?? '', m[2] ?? '')
      const rest = m[3] ?? ''
      if (rest === '' && method === 'GET') {
        return { status: 200, json: { paper: d.paper, blocks: [...d.blocks.values()], role: paperRole(d.paper) } }
      }
      if (rest === '' && method === 'DELETE') {
        papers.delete(d.paper.id)
        publish(d, { type: 'paper_deleted' })
        return { status: 204 }
      }
      if (rest === '/access') {
        d.paper = { ...d.paper, access: b as unknown as Paper['access'] }
        publish(d, { type: 'paper_updated', paper: clone(d.paper) })
        return { status: 200, json: d.paper }
      }
      if (rest === '/commits' && method === 'GET') {
        const newest = [...d.commits].reverse()
        const before = url.searchParams.get('before')
        const start = before ? newest.findIndex((c) => c.id === before) + 1 : 0
        return { status: 200, json: { commits: newest.slice(start, start + 50) } }
      }
      if (rest === '/commits' && method === 'POST') {
        const r = commit(d, human(ME), (b.ops ?? []) as Op[], { message: String(b.message ?? ''), force: !!b.force })
        return { status: 200, json: { ...r, paper: d.paper } }
      }
      const vm = rest.match(/^\/blocks\/([^/]+)\/versions$/)
      if (vm) {
        return { status: 200, json: { versions: [...(d.versions.get(vm[1] ?? '') ?? [])].reverse() } }
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
  const s: Socket = { id: `conn:${uid()}`, paperKey: paperId, deliver }
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

const ALICE_LINES = [
  ' Ping #sec-oncall when you start.',
  ' Keep the incident channel open until the postmortem.',
  ' Screenshots go in the evidence folder.',
  ' Check CI logs too; keys leak there most.',
]
const CLAUDE_NOTES = [
  'I checked the last three incidents: all keys were found by the secret scanner within 20 minutes.',
  'Suggestion: add the vault rotation command to step 1 so on-call can copy it.',
  'Two steps here have no owner. I left them unassigned instead of guessing.',
  'Summary of this paper: rotate first, audit second, write the timeline as you go.',
]

function sleep(ms: number) {
  return new Promise((r) => window.setTimeout(r, ms))
}

function textBlocks(d: PaperData): Block[] {
  return children(d, '').filter((b) => ['paragraph', 'bulleted_list_item', 'numbered_list_item', 'todo'].includes(b.type))
}

// Alice types into a paragraph word by word. Each save is based on the
// version she last saw; if someone else changed the block in between, the
// save conflicts and she stops, as a person would after seeing the dialog.
async function aliceEdits(d: PaperData) {
  const paragraphs = textBlocks(d).filter((b) => b.type === 'paragraph')
  const target = paragraphs[Math.floor(Math.random() * paragraphs.length)]
  if (!target) return
  await sleep(1500)
  const line = ALICE_LINES[Math.floor(Math.random() * ALICE_LINES.length)] ?? ''
  let seen = d.blocks.get(target.id)
  for (const w of line.split(/(?= )/)) {
    await sleep(700)
    if (!seen) break
    try {
      const r = commit(d, human(ALICE), [{ op: 'update', block_id: target.id, content: seen.content + w, base_version: seen.version }], {})
      seen = r.blocks.find((b) => b.id === target.id) ?? seen
    } catch {
      break
    }
  }
}

function claudeWrites(d: PaperData) {
  const agent = claudeFor(ALICE)
  const roll = Math.random()
  const todos = children(d, '').filter((b) => b.type === 'todo' && !b.props.checked)
  try {
    if (roll < 0.35 && todos[0]) {
      commit(d, agent, [{ op: 'update', block_id: todos[0].id, props: { ...todos[0].props, checked: true }, base_version: todos[0].version }], { message: 'Mark done after checking the audit log' })
      return
    }
    const top = children(d, '')
    const note = CLAUDE_NOTES[Math.floor(Math.random() * CLAUDE_NOTES.length)] ?? ''
    commit(d, agent, [{ op: 'insert', type: 'callout', content: note, props: { icon: '🤖' }, after: top[Math.min(2, top.length - 1)]?.id }], { message: 'Add a note from review' })
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
      if (!d) break
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
