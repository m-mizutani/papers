import type { Actor, Block, BlockLock, BlockProps, BlockType, Change, Commit, Op, Paper, PaperEvent, Role, RoleBinding, Workspace } from '../api/types'
import { keyBetween } from './order'
import { ALICE, BOB, ME, claudeFor, human, seed } from './seed'

// An in-browser stand-in for the Go server, used by the demo build. It keeps
// the same commit / lock semantics (last writer wins, soft locks that reject
// writes from other holders unless forced) and simulates two collaborators:
// Alice (a person) and Claude (an AI agent).

const LOCK_TTL = 30000

class HttpError extends Error {
  status: number
  code: string
  lock?: BlockLock
  constructor(status: number, code: string, message: string, lock?: BlockLock) {
    super(message)
    this.status = status
    this.code = code
    this.lock = lock
  }
}

interface PaperData {
  paper: Paper
  blocks: Map<string, Block>
  commits: Commit[]
  locks: Map<string, BlockLock>
}

type Socket = { id: string; paperKey: string; deliver: (msg: object) => void }

const data = seed()
const workspaces = new Map(data.workspaces.map((w) => [w.id, w]))
const papers = new Map<string, PaperData>()
for (const p of data.papers) {
  papers.set(p.paper.id, {
    paper: p.paper,
    blocks: new Map(p.blocks.map((b) => [b.id, b])),
    commits: [{ id: `${p.paper.id}-c1`, seq: 1, actor: p.paper.created_by, message: 'Initial content', changes: p.blocks.map((b) => ({ kind: 'insert', block_id: b.id, after: b })), created_at: p.paper.created_at }],
    locks: new Map(),
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

function activeLock(d: PaperData, blockId: string): BlockLock | undefined {
  const l = d.locks.get(blockId)
  if (l && Date.parse(l.expires_at) <= Date.now()) {
    d.locks.delete(blockId)
    return undefined
  }
  return l
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

export function commit(d: PaperData, actor: Actor, ops: Op[], opts: { message?: string; force?: boolean; holder: string }): Commit | null {
  const staged = new Map([...d.blocks].map(([k, v]) => [k, clone(v)]))
  const view: PaperData = { ...d, blocks: staged }
  const changes: Change[] = []
  let title = d.paper.title
  const at = now()
  const checkLock = (id: string) => {
    const l = activeLock(d, id)
    if (l && l.holder_key !== opts.holder && !opts.force) throw new HttpError(409, 'locked', `block is being edited by ${l.holder.agent_name || l.holder.name}`, l)
  }
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
        checkLock(c.id)
        staged.delete(c.id)
        changes.push({ kind: 'delete', block_id: c.id, before: c })
      } else have.add(`${c.props.row}:${c.props.col}`)
    }
    for (let r = 0; r < rows; r++) for (let c = 0; c < cols; c++) {
      if (have.has(`${r}:${c}`)) continue
      const cell = mk(uid(), 'table_cell', cells?.[r]?.[c] ?? '', { row: r, col: c }, 'V', table.id)
      staged.set(cell.id, cell)
      changes.push({ kind: 'insert', block_id: cell.id, after: clone(cell) })
    }
  }
  const get = (id: string) => {
    const b = staged.get(id)
    if (!b) throw new HttpError(400, 'invalid_argument', 'block does not exist')
    return b
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
        changes.push({ kind: 'insert', block_id: id, after: clone(b) })
        if (b.type === 'table') syncCells(b, op.cells)
        break
      }
      case 'update': {
        const before = get(op.block_id)
        checkLock(before.id)
        const b = clone(before)
        if (op.type && op.type !== b.type) {
          if (b.type === 'table') for (const c of children(view, b.id)) { staged.delete(c.id); changes.push({ kind: 'delete', block_id: c.id, before: c }) }
          b.type = op.type
        }
        if (op.content !== undefined) b.content = op.content
        if (op.props) b.props = b.type === 'table_cell' ? { ...op.props, row: before.props.row, col: before.props.col } : op.props
        b.props = normalize(b.type, b.props)
        if (b.type === 'divider' || b.type === 'table') b.content = ''
        if (b.type === before.type && b.content === before.content && JSON.stringify(b.props) === JSON.stringify(before.props)) break
        b.version = before.version + 1
        b.updated_by = actor
        b.updated_at = at
        staged.set(b.id, b)
        const overwrote = op.base_version && op.base_version < before.version ? before.version : undefined
        changes.push({ kind: 'update', block_id: b.id, before, after: clone(b), overwrote_version: overwrote })
        if (b.type === 'table') syncCells(b)
        break
      }
      case 'delete': {
        const b = get(op.block_id)
        checkLock(b.id)
        for (const c of children(view, b.id)) { staged.delete(c.id); changes.push({ kind: 'delete', block_id: c.id, before: c }) }
        staged.delete(b.id)
        changes.push({ kind: 'delete', block_id: b.id, before: b })
        break
      }
      case 'move': {
        const before = get(op.block_id)
        const b = { ...clone(before), order: orderAfter(view, op.after, before.id), version: before.version + 1, updated_by: actor, updated_at: at }
        staged.set(b.id, b)
        changes.push({ kind: 'move', block_id: b.id, before, after: clone(b) })
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
  if (changes.length === 0) return null
  d.blocks = staged
  d.paper = { ...d.paper, title, revision: d.paper.revision + 1, updated_by: actor, updated_at: at }
  const c: Commit = { id: uid(), seq: d.paper.revision, actor, message: opts.message, changes, created_at: at }
  d.commits.push(c)
  publish(d, { type: 'commit', commit: c })
  return c
}

function lock(d: PaperData, blockId: string, holder: string, actor: Actor): { ok: boolean; lock?: BlockLock } {
  const cur = activeLock(d, blockId)
  if (cur && cur.holder_key !== holder) return { ok: false, lock: cur }
  if (!d.blocks.has(blockId)) return { ok: false }
  const l: BlockLock = {
    workspace_id: d.paper.workspace_id, paper_id: d.paper.id, block_id: blockId, holder_key: holder, holder: actor,
    acquired_at: now(), expires_at: new Date(Date.now() + LOCK_TTL).toISOString(),
  }
  d.locks.set(blockId, l)
  publish(d, { type: 'lock', lock: l })
  return { ok: true, lock: l }
}

function unlock(d: PaperData, blockId: string, holder: string) {
  const cur = d.locks.get(blockId)
  if (!cur || cur.holder_key !== holder) return
  d.locks.delete(blockId)
  publish(d, { type: 'unlock', block_id: blockId, holder_key: holder })
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
        const paper: Paper = { id, workspace_id: ws, title: String(b.title ?? ''), revision: 0, access: { restricted: false, bindings: [] }, created_by: a, created_at: now(), updated_by: a, updated_at: now() }
        papers.set(id, { paper, blocks: new Map(), commits: [], locks: new Map() })
        return { status: 201, json: { paper, blocks: [], locks: [], role: 'owner', lock_ttl_ms: LOCK_TTL } }
      }
      const list = [...papers.values()].filter((d) => d.paper.workspace_id === ws).map((d) => ({ ...d.paper, role: paperRole(d.paper) }))
      list.sort((x, y) => (x.updated_at < y.updated_at ? 1 : -1))
      return { status: 200, json: { papers: list } }
    }
    if ((m = p.match(/^\/api\/v1\/workspaces\/([^/]+)\/papers\/([^/]+)(\/.*)?$/))) {
      const d = getPaper(m[1] ?? '', m[2] ?? '')
      const rest = m[3] ?? ''
      if (rest === '' && method === 'GET') {
        const locks = [...d.locks.keys()].map((k) => activeLock(d, k)).filter(Boolean)
        return { status: 200, json: { paper: d.paper, blocks: [...d.blocks.values()], locks, role: paperRole(d.paper), lock_ttl_ms: LOCK_TTL } }
      }
      if (rest === '' && method === 'DELETE') {
        papers.delete(d.paper.id)
        publish(d, { type: 'paper_deleted' })
        return { status: 204 }
      }
      if (rest === '/access') {
        d.paper = { ...d.paper, access: b as unknown as Paper['access'] }
        publish(d, { type: 'access_changed' })
        return { status: 200, json: d.paper }
      }
      if (rest === '/commits' && method === 'GET') {
        const after = Number(url.searchParams.get('after') ?? 0)
        return { status: 200, json: { commits: d.commits.filter((c) => c.seq > after) } }
      }
      if (rest === '/commits' && method === 'POST') {
        const c = commit(d, human(ME), (b.ops ?? []) as Op[], { message: String(b.message ?? ''), force: !!b.force, holder: String(b.connection_id ?? '') })
        return { status: 200, json: { commit: c, paper: d.paper } }
      }
    }
    throw new HttpError(404, 'not_found', `no demo route for ${method} ${p}`)
  } catch (e) {
    if (e instanceof HttpError) return { status: e.status, json: { error: { code: e.code, message: e.message, lock: e.lock } } }
    return { status: 500, json: { error: { code: 'internal', message: String(e) } } }
  }
}

// ---------------------------------------------------------------- live

export function connect(paperId: string, deliver: (msg: object) => void): { send: (raw: string) => void; close: () => void } {
  const s: Socket = { id: `conn:${uid()}`, paperKey: paperId, deliver }
  sockets.add(s)
  const held = new Set<string>()
  deliver({ type: 'hello', connection_id: s.id, lock_ttl_ms: LOCK_TTL })
  const sim = startSimulation(paperId)
  return {
    send: (raw) => {
      const msg = JSON.parse(raw) as { type: string; block_id?: string }
      const d = papers.get(paperId)
      if (!d) return
      if (msg.type === 'lock' && msg.block_id) {
        const r = lock(d, msg.block_id, s.id, human(ME))
        if (r.ok) held.add(msg.block_id)
        deliver({ type: 'lock_result', block_id: msg.block_id, ok: r.ok, lock: r.lock })
      } else if (msg.type === 'unlock' && msg.block_id) {
        held.delete(msg.block_id)
        unlock(d, msg.block_id, s.id)
      } else if (msg.type === 'heartbeat') {
        for (const id of held) lock(d, id, s.id, human(ME))
      }
    },
    close: () => {
      sim()
      sockets.delete(s)
      const d = papers.get(paperId)
      if (d) for (const id of held) unlock(d, id, s.id)
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

async function aliceEdits(d: PaperData) {
  const free = textBlocks(d).filter((b) => b.type === 'paragraph' && !activeLock(d, b.id))
  const target = free[Math.floor(Math.random() * free.length)]
  if (!target) return
  const holder = 'conn:alice'
  if (!lock(d, target.id, holder, human(ALICE)).ok) return
  await sleep(1500)
  const line = ALICE_LINES[Math.floor(Math.random() * ALICE_LINES.length)] ?? ''
  const words = line.split(/(?= )/)
  let text = d.blocks.get(target.id)?.content ?? ''
  for (const w of words) {
    await sleep(700)
    const cur = d.blocks.get(target.id)
    if (!cur) break
    text = cur.content + w
    try {
      commit(d, human(ALICE), [{ op: 'update', block_id: target.id, content: text, base_version: cur.version }], { holder })
    } catch {
      break
    }
  }
  await sleep(1200)
  unlock(d, target.id, holder)
}

function claudeWrites(d: PaperData) {
  const agent = claudeFor(ALICE)
  const holder = 'token:t-claude'
  const roll = Math.random()
  const todos = children(d, '').filter((b) => b.type === 'todo' && !b.props.checked)
  try {
    if (roll < 0.35 && todos[0]) {
      commit(d, agent, [{ op: 'update', block_id: todos[0].id, props: { ...todos[0].props, checked: true }, base_version: todos[0].version }], { holder, message: 'Mark done after checking the audit log' })
      return
    }
    const top = children(d, '')
    const note = CLAUDE_NOTES[Math.floor(Math.random() * CLAUDE_NOTES.length)] ?? ''
    commit(d, agent, [{ op: 'insert', type: 'callout', content: note, props: { icon: '🤖' }, after: top[Math.min(2, top.length - 1)]?.id }], { holder, message: 'Add a note from review' })
  } catch {
    // A person is editing the target block; Claude backs off, as it should.
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
