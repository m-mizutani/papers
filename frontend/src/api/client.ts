import type { ApiToken, Block, BlockLock, Commit, CommitResult, Group, Me, Op, Paper, PaperAccess, PaperView, RoleBinding, User, Workspace } from './types'

export class ApiError extends Error {
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

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'X-Requested-With': 'papers' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(path, {
    method,
    headers,
    credentials: 'same-origin',
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!res.ok) {
    let code = 'error'
    let message = text
    let lock: BlockLock | undefined
    try {
      const parsed = JSON.parse(text) as { error?: { code: string; message: string; lock?: BlockLock } }
      if (parsed.error) {
        code = parsed.error.code
        message = parsed.error.message
        lock = parsed.error.lock
      }
    } catch {
      // keep the raw body as the message
    }
    throw new ApiError(res.status, code, message, lock)
  }
  const type = res.headers.get('Content-Type') ?? ''
  return (type.includes('application/json') ? JSON.parse(text) : text) as T
}

const wsPath = (ws: string) => `/api/v1/workspaces/${encodeURIComponent(ws)}`
const paperPath = (ws: string, paper: string) => `${wsPath(ws)}/papers/${encodeURIComponent(paper)}`

export const api = {
  me: () => request<Me>('GET', '/api/auth/me'),
  logout: () => request<void>('POST', '/api/auth/logout'),

  listUsers: () => request<{ users: User[] }>('GET', '/api/v1/users').then((r) => r.users),
  listGroups: () => request<{ groups: Group[] }>('GET', '/api/v1/groups').then((r) => r.groups),

  listWorkspaces: () => request<{ workspaces: Workspace[] }>('GET', '/api/v1/workspaces').then((r) => r.workspaces),
  createWorkspace: (name: string, emoji: string) => request<Workspace>('POST', '/api/v1/workspaces', { name, emoji, description: '' }),
  updateWorkspace: (ws: string, patch: { name?: string; emoji?: string; bindings?: RoleBinding[] }) =>
    request<Workspace>('PATCH', wsPath(ws), patch),

  listPapers: (ws: string) => request<{ papers: Paper[] }>('GET', `${wsPath(ws)}/papers`).then((r) => r.papers),
  createPaper: (ws: string, title: string) => request<PaperView>('POST', `${wsPath(ws)}/papers`, { title, markdown: '', restricted: false, connection_id: '' }),
  search: (ws: string, q: string) =>
    request<{ hits: { paper: Paper; block_id?: string; snippet: string }[] }>('GET', `${wsPath(ws)}/search?q=${encodeURIComponent(q)}`).then((r) => r.hits),

  getPaper: (ws: string, paper: string) => request<PaperView>('GET', paperPath(ws, paper)),
  deletePaper: (ws: string, paper: string) => request<void>('DELETE', paperPath(ws, paper)),
  updateAccess: (ws: string, paper: string, access: PaperAccess) => request<Paper>('PUT', `${paperPath(ws, paper)}/access`, access),
  commits: (ws: string, paper: string, before = '') =>
    request<{ commits: Commit[] }>('GET', `${paperPath(ws, paper)}/commits?limit=50${before ? `&before=${encodeURIComponent(before)}` : ''}`).then((r) => r.commits),
  blockVersions: (ws: string, paper: string, block: string) =>
    request<{ versions: Block[] }>('GET', `${paperPath(ws, paper)}/blocks/${encodeURIComponent(block)}/versions?limit=50`).then((r) => r.versions),
  commit: (ws: string, paper: string, ops: Op[], opts: { message?: string; force?: boolean; connectionId?: string } = {}) =>
    request<CommitResult>('POST', `${paperPath(ws, paper)}/commits`, {
      ops,
      message: opts.message ?? '',
      force: opts.force ?? false,
      connection_id: opts.connectionId ?? '',
    }),
  markdownURL: (ws: string, paper: string) => `${paperPath(ws, paper)}/markdown`,
  liveURL: (ws: string, paper: string) => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${window.location.host}${paperPath(ws, paper)}/live`
  },

  listTokens: () => request<{ tokens: ApiToken[] }>('GET', '/api/v1/tokens').then((r) => r.tokens),
  createToken: (input: { name: string; kind: ApiToken['kind']; scope: ApiToken['scope']; ttl_hours: number }) =>
    request<{ token: ApiToken; secret: string }>('POST', '/api/v1/tokens', { ...input, agent_name: input.kind === 'agent' ? input.name : '', workspaces: [] }),
  revokeToken: (id: string) => request<void>('DELETE', `/api/v1/tokens/${encodeURIComponent(id)}`),
}

export type { Block }
