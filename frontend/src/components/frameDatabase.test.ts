import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { PaperEvent, PaperView } from '../api/types'
import { DB_SCRIPT, FrameDatabase, MAX_RUNNING, MAX_WAITING, type Backend } from './frameDatabase'
import { SANDBOX_CSP, buildSrcDoc } from './RichFrame'
import { buildPageDoc } from './HtmlPage'

// A port pair that delivers synchronously enough for tests: what the page
// posts lands in `sent`, and `send` plays the frame.
function pair() {
  const sent: unknown[] = []
  let onmessage: ((e: MessageEvent) => void) | null = null
  const port = {
    postMessage: (m: unknown) => { sent.push(m) },
    close: vi.fn(),
    set onmessage(f: ((e: MessageEvent) => void) | null) { onmessage = f },
    get onmessage() { return onmessage },
  } as unknown as MessagePort
  const send = (data: unknown) => onmessage?.({ data } as MessageEvent)
  return { port, sent, send }
}

const flush = () => new Promise((r) => setTimeout(r, 0))

function view(id: string, extra: Partial<PaperView['paper']> = {}): PaperView {
  return { paper: { id, kind: 'blocks', title: id, ...extra }, values: [] } as unknown as PaperView
}

function backend(over: Partial<Backend> = {}): Backend & { lives: { id: string; hello: () => void; event: (e: PaperEvent) => void; close: ReturnType<typeof vi.fn> }[] } {
  const lives: { id: string; hello: () => void; event: (e: PaperEvent) => void; close: ReturnType<typeof vi.fn> }[] = []
  return {
    lives,
    getPaper: vi.fn(async (_ws: string, id: string) => {
      if (id === 'db1' || id === 'db2') return view(id, { kind: 'database', properties: [] })
      if (id === 'i1') return view(id, { database_id: 'db1' })
      if (id === 'i2') return view(id, { database_id: 'db2' })
      if (id === 'i9') return view(id, { database_id: 'db9' })
      throw new ApiError(404, 'not_found', 'not found')
    }) as Backend['getPaper'],
    items: vi.fn(async () => ({ items: [], next_cursor: '' })) as Backend['items'],
    ops: vi.fn(async () => ({ paper: null, blocks: [], deleted: [], refs: {} })) as Backend['ops'],
    loadAllBlocks: vi.fn(async () => ({ blocks: [{ id: 'b1', rev: 3 }], next: '', papers: {} })) as unknown as Backend['loadAllBlocks'],
    live: (_ws, id, h) => {
      const close = vi.fn()
      lives.push({ id, hello: h.onHello, event: h.onEvent, close })
      return { close }
    },
    ...over,
  }
}

const LINKS = [{ id: 'db1', write: true }, { id: 'db2' }]

function connect(b = backend()) {
  const fd = new FrameDatabase('team', LINKS, b)
  const p = pair()
  fd.accept(p.port, true)
  return { fd, b, ...p }
}

async function call(c: ReturnType<typeof connect>, id: number, method: string, params: Record<string, unknown> = {}) {
  c.send({ id, method, params })
  await flush()
  await flush()
  return c.sent.find((m) => (m as { id?: number }).id === id) as { ok: boolean; result?: unknown; error?: { code: string; conflicts?: unknown[] } }
}

describe('documents with databases', () => {
  it('puts papers.db before every script of the document', () => {
    // T6
    const page = buildPageDoc('<!doctype html><html><head><script>mine()</script></head><body></body></html>', true, true)
    expect(page.startsWith('<!doctype html>' + DB_SCRIPT)).toBe(true)
    expect(page.indexOf(DB_SCRIPT)).toBeLessThan(page.indexOf('mine()'))
    const block = buildSrcDoc('html', '<script>mine()</script>', {}, false, true, true)
    expect(block.indexOf(DB_SCRIPT)).toBeLessThan(block.indexOf('mine()'))
    const closed = buildPageDoc('<p>x</p>', false, true)
    expect(closed.indexOf(SANDBOX_CSP)).toBeLessThan(closed.indexOf(DB_SCRIPT))
  })

  it('leaves documents without databases as they were', () => {
    // T7
    expect(buildPageDoc('<h1>x</h1>', true)).toBe('<h1>x</h1>')
    expect(buildSrcDoc('html', '<p>x</p>', {}, false, true)).not.toContain('papersDb')
  })

  it('follows the workspace setting for loads from other hosts, connected or not', () => {
    // T27
    expect(buildPageDoc('<p>x</p>', true, true)).not.toContain(SANDBOX_CSP)
    expect(buildPageDoc('<p>x</p>', false, true)).toContain(SANDBOX_CSP)
    expect(buildPageDoc('<p>x</p>', false, false)).toContain(SANDBOX_CSP)
  })
})

describe('FrameDatabase', () => {
  it('passes items on as the API answers', async () => {
    // T8
    const c = connect()
    const r = await call(c, 1, 'items', { database: 'db2', query: { limit: 10, sort: 'title:asc', after: 'x', filter: [{ property: 'p', op: 'is_empty' }] } })
    expect(r).toEqual({ id: 1, ok: true, result: { items: [], next_cursor: '' } })
    expect(c.b.items).toHaveBeenCalledWith('team', 'db2', { limit: 10, sort: 'title:asc', after: 'x', filter: [{ property: 'p', op: 'is_empty' }] })
  })

  it('lists the connected databases in order, unavailable ones too', async () => {
    const fd = new FrameDatabase('team', [{ id: 'db1', write: true }, { id: 'gone' }], backend())
    const p = pair()
    fd.accept(p.port, true)
    p.send({ id: 1, method: 'databases' })
    await flush(); await flush()
    const r = p.sent[0] as { result: { id: string; write: boolean; available: boolean }[] }
    expect(r.result.map((d) => [d.id, d.write, d.available])).toEqual([['db1', true, true], ['gone', false, false]])
  })

  it('sets values and adds items through the database batch', async () => {
    // T9
    const c = connect()
    await call(c, 1, 'setValues', { database: 'db1', item: 'i1', set: { p1: 'e1' } })
    expect(c.b.ops).toHaveBeenCalledWith('team', 'db1', [{ op: 'set_values', item: 'i1', set: { p1: 'e1' } }])
    await call(c, 2, 'addItem', { database: 'db1', item: { title: 'New', after: 'i1', values: { p1: 'e2' } } })
    expect(c.b.ops).toHaveBeenLastCalledWith('team', 'db1', [{ op: 'insert_item', title: 'New', after: 'i1', set: { p1: 'e2' } }])
  })

  it('reads and writes an item of a connected database, reading its database once', async () => {
    // T10
    const c = connect()
    const r = await call(c, 1, 'item', { item: 'i1' })
    expect(r.ok).toBe(true)
    expect((r.result as { blocks: unknown[] }).blocks).toEqual([{ id: 'b1', rev: 3 }])
    const ops = [{ op: 'update', block_id: 'b1', base_rev: 3, content: 'Shipped' }]
    await call(c, 2, 'writeItem', { item: 'i1', ops, force: true })
    expect(c.b.ops).toHaveBeenCalledWith('team', 'i1', ops, true)
    await call(c, 3, 'writeItem', { item: 'i1', ops })
    expect(c.b.ops).toHaveBeenLastCalledWith('team', 'i1', ops, false)
    // one read, for both the database and the item's view; none for the writes
    expect((c.b.getPaper as ReturnType<typeof vi.fn>).mock.calls.filter((x) => x[1] === 'i1')).toHaveLength(1)
    // a later item read reads the paper again for its current view
    await call(c, 4, 'item', { item: 'i1' })
    expect((c.b.getPaper as ReturnType<typeof vi.fn>).mock.calls.filter((x) => x[1] === 'i1')).toHaveLength(2)
  })

  it('hands a conflict back with the current blocks', async () => {
    // T11
    const conflicts = [{ block_id: 'b1', base_rev: 3, current: { id: 'b1', rev: 4 } }]
    const c = connect(backend({ ops: vi.fn(async () => { throw new ApiError(409, 'conflict', 'changed', { conflicts: conflicts as never }) }) as Backend['ops'] }))
    const r = await call(c, 1, 'writeItem', { item: 'i1', ops: [{ op: 'update', block_id: 'b1', base_rev: 3, content: 'x' }] })
    expect(r.ok).toBe(false)
    expect(r.error).toEqual({ code: 'conflict', message: 'changed', conflicts })
  })

  it('passes on the database feed, a resync after reconnecting, and closes it with the last subscriber', async () => {
    // T12
    const c = connect()
    await call(c, 1, 'subscribe', { database: 'db1' })
    await call(c, 2, 'subscribe', { database: 'db1' })
    expect(c.b.lives).toHaveLength(1)
    const live = c.b.lives[0]!
    live.hello()
    for (const type of ['item_put', 'item_deleted', 'property_value_put', 'property_value_deleted', 'paper_updated', 'block_put'] as const) {
      live.event({ type, workspace_id: 'team', paper_id: 'db1', at: '' })
    }
    live.hello()
    const events = c.sent.filter((m) => (m as { event?: unknown }).event).map((m) => (m as { event: { type: string; database_id: string } }).event)
    expect(events.map((e) => e.type)).toEqual(['item_put', 'item_deleted', 'property_value_put', 'property_value_deleted', 'paper_updated', 'resync'])
    expect(events.every((e) => e.database_id === 'db1')).toBe(true)
    await call(c, 3, 'unsubscribe', { database: 'db1' })
    expect(live.close).toHaveBeenCalled()
  })

  it('refuses what the document was not given, without calling the API', async () => {
    // T15, T16, T17
    const c = connect()
    expect((await call(c, 1, 'items', { database: 'db9' })).error?.code).toBe('not_connected')
    expect((await call(c, 2, 'item', { item: 'i9' })).error?.code).toBe('not_connected')
    expect((await call(c, 3, 'setValues', { database: 'db2', item: 'i2', set: {} })).error?.code).toBe('read_only')
    expect((await call(c, 4, 'addItem', { database: 'db2' })).error?.code).toBe('read_only')
    expect((await call(c, 5, 'writeItem', { item: 'i2', ops: [{ op: 'update' }] })).error?.code).toBe('read_only')
    expect((await call(c, 6, 'writeItem', { item: 'i1', ops: [{ op: 'save_version' }] })).error?.code).toBe('invalid_argument')
    expect((await call(c, 7, 'writeItem', { item: 'i1', ops: [{ op: 'move_paper', parent: 'x' }] })).error?.code).toBe('invalid_argument')
    expect((await call(c, 8, 'writeItem', { item: 'i1', ops: [{ op: 'insert', type: 'paper', title: 'child' }] })).error?.code).toBe('invalid_argument')
    expect((await call(c, 9, 'drop_everything')).error?.code).toBe('invalid_argument')
    expect(c.b.ops).not.toHaveBeenCalled()
    expect(c.b.items).not.toHaveBeenCalled()
  })

  it('writes no html, svg or mermaid block into an item, so a document reaches no other database', async () => {
    const b = backend({ loadAllBlocks: vi.fn(async () => ({ blocks: [{ id: 'b1', rev: 3, type: 'paragraph' }, { id: 'h1', rev: 1, type: 'html' }], next: '', papers: {} })) as unknown as Backend['loadAllBlocks'] })
    const c = connect(b)
    expect((await call(c, 1, 'writeItem', { item: 'i1', ops: [{ op: 'insert', type: 'html', content: '<p>x</p>' }] })).error?.code).toBe('invalid_argument')
    expect((await call(c, 2, 'writeItem', { item: 'i1', ops: [{ op: 'insert', type: 'paragraph', props: { databases: [{ id: 'db2', write: true }] } }] })).error?.code).toBe('invalid_argument')
    expect((await call(c, 3, 'writeItem', { item: 'i1', ops: [{ op: 'update', block_id: 'h1', base_rev: 1, content: '<p>y</p>' }] })).error?.code).toBe('invalid_argument')
    expect(c.b.ops).not.toHaveBeenCalled()
    expect((await call(c, 4, 'writeItem', { item: 'i1', ops: [{ op: 'update', block_id: 'b1', base_rev: 3, content: 'ok' }] })).ok).toBe(true)
  })

  it('answers unavailable when not connected', () => {
    // T19
    const fd = new FrameDatabase('team', LINKS, backend())
    const p = pair()
    fd.accept(p.port, false)
    expect(p.sent).toEqual([{ closed: 'unavailable' }])
    expect(p.port.close).toHaveBeenCalled()
  })

  it('takes only the first port and stops answering once closed', async () => {
    // T14
    const b = backend()
    const fd = new FrameDatabase('team', LINKS, b)
    const first = pair()
    const second = pair()
    fd.accept(first.port, true)
    fd.accept(second.port, true)
    second.send({ id: 1, method: 'items', params: { database: 'db1' } })
    fd.close()
    first.send({ id: 2, method: 'items', params: { database: 'db1' } })
    await flush()
    expect(second.sent).toEqual([])
    expect(first.sent).toEqual([])
    expect(b.items).not.toHaveBeenCalled()
  })

  it('runs four calls at once and queues the rest', async () => {
    // T25, T26
    const resolvers: (() => void)[] = []
    const b = backend({ items: vi.fn(() => new Promise((r) => resolvers.push(() => r({ items: [], next_cursor: '' })))) as Backend['items'] })
    const c = connect(b)
    for (let i = 1; i <= MAX_RUNNING + 1 + MAX_WAITING; i++) c.send({ id: i, method: 'items', params: { database: 'db1' } })
    await flush()
    expect(b.items).toHaveBeenCalledTimes(MAX_RUNNING)
    expect(c.sent).toEqual([{ id: MAX_RUNNING + 1 + MAX_WAITING, ok: false, error: { code: 'busy', message: 'too many calls are waiting' } }])
    resolvers[0]?.()
    await flush(); await flush()
    expect(b.items).toHaveBeenCalledTimes(MAX_RUNNING + 1)
  })
})
