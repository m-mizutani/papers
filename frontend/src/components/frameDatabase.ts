import { useLayoutEffect, useRef, type RefObject } from 'react'
import { ApiError, api, loadAllBlocks } from '../api/client'
import { LiveConnection } from '../api/live'
import type { BlockType, DatabaseLink, FilterCondition, Op, OpKind, PaperEvent, PaperView, Value } from '../api/types'

// An HTML document (an HTML paper, or an html block) may name databases of
// its workspace in props.databases. Its sandboxed frame holds no
// credentials: a script put before the document's own defines papers.db,
// which sends each call through a MessagePort to this page. This page
// checks the call against the document's list and runs it through the API
// as the person viewing it (model D24).
//
// The frame makes the MessageChannel and hands this page one port with its
// first message. A port belongs to the document that made it, so after the
// frame navigates elsewhere nothing reaches the new document: this page
// never posts to the frame's window, and it closes the port on the frame's
// second load.

/** Calls that run at once from one frame; more wait in order. */
export const MAX_RUNNING = 4
/** Calls that may wait; beyond this a call fails with busy. */
export const MAX_WAITING = 100

/** The body and title ops writeItem passes on. */
/** Block types a document may not write into an item. */
const RICH_TYPES: ReadonlySet<BlockType> = new Set<BlockType>(['html', 'svg', 'mermaid'])

export const ITEM_OPS: ReadonlySet<OpKind> = new Set<OpKind>([
  'insert', 'update', 'delete', 'move', 'insert_markdown', 'set_title', 'set_icon',
  'insert_row', 'delete_row', 'move_row', 'insert_col', 'delete_col', 'move_col',
])

/** Feed events of a database passed on to the frame. */
const FORWARDED = new Set(['item_put', 'item_deleted', 'property_value_put', 'property_value_deleted', 'paper_updated'])

/**
 * The script that defines papers.db in the frame, before anything of the
 * document. It keeps the port in its closure and exposes only the methods.
 */
export const DB_SCRIPT = `<script>(function(){var c=new MessageChannel(),port=c.port1,seq=0,wait={},subs={},closed='';
function fail(w,e){var x=new Error(e.message||e.code||'failed');x.code=e.code||'internal';if(e.conflicts)x.conflicts=e.conflicts;w.reject(x)}
port.onmessage=function(m){var d=m.data||{};if(d.closed){closed=d.closed;for(var k in wait)fail(wait[k],{code:closed,message:'not connected'});wait={};return}
if(d.event){var l=subs[d.event.database_id];if(l)l.slice().forEach(function(f){try{f(d.event)}catch(x){setTimeout(function(){throw x})}});return}
var w=wait[d.id];if(!w)return;delete wait[d.id];if(d.ok)w.resolve(d.result);else fail(w,d.error||{})};
function call(method,params){return new Promise(function(resolve,reject){if(closed){var x=new Error('not connected');x.code=closed;reject(x);return}var id=++seq;wait[id]={resolve:resolve,reject:reject};port.postMessage({id:id,method:method,params:params||{}})})}
parent.postMessage({papersDb:'hello'},'*',[c.port2]);
var db=Object.freeze({databases:function(){return call('databases')},
items:function(d,q){return call('items',{database:d,query:q||{}})},
setValues:function(d,i,s){return call('setValues',{database:d,item:i,set:s})},
addItem:function(d,i){return call('addItem',{database:d,item:i||{}})},
item:function(i){return call('item',{item:i})},
writeItem:function(i,ops,o){return call('writeItem',{item:i,ops:ops,force:!!(o&&o.force)})},
subscribe:function(d,f){if(typeof f!=='function')throw new TypeError('subscribe needs a function');var l=subs[d]||(subs[d]=[]);l.push(f);if(l.length===1)call('subscribe',{database:d}).catch(function(){});var on=true;
return function(){if(!on)return;on=false;var i=l.indexOf(f);if(i>=0)l.splice(i,1);if(l.length===0&&subs[d]===l){delete subs[d];call('unsubscribe',{database:d}).catch(function(){})}}}});
Object.defineProperty(window,'papers',{value:Object.freeze({db:db}),enumerable:true})})()</script>`

/** The error a call answers with. */
export interface CallError {
  code: string
  message: string
  conflicts?: unknown[]
}

interface Request {
  id: number
  method: string
  params: Record<string, unknown>
}

type Reply = { id: number; ok: true; result: unknown } | { id: number; ok: false; error: CallError }

class Refused extends Error {
  code: string
  constructor(code: string, message: string) {
    super(message)
    this.code = code
  }
}

const isObject = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v)
const str = (v: unknown, what: string): string => {
  if (typeof v !== 'string' || v === '') throw new Refused('invalid_argument', `${what} must be an id`)
  return v
}

/** What the page needs to run a frame's calls; the API by default, a fake in tests. */
export interface Backend {
  getPaper: typeof api.getPaper
  items: typeof api.items
  ops: typeof api.ops
  loadAllBlocks: typeof loadAllBlocks
  live: (ws: string, paper: string, handlers: { onHello: () => void; onEvent: (e: PaperEvent) => void }) => { close: () => void }
}

export const apiBackend: Backend = {
  getPaper: api.getPaper,
  items: api.items,
  ops: api.ops,
  loadAllBlocks,
  live: (ws, paper, h) => new LiveConnection(api.liveURL(ws, paper), { ...h, onStatus: () => {}, onClosing: () => {} }),
}

/**
 * Runs the calls of one frame's document. It is made for one document and
 * one port; close ends it for good.
 */
export class FrameDatabase {
  private port?: MessagePort
  private closed = false
  private running = 0
  private waiting: Request[] = []
  private itemDatabase = new Map<string, string>()
  private lives = new Map<string, { close: () => void; hello: boolean }>()
  private ws: string
  private links: DatabaseLink[]
  private backend: Backend

  constructor(ws: string, links: DatabaseLink[], backend: Backend = apiBackend) {
    this.ws = ws
    this.links = links
    this.backend = backend
  }

  /** Whether a port was taken (by the frame's first document). */
  get taken(): boolean {
    return !!this.port
  }

  /** Takes the frame's port: the first one only. */
  accept(port: MessagePort, connect: boolean) {
    if (this.port || this.closed) return
    this.port = port
    if (!connect) {
      port.postMessage({ closed: 'unavailable' })
      this.close()
      return
    }
    port.onmessage = (e: MessageEvent) => this.receive(e.data)
  }

  close() {
    this.closed = true
    this.port?.close()
    this.waiting = []
    this.lives.forEach((l) => l.close())
    this.lives.clear()
  }

  private receive(data: unknown) {
    if (this.closed || !isObject(data) || typeof data.id !== 'number') return
    const req: Request = { id: data.id, method: String(data.method ?? ''), params: isObject(data.params) ? data.params : {} }
    if (this.running < MAX_RUNNING) {
      void this.run(req)
    } else if (this.waiting.length < MAX_WAITING) {
      this.waiting.push(req)
    } else {
      this.reply({ id: req.id, ok: false, error: { code: 'busy', message: 'too many calls are waiting' } })
    }
  }

  private async run(req: Request) {
    this.running++
    try {
      const result = await this.handle(req.method, req.params)
      this.reply({ id: req.id, ok: true, result: result ?? null })
    } catch (err) {
      this.reply({ id: req.id, ok: false, error: toCallError(err) })
    } finally {
      this.running--
      const next = this.waiting.shift()
      if (next && !this.closed) void this.run(next)
    }
  }

  private reply(r: Reply) {
    if (!this.closed) this.port?.postMessage(r)
  }

  private link(database: unknown): DatabaseLink {
    const id = str(database, 'database')
    const l = this.links.find((x) => x.id === id)
    if (!l) throw new Refused('not_connected', 'this database is not connected to the document')
    return l
  }

  private writable(database: unknown): DatabaseLink {
    const l = this.link(database)
    if (!l.write) throw new Refused('read_only', 'the document only reads this database')
    return l
  }

  /** The connected database an item belongs to; its DatabaseID never changes, so it is read once. */
  /** The link of an item's database, and the item's view when it had to be read for it. */
  private async itemLink(item: unknown): Promise<{ link: DatabaseLink; view?: PaperView }> {
    const id = str(item, 'item')
    let db = this.itemDatabase.get(id)
    let view: PaperView | undefined
    if (db === undefined) {
      view = await this.backend.getPaper(this.ws, id)
      db = view.paper.database_id ?? ''
      this.itemDatabase.set(id, db)
    }
    if (!db) throw new Refused('not_connected', 'this paper is not an item of a connected database')
    return { link: this.link(db), view }
  }

  private async handle(method: string, p: Record<string, unknown>): Promise<unknown> {
    switch (method) {
      case 'databases':
        return Promise.all(this.links.map(async (l) => {
          try {
            const view = await this.backend.getPaper(this.ws, l.id)
            const ok = view.paper.kind === 'database'
            return { id: l.id, write: !!l.write, available: ok, paper: ok ? view.paper : null }
          } catch {
            return { id: l.id, write: !!l.write, available: false, paper: null }
          }
        }))
      case 'items': {
        const l = this.link(p.database)
        const q = isObject(p.query) ? p.query : {}
        return this.backend.items(this.ws, l.id, {
          after: typeof q.after === 'string' ? q.after : undefined,
          limit: typeof q.limit === 'number' ? q.limit : undefined,
          filter: Array.isArray(q.filter) ? (q.filter as FilterCondition[]) : undefined,
          sort: typeof q.sort === 'string' ? q.sort : undefined,
        })
      }
      case 'setValues': {
        const l = this.writable(p.database)
        const item = str(p.item, 'item')
        if (!isObject(p.set)) throw new Refused('invalid_argument', 'set must map property ids to values')
        return this.backend.ops(this.ws, l.id, [{ op: 'set_values', item, set: p.set as Record<string, Value | null> }])
      }
      case 'addItem': {
        const l = this.writable(p.database)
        const it = isObject(p.item) ? p.item : {}
        const op: Op = { op: 'insert_item' }
        if (typeof it.title === 'string') op.title = it.title
        if (typeof it.icon === 'string') op.icon = it.icon
        if (typeof it.after === 'string') op.after = it.after
        if (isObject(it.values)) op.set = it.values as Record<string, Value | null>
        return this.backend.ops(this.ws, l.id, [op])
      }
      case 'item': {
        const { view: read } = await this.itemLink(p.item)
        const id = str(p.item, 'item')
        const [view, page] = await Promise.all([read ?? this.backend.getPaper(this.ws, id), this.backend.loadAllBlocks(this.ws, id)])
        return { paper: view.paper, values: view.values ?? [], blocks: page.blocks }
      }
      case 'writeItem': {
        const { link: l } = await this.itemLink(p.item)
        if (!l.write) throw new Refused('read_only', 'the document only reads this database')
        if (!Array.isArray(p.ops) || p.ops.length === 0) throw new Refused('invalid_argument', 'ops must be a list of ops')
        const ops = p.ops.map((o) => checkItemOp(o))
        if (ops.some((o) => o.op === 'update')) {
          // The type of an updated block is not in the op: read the body.
          const { blocks } = await this.backend.loadAllBlocks(this.ws, str(p.item, 'item'))
          const rich = new Set(blocks.filter((b) => RICH_TYPES.has(b.type)).map((b) => b.id))
          if (ops.some((o) => o.op === 'update' && rich.has(o.block_id ?? ''))) throw new Refused('invalid_argument', 'writeItem does not change html, svg or mermaid blocks')
        }
        return this.backend.ops(this.ws, str(p.item, 'item'), ops, p.force === true)
      }
      case 'subscribe': {
        const l = this.link(p.database)
        if (!this.lives.has(l.id)) {
          const entry = { close: () => {}, hello: false }
          this.lives.set(l.id, entry)
          entry.close = this.backend.live(this.ws, l.id, {
            onHello: () => {
              if (entry.hello) this.event({ type: 'resync', database_id: l.id })
              entry.hello = true
            },
            onEvent: (e) => {
              if (FORWARDED.has(e.type)) this.event({ ...e, database_id: l.id })
            },
          }).close
        }
        return null
      }
      case 'unsubscribe': {
        const l = this.link(p.database)
        this.lives.get(l.id)?.close()
        this.lives.delete(l.id)
        return null
      }
      default:
        throw new Refused('invalid_argument', `unknown method ${method}`)
    }
  }

  private event(e: Record<string, unknown>) {
    if (!this.closed) this.port?.postMessage({ event: e })
  }
}

function checkItemOp(o: unknown): Op {
  if (!isObject(o) || typeof o.op !== 'string' || !ITEM_OPS.has(o.op as OpKind)) {
    throw new Refused('invalid_argument', `writeItem does not take ${isObject(o) ? String(o.op) : 'this op'}`)
  }
  if (o.op === 'insert' && o.type === 'paper') throw new Refused('invalid_argument', 'writeItem does not create child papers')
  // A document must not reach databases it was not given, so it writes no
  // html, svg or mermaid block (whose props connect HTML to databases).
  if (typeof o.type === 'string' && RICH_TYPES.has(o.type as BlockType)) throw new Refused('invalid_argument', 'writeItem does not write html, svg or mermaid blocks')
  if (isObject(o.props) && 'databases' in o.props) throw new Refused('invalid_argument', 'writeItem does not connect databases')
  return o as unknown as Op
}

function toCallError(err: unknown): CallError {
  if (err instanceof Refused) return { code: err.code, message: err.message }
  if (err instanceof ApiError) {
    const e: CallError = { code: err.code || 'internal', message: err.message }
    if (err.conflicts.length > 0) e.conflicts = err.conflicts
    return e
  }
  return { code: 'internal', message: err instanceof Error ? err.message : 'failed' }
}

/**
 * Connects a frame's document to its databases. Call it with the frame's
 * ref and its srcdoc: each new document gets a new connection. connect
 * false answers the document with unavailable (an earlier version, a paper
 * being moved).
 */
export function useFrameDatabase(ref: RefObject<HTMLIFrameElement | null>, ws: string, links: DatabaseLink[] | undefined, connect: boolean, doc: string) {
  const linksRef = useRef(links)
  linksRef.current = links
  const has = !!links && links.length > 0
  // The frame is keyed by its links too: new links are a new frame.
  const linksKey = JSON.stringify(links ?? [])
  useLayoutEffect(() => {
    const frame = ref.current
    if (!has || !frame) return
    const fd = new FrameDatabase(ws, linksRef.current ?? [])
    let loads = 0
    const onLoad = () => {
      loads++
      // A second load is the frame navigating away from the document.
      if (loads > 1) fd.close()
    }
    const onMessage = (e: MessageEvent) => {
      if (e.source !== frame.contentWindow) return
      const d = e.data as { papersDb?: unknown } | null
      const port = e.ports[0]
      if (d?.papersDb !== 'hello' || !port) return
      if (loads > 1 || fd.taken) {
        // A document the frame navigated to (or reloaded) is not connected:
        // its calls fail with unavailable instead of waiting forever.
        port.postMessage({ closed: 'unavailable' })
        port.close()
        return
      }
      fd.accept(port, connect)
    }
    frame.addEventListener('load', onLoad)
    window.addEventListener('message', onMessage)
    return () => {
      frame.removeEventListener('load', onLoad)
      window.removeEventListener('message', onMessage)
      fd.close()
    }
  }, [ref, ws, has, connect, doc, linksKey])
}
