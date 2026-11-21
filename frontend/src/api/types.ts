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

export type ChangeKind = 'insert' | 'update' | 'delete' | 'move' | 'restore' | 'set_title'

export interface Change {
  kind: ChangeKind
  block_id?: string
  from_version?: number
  to_version?: number
  /** Set when the writer forced its edit over someone else's newer contents. */
  overwrote_version?: number
  overwrote_by?: Actor
  restored_version?: number
  title_before?: string
  title_after?: string
}

export interface Commit {
  id: string
  actor: Actor
  message?: string
  changes: Change[]
  created_at: string
}

export interface PaperView {
  paper: Paper
  blocks: Block[]
  role: Role
}

export type Op =
  | { op: 'insert'; block_id?: string; type: BlockType; content?: string; props?: BlockProps; after?: string; cells?: string[][] }
  | { op: 'update'; block_id: string; type?: BlockType; content?: string; props?: BlockProps; base_version: number }
  | { op: 'delete'; block_id: string; base_version: number }
  | { op: 'restore'; block_id: string; version: number; base_version?: number }
  | { op: 'move'; block_id: string; after?: string }
  | { op: 'set_title'; title: string }
  | { op: 'insert_markdown'; markdown: string; after?: string }

export interface PaperEvent {
  type: 'block_put' | 'block_deleted' | 'paper_updated' | 'paper_deleted'
  workspace_id: string
  paper_id: string
  block?: Block
  paper?: Paper
  block_id?: string
}

/** One op of a rejected commit: the block changed since its base version. */
export interface Conflict {
  block_id: string
  base_version: number
  /** The block as it is now; null when someone deleted it. */
  current: Block | null
}

export interface CommitResult {
  commit: Commit | null
  paper: Paper
  blocks: Block[]
  deleted: string[]
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
  demo?: boolean
}
