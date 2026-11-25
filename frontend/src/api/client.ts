import type {
  ApiToken, Attachment, BatchResult, BlocksPage, Conflict, Diff, FilterCondition, Forward, ItemsPage, Me, OAuthRequest, OAuthRequestInfo, Op, Paper, PaperListItem, PaperListQuery, PaperRef,
  PaperStatusInfo, PaperView, SearchHit, Sidebar, TrashItem, UserSummary, VersionSummary, VersionView, Visibility, Workspace, WorkspaceGroup,
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

/** Fired when an API call finds the sign-in gone (the session expired). */
export const UNAUTHORIZED = 'papers:unauthorized'

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
  if (res.status === 401 && path.startsWith('/api/v1/')) window.dispatchEvent(new Event(UNAUTHORIZED))
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
  /** People of the workspace to mention: by a part of the name or email, or by id. */
  users: (ws: string, q: { q?: string; ids?: string[] }) =>
    request<{ users: UserSummary[] }>('GET', `${wsPath(ws)}/users?${q.ids ? `ids=${q.ids.map(enc).join(',')}` : `q=${enc(q.q ?? '')}`}`).then((r) => r.users),
  trash: (ws: string) => request<{ papers: TrashItem[] }>('GET', `${wsPath(ws)}/trash`).then((r) => r.papers),
  /** Without a parent a public paper is created unfiled. */
  createPaper: (ws: string, input: { title: string; visibility?: Visibility; parent?: string; after?: string; kind?: 'blocks' | 'database' }) =>
    request<Paper>('POST', `${wsPath(ws)}/papers`, { title: input.title, visibility: input.visibility ?? 'public', parent: input.parent ?? '', after: input.after ?? null, markdown: '', kind: input.kind ?? 'blocks' }),
  listPapers: (ws: string, q: PaperListQuery = {}) => {
    const params = new URLSearchParams()
    Object.entries(q).forEach(([k, v]) => {
      if (v !== undefined && v !== '') params.set(k, String(v))
    })
    return request<{ papers: PaperListItem[]; next_cursor: string }>('GET', `${wsPath(ws)}/papers?${params.toString()}`)
  },

  getPaper: (ws: string, paper: string) => request<PaperView>('GET', paperPath(ws, paper)),
  blocks: (ws: string, paper: string, after = '', limit = 200) =>
    request<BlocksPage>('GET', `${paperPath(ws, paper)}/blocks?limit=${limit}${after ? `&after=${enc(after)}` : ''}`),
  /** A page of a database's items; sort is "{property id|title|created|updated}:{asc|desc}". */
  items: (ws: string, db: string, q: { after?: string; limit?: number; filter?: FilterCondition[]; sort?: string } = {}) => {
    const params = new URLSearchParams()
    if (q.after) params.set('after', q.after)
    if (q.limit) params.set('limit', String(q.limit))
    if (q.filter && q.filter.length > 0) params.set('filter', JSON.stringify(q.filter))
    if (q.sort) params.set('sort', q.sort)
    return request<ItemsPage>('GET', `${paperPath(ws, db)}/items?${params.toString()}`)
  },
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
  /** Takes a paper without children out of the tree (it becomes unfiled). */
  unfile: (ws: string, paper: string) => request<BatchResult>('POST', `${paperPath(ws, paper)}/move`, { unfiled: true }),
  /** Public without a parent makes the paper unfiled. */
  setVisibility: (ws: string, paper: string, visibility: Visibility, parent = '') =>
    request<BatchResult>('PUT', `${paperPath(ws, paper)}/visibility`, { visibility, parent, after: null }),
  archive: (ws: string, paper: string, archived: boolean, subtree: boolean) =>
    request<BatchResult>('POST', `${paperPath(ws, paper)}/${archived ? 'archive' : 'unarchive'}`, { subtree }),
  trashPaper: (ws: string, paper: string) => request<BatchResult>('POST', `${paperPath(ws, paper)}/trash`),
  restorePaper: (ws: string, paper: string) => request<BatchResult>('POST', `${paperPath(ws, paper)}/restore`),
  favorite: (ws: string, paper: string) => request<void>('PUT', `${paperPath(ws, paper)}/favorite`, { after: null }),
  unfavorite: (ws: string, paper: string) => request<void>('DELETE', `${paperPath(ws, paper)}/favorite`),
  presence: (ws: string, paper: string, blockId: string, leaving: boolean) =>
    request<void>('POST', `${paperPath(ws, paper)}/presence`, { block_id: blockId, leaving }),
  upload: (ws: string, paper: string, file: File) =>
    request<Attachment>('POST', `${paperPath(ws, paper)}/attachments?name=${enc(file.name)}`, undefined, { type: file.type, data: file }),
  attachmentURL: (ws: string, paper: string, id: string) => `${paperPath(ws, paper)}/attachments/${enc(id)}`,
  markdownURL: (ws: string, paper: string) => `${paperPath(ws, paper)}/markdown`,
  /** One document of an HTML paper, served sandboxed; documents never change. */
  htmlURL: (ws: string, paper: string, attachment: string) => `${paperPath(ws, paper)}/html/${enc(attachment)}`,
  fetchHTML: (ws: string, paper: string, attachment: string) => request<string>('GET', `${paperPath(ws, paper)}/html/${enc(attachment)}`),
  liveURL: (ws: string, paper: string) => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${proto}//${window.location.host}${paperPath(ws, paper)}/live`
  },

  listTokens: () => request<{ tokens: ApiToken[] }>('GET', '/api/v1/tokens').then((r) => r.tokens),
  createToken: (input: { name: string; kind: ApiToken['kind']; scope: ApiToken['scope']; ttl_hours: number }) =>
    request<{ token: ApiToken; secret: string }>('POST', '/api/v1/tokens', { ...input, agent_name: input.kind === 'agent' ? input.name : '', workspaces: [] }),
  revokeToken: (id: string) => request<void>('DELETE', `/api/v1/tokens/${enc(id)}`),
  /** Validates an OAuth authorization request for the consent screen; errors carry the OAuth error code as the message. */
  checkAuthorize: (req: OAuthRequest) =>
    request<OAuthRequestInfo>('GET', `/api/v1/oauth/authorize?${new URLSearchParams(req as unknown as Record<string, string>).toString()}`),
  /** Answers the consent screen; the browser then goes to redirect_to. */
  decideAuthorize: (req: OAuthRequest, scope: ApiToken['scope'], decision: 'allow' | 'deny') =>
    request<{ redirect_to: string }>('POST', '/api/v1/oauth/authorize', {
      client_id: req.client_id, redirect_uri: req.redirect_uri, response_type: req.response_type,
      code_challenge: req.code_challenge, code_challenge_method: req.code_challenge_method,
      state: req.state, resource: req.resource, scope, decision,
    }),
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
