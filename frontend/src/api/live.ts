import type { PaperEvent } from './types'

export interface LiveHandlers {
  /** The feed is live (again): reload the paper, events may have been missed. */
  onHello: () => void
  onEvent: (event: PaperEvent) => void
  onStatus: (status: 'connecting' | 'open' | 'closed') => void
  onClosing: (reason: string) => void
}

interface ServerMsg {
  type: string
  event?: PaperEvent
  reason?: string
}

// 'resync' is not final: the client reconnects and reloads.
const FINAL_REASONS = new Set(['access revoked', 'paper deleted'])

/**
 * The change feed of one paper. It only receives: writes go through the REST
 * API. It reconnects with backoff, since the server closes every connection
 * at the platform request timeout.
 */
export class LiveConnection {
  private ws?: WebSocket
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
      this.handlers.onStatus('closed')
      if (this.stopped) return
      const delay = Math.min(30000, 500 * 2 ** this.retry)
      this.retry++
      window.setTimeout(() => this.connect(), delay)
    }
  }

  private receive(msg: ServerMsg) {
    switch (msg.type) {
      case 'hello':
        this.retry = 0
        this.handlers.onStatus('open')
        this.handlers.onHello()
        break
      case 'event':
        if (msg.event) this.handlers.onEvent(msg.event)
        break
      case 'closing':
        if (FINAL_REASONS.has(msg.reason ?? '')) this.stopped = true
        this.handlers.onClosing(msg.reason ?? '')
        break
    }
  }

  close() {
    this.stopped = true
    this.ws?.close()
  }
}
