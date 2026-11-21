import type {
  ApiToken, Attachment, BatchResult, BlocksPage, Conflict, Diff, Forward, Me, Op, Paper, PaperRef, PaperStatusInfo,
  PaperView, SearchHit, Sidebar, VersionSummary, VersionView, Visibility, Workspace, WorkspaceGroup,
} from './types'

export class ApiError extends Error {
  status: number
  code: string
  /** Set for 409 "conflict": the ops based on outdated revisions. */
  conflicts: Conflict[]
  /** Set for "moved": where the paper lives now. */
  movedTo?: Forward
  candidates: PaperRef[]

  constructor(status: number, code: string, message: string, extra: { conflicts?: Conflict[]; moved_to?: Forward; candidates?: PaperRef[] } = {}) {
    super(message)
    this.status = status
    this.code = code
    this.conflicts = extra.conflicts ?? []
    this.movedTo = extra.moved_to
    this.candidates = extra.candidates ?? []
  }
}

async function request<T>(method: string, path: string, body?: unknown, raw?: { type: string; data: Blob }): Promise<T> {
  const headers: Record<string, string> = { 'X-Requested-With': 'papers' }
  let payload: BodyInit | undefined
  if (raw) {
    headers['Content-Type'] = raw.type
    payload = raw.data
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    payload = JSON.stringify(body)
  }
  const res = await fetch(path, { method, headers, credentials: 'same-origin', body: payload })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!res.ok) {
    let code = 'error'
    let message = text
    let extra = {}
    try {
      const parsed = JSON.parse(text) as { error?: { code: string; message: string; conflicts?: Conflict[]; moved_to?: Forward; candidates?: PaperRef[] } }
      if (parsed.error) {
        code = parsed.error.code
        message = parsed.error.message
        extra = parsed.error
      }
    } catch {
      // keep the raw body as the message
    }
    throw new ApiError(res.status, code, message, extra)
  }
  const type = res.headers.get('Content-Type') ?? ''
  return (type.includes('application/json') ? JSON.parse(text) : text) as T
}

const enc = encodeURIComponent
const wsPath = (ws: string) => `/api/v1/workspaces/${enc(ws)}`
export const paperPath = (ws: string, paper: string) => `${wsPath(ws)}/papers/${enc(paper)}`

export const api = {
  me: () => request<Me>('GET', '/api/auth/me'),
  logout: () => request<void>('POST', '/api/auth/logout'),

  listWorkspaces: () => request<{ workspaces: Workspace[]; groups: WorkspaceGroup[] }>('GET', '/api/v1/workspaces'),
  sidebar: (ws: string) => request<Sidebar>('GET', `${wsPath(ws)}/sidebar`),
  search: (ws: string, q: string, under = '') =>
    request<{ hits: SearchHit[] }>('GET', `${wsPath(ws)}/search?q=${enc(q)}${under ? `&under=${enc(under)}` : ''}`).then((r) => r.hits),
  trash: (ws: string) => request<{ papers: PaperRef[] }>('GET', `${wsPath(ws)}/trash`).then((r) => r.papers),
  createPaper: (ws: string, input: { title: string; visibility?: Visibility; parent?: string; after?: string }) =>
    request<Paper>('POST', `${wsPath(ws)}/papers`, { title: input.title, visibility: input.visibility ?? 'public', parent: input.parent ?? '', after: input.after ?? null, markdown: '' }),

  getPaper: (ws: string, paper: string) => request<PaperView>('GET', paperPath(ws, paper)),
  blocks: (ws: string, paper: string, after = '', limit = 200) =>
    request<BlocksPage>('GET', `${paperPath(ws, paper)}/blocks?limit=${limit}${after ? `&after=${enc(after)}` : ''}`),
  children: (ws: string, paper: string) => request<{ papers: PaperRef[] }>('GET', `${paperPath(ws, paper)}/children`).then((r) => r.papers),
  ops: (ws: string, paper: string, ops: Op[], force = false) => request<BatchResult>('POST', `${paperPath(ws, paper)}/ops`, { ops, force }),
  status: (ws: string, paper: string) => request<PaperStatusInfo>('GET', `${paperPath(ws, paper)}/status`),
  versions: (ws: string, paper: string, before = 0) =>
    request<{ versions: VersionSummary[] }>('GET', `${paperPath(ws, paper)}/versions?limit=50${before ? `&before=${before}` : ''}`).then((r) => r.versions),
  saveVersion: (ws: string, paper: string, note: string) =>
    request<{ version: VersionSummary | null }>('POST', `${paperPath(ws, paper)}/versions`, { note }).then((r) => r.version),
  version: (ws: string, paper: string, n: number | 'latest') => request<VersionView>('GET', `${paperPath(ws, paper)}/versions/${n}`),
  restoreVersion: (ws: string, paper: string, n: number) => request<BatchResult>('POST', `${paperPath(ws, paper)}/versions/${n}/restore`),
  diff: (ws: string, paper: string, from: string, to: string) =>
    request<Diff>('GET', `${paperPath(ws, paper)}/diff?from=${enc(from)}&to=${enc(to)}`),
  move: (ws: string, paper: string, input: { parent: string; after?: string; workspace?: string }) =>
    request<BatchResult>('POST', `${paperPath(ws, paper)}/move`, { parent: input.parent, after: input.after ?? null, workspace: input.workspace ?? '' }),
  setVisibility: (ws: string, paper: string, visibility: Visibility, parent = '') =>
    request<BatchResult>('PUT', `${paperPath(ws, paper)}/visibility`, { visibility, parent, after: null }),
  archive: (ws: string, paper: string, archived: boolean, subtree: boolean) =>
    request<BatchResult>('POST', `${paperPath(ws, paper)}/${archived ? 'archive' : 'unarchive'}`, { subtree }),
  trashPaper: (ws: string, paper: string) => request<BatchResult>('POST', `${paperPath(ws, paper)}/trash`),
  restorePaper: (ws: string, paper: string) => request<BatchResult>('POST', `${paperPath(ws, paper)}/restore`),
  pin: (ws: string, paper: string) => request<void>('PUT', `${paperPath(ws, paper)}/pin`, { after: null }),
  unpin: (ws: string, paper: string) => request<void>('DELETE', `${paperPath(ws, paper)}/pin`),
  presence: (ws: string, paper: string, blockId: string, leaving: boolean) =>
    request<void>('POST', `${paperPath(ws, paper)}/presence`, { block_id: blockId, leaving }),
  upload: (ws: string, paper: string, file: File) =>
    request<Attachment>('POST', `${paperPath(ws, paper)}/attachments?name=${enc(file.name)}`, undefined, { type: file.type, data: file }),
  attachmentURL: (ws: string, paper: string, id: string) => `${paperPath(ws, paper)}/attachments/${enc(id)}`,
  markdownURL: (ws: string, paper: string) => `${paperPath(ws, paper)}/markdown`,
  liveURL: (ws: string, paper: string) => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${window.location.host}${paperPath(ws, paper)}/live`
  },

  listTokens: () => request<{ tokens: ApiToken[] }>('GET', '/api/v1/tokens').then((r) => r.tokens),
  createToken: (input: { name: string; kind: ApiToken['kind']; scope: ApiToken['scope']; ttl_hours: number }) =>
    request<{ token: ApiToken; secret: string }>('POST', '/api/v1/tokens', { ...input, agent_name: input.kind === 'agent' ? input.name : '', workspaces: [] }),
  revokeToken: (id: string) => request<void>('DELETE', `/api/v1/tokens/${enc(id)}`),
}

/** Loads every block of a paper, a range at a time (printing, export). */
export async function loadAllBlocks(ws: string, paper: string): Promise<BlocksPage> {
  const out: BlocksPage = { blocks: [], next: '', papers: {} }
  let after = ''
  for (;;) {
    const page = await api.blocks(ws, paper, after, 500)
    out.blocks.push(...page.blocks)
    Object.assign(out.papers, page.papers)
    if (!page.next) return out
    after = page.next
  }
}
