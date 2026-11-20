import type { BlockLock, PaperEvent } from './types'

export interface LiveHandlers {
  onHello: (connectionId: string, lockTtlMs: number) => void
  onEvent: (event: PaperEvent) => void
  onLockResult: (blockId: string, ok: boolean, lock?: BlockLock) => void
  onLockLost: (blockId: string, lock?: BlockLock) => void
  onStatus: (status: 'connecting' | 'open' | 'closed') => void
  onClosing: (reason: string) => void
}

interface ServerMsg {
  type: string
  connection_id?: string
  lock_ttl_ms?: number
  block_id?: string
  ok?: boolean
  lock?: BlockLock
  event?: PaperEvent
  reason?: string
}

const FINAL_REASONS = new Set(['access revoked', 'paper deleted'])

/**
 * One realtime connection to a paper. It reconnects with backoff (the
 * server closes every connection at the platform request timeout) and
 * renews this connection's locks on a heartbeat.
 */
export class LiveConnection {
  private ws?: WebSocket
  private heartbeat?: number
  private retry = 0
  private stopped = false
  private url: string
  private handlers: LiveHandlers

  constructor(url: string, handlers: LiveHandlers) {
    this.url = url
    this.handlers = handlers
    this.connect()
  }

  private connect() {
    if (this.stopped) return
    this.handlers.onStatus('connecting')
    const ws = new WebSocket(this.url)
    this.ws = ws
    ws.onmessage = (e) => this.receive(JSON.parse(String(e.data)) as ServerMsg)
    ws.onclose = () => {
      window.clearInterval(this.heartbeat)
      this.handlers.onStatus('closed')
      if (this.stopped) return
      const delay = Math.min(30000, 500 * 2 ** this.retry)
      this.retry++
      window.setTimeout(() => this.connect(), delay)
    }
  }

  private receive(msg: ServerMsg) {
    switch (msg.type) {
      case 'hello': {
        this.retry = 0
        this.handlers.onStatus('open')
        const ttl = msg.lock_ttl_ms ?? 30000
        this.handlers.onHello(msg.connection_id ?? '', ttl)
        window.clearInterval(this.heartbeat)
        this.heartbeat = window.setInterval(() => this.send({ type: 'heartbeat' }), Math.max(1000, ttl / 3))
        break
      }
      case 'event':
        if (msg.event) this.handlers.onEvent(msg.event)
        break
      case 'lock_result':
        this.handlers.onLockResult(msg.block_id ?? '', msg.ok ?? false, msg.lock)
        break
      case 'lock_lost':
        this.handlers.onLockLost(msg.block_id ?? '', msg.lock)
        break
      case 'closing':
        if (FINAL_REASONS.has(msg.reason ?? '')) this.stopped = true
        this.handlers.onClosing(msg.reason ?? '')
        break
    }
  }

  private send(msg: object) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg))
  }

  lock(blockId: string) {
    this.send({ type: 'lock', block_id: blockId })
  }

  unlock(blockId: string) {
    this.send({ type: 'unlock', block_id: blockId })
  }

  close() {
    this.stopped = true
    window.clearInterval(this.heartbeat)
    this.ws?.close()
  }
}
