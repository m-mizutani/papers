// Mirrors of the Go JSON shapes (pkg/domain/model, pkg/controller/http).

export type Role = '' | 'viewer' | 'editor' | 'owner'
export type ActorKind = 'human' | 'agent'

export interface Actor {
  kind: ActorKind
  user_id: string
  name: string
  agent_name?: string
  token_id?: string
}

export interface User {
  id: string
  email: string
  name: string
  avatar_url?: string
}

export type SubjectKind = 'user' | 'group' | 'domain' | 'authenticated'

export interface RoleBinding {
  subject: { kind: SubjectKind; id?: string }
  role: Role
}

export interface Workspace {
  id: string
  name: string
  description?: string
  emoji?: string
  bindings: RoleBinding[]
  role: Role
}

export interface PaperAccess {
  restricted: boolean
  bindings: RoleBinding[]
}

export interface Paper {
  id: string
  workspace_id: string
  title: string
  revision: number
  access: PaperAccess
  created_by: Actor
  created_at: string
  updated_by: Actor
  updated_at: string
  role?: Role
}

export type BlockType =
  | 'paragraph'
  | 'heading'
  | 'quote'
  | 'bulleted_list_item'
  | 'numbered_list_item'
  | 'todo'
  | 'callout'
  | 'code'
  | 'divider'
  | 'image'
  | 'table'
  | 'table_cell'
  | 'html'
  | 'svg'
  | 'mermaid'

export interface BlockProps {
  level?: number
  indent?: number
  checked?: boolean
  language?: string
  icon?: string
  url?: string
  rows?: number
  cols?: number
  header_row?: boolean
  row?: number
  col?: number
  height?: number
}

export interface Block {
  id: string
  workspace_id: string
  paper_id: string
  parent_id?: string
  order: string
  type: BlockType
  content: string
  props: BlockProps
  version: number
  created_by: Actor
  created_at: string
  updated_by: Actor
  updated_at: string
}

export interface BlockLock {
  workspace_id: string
  paper_id: string
  block_id: string
  holder_key: string
  holder: Actor
  acquired_at: string
  expires_at: string
}

export type ChangeKind = 'insert' | 'update' | 'delete' | 'move' | 'set_title'

export interface Change {
  kind: ChangeKind
  block_id?: string
  before?: Block
  after?: Block
  overwrote_version?: number
  title_before?: string
  title_after?: string
}

export interface Commit {
  id: string
  seq: number
  actor: Actor
  message?: string
  changes: Change[]
  created_at: string
}

export interface PaperView {
  paper: Paper
  blocks: Block[]
  locks: BlockLock[]
  role: Role
  lock_ttl_ms: number
}

export type Op =
  | { op: 'insert'; block_id?: string; type: BlockType; content?: string; props?: BlockProps; after?: string; cells?: string[][] }
  | { op: 'update'; block_id: string; type?: BlockType; content?: string; props?: BlockProps; base_version?: number }
  | { op: 'delete'; block_id: string }
  | { op: 'move'; block_id: string; after?: string }
  | { op: 'set_title'; title: string }
  | { op: 'insert_markdown'; markdown: string; after?: string }

export interface PaperEvent {
  type: 'commit' | 'lock' | 'unlock' | 'paper_deleted' | 'access_changed'
  workspace_id: string
  paper_id: string
  commit?: Commit
  lock?: BlockLock
  block_id?: string
  holder_key?: string
}

export interface Group {
  id: string
  name: string
  description?: string
  members: string[]
}

export interface ApiToken {
  id: string
  name: string
  kind: 'personal' | 'agent'
  agent_name?: string
  scope: 'read' | 'write'
  created_at: string
  expires_at?: string
  last_used_at?: string
}

export interface Me {
  user: User
  system_admin: boolean
  no_authn: boolean
}
