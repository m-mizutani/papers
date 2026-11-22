import { connect, handle } from './server'

// Replaces fetch and WebSocket so the real UI talks to the in-browser demo
// server. Only loaded by the demo build (VITE_DEMO=1).

export function installMock() {
  const realFetch = window.fetch.bind(window)
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url, window.location.href)
    if (!url.pathname.startsWith('/api/')) return realFetch(input, init)
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined
    await new Promise((r) => setTimeout(r, 60))
    const res = handle(init?.method ?? 'GET', url, body)
    if (res.status === 204) return new Response(null, { status: 204 })
    return new Response(JSON.stringify(res.json), { status: res.status, headers: { 'Content-Type': 'application/json' } })
  }

  class MockWebSocket {
    static readonly CONNECTING = 0
    static readonly OPEN = 1
    static readonly CLOSING = 2
    static readonly CLOSED = 3
    readyState = 0
    onmessage: ((e: { data: string }) => void) | null = null
    onclose: (() => void) | null = null
    onopen: (() => void) | null = null
    private conn?: ReturnType<typeof connect>

    constructor(url: string) {
      const m = url.match(/\/papers\/([^/]+)\/live$/)
      setTimeout(() => {
        this.readyState = 1
        this.onopen?.()
        this.conn = connect(m?.[1] ?? '', (msg) => setTimeout(() => this.onmessage?.({ data: JSON.stringify(msg) }), 30))
      }, 50)
    }

    send(data: string) {
      this.conn?.send(data)
    }

    close() {
      this.readyState = 3
      this.conn?.close()
    }
  }
  window.WebSocket = MockWebSocket as unknown as typeof WebSocket
}
