// Mirrors of the Go JSON shapes (pkg/domain/model, pkg/usecase,
// pkg/controller/http). docs/model.md is the canonical description.

export type ActorKind = 'human' | 'agent' | 'system'

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

export interface Workspace {
  id: string
  name: string
  description?: string
  emoji?: string
  color?: string
  language?: string
  group?: string
  home_paper_id: string
  /** The paper the user opened last in this workspace, if it can still be shown. */
  last_opened?: { paper_id: string; title: string; opened_at: string } | null
}

export interface WorkspaceGroup {
  name: string
  workspaces: string[]
}

export type Visibility = 'public' | 'private'
export type PaperStatus = 'draft' | 'canonical' | 'archived' | 'trashed'

export interface Paper {
  id: string
  workspace_id: string
  title: string
  parent_id?: string
  visibility: Visibility
  version_number: number
  archived_at?: string
  deleted_at?: string
  deleted_with?: string
  /** Set while a move to another workspace works on the paper (read-only). */
  job_id?: string
  created_by: Actor
  created_at: string
  updated_by: Actor
  updated_at: string
}

export interface PaperRef {
  id: string
  workspace_id: string
  title: string
  parent_id?: string
  visibility: Visibility
  status: PaperStatus
  version_number: number
  created_by: Actor
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
  | 'paper'
  | 'html'
  | 'svg'
  | 'mermaid'

export interface TableLine {
  id: string
  order: string
}

export interface BlockProps {
  level?: number
  indent?: number
  checked?: boolean
  language?: string
  icon?: string
  url?: string
  attachment_id?: string
  rows?: TableLine[]
  cols?: TableLine[]
  header_row?: boolean
  row_id?: string
  col_id?: string
  paper_id?: string
  height?: number
  /** Values of an AI-made html / svg block that people may edit in place (data-field). */
  fields?: Record<string, string>
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
  /** Internal revision: +1 on every content change. Sent back as base_rev. */
  rev: number
  created_by: Actor
  created_at: string
  updated_by: Actor
  updated_at: string
}

export type OpKind =
  | 'insert' | 'update' | 'delete' | 'move' | 'set_title' | 'insert_markdown'
  | 'insert_row' | 'delete_row' | 'move_row' | 'insert_col' | 'delete_col' | 'move_col'
  | 'move_paper' | 'set_visibility' | 'save_version' | 'restore_version' | 'archive' | 'unarchive'

export interface Op {
  op: OpKind
  block_id?: string
  ref?: string
  type?: BlockType
  content?: string
  props?: BlockProps
  /** undefined = end, '' = top, an ID or "ref:name" = after it. */
  after?: string
  base_rev?: number
  title?: string
  markdown?: string
  cells?: string[][]
  rows?: number
  cols?: number
  line_id?: string
  values?: string[]
  parent?: string
  workspace?: string
  visibility?: Visibility
  note?: string
  version?: number
  subtree?: boolean
}

export interface Conflict {
  block_id: string
  base_rev: number
  current: Block | null
}

export interface Editing {
  block_id: string
  actor: Actor
}

export interface NotRestored {
  paper_id: string
  reason: 'moved' | 'trashed' | 'missing'
}

export interface Forward {
  workspace_id: string
  paper_id: string
  to_workspace_id: string
  to_paper_id: string
  moved_at: string
}

export interface BatchResult {
  paper: Paper | null
  blocks: Block[] | null
  deleted: string[] | null
  refs: Record<string, string> | null
  editing?: Editing[]
  not_restored?: NotRestored[]
}

export interface PaperView {
  workspace: Workspace
  paper: Paper
  path: PaperRef[]
  is_home: boolean
}

export interface BlocksPage {
  blocks: Block[]
  next: string
  papers: Record<string, PaperRef>
}

export interface VersionSummary {
  number: number
  title: string
  note?: string
  created_by: Actor
  created_at: string
  block_count: number
}

export interface VersionView {
  version: VersionSummary
  paper: Paper
  blocks: Block[]
  /** What restoring the version does with each child paper it has. */
  children?: ChildOutcome[]
}

export interface ChildOutcome {
  paper_id: string
  /** Only for papers the caller may see. */
  title?: string
  outcome: 'kept' | 'from_trash' | 'moved' | 'trashed' | 'missing'
}

/** A paper I sent to the trash. */
export interface TrashItem extends PaperRef {
  deleted_at: string
  purge_at: string
  /** Papers that went to the trash with it. */
  below: number
}

export interface PaperStatusInfo {
  version_number: number
  changed: boolean
}

export type DiffKind = 'added' | 'removed' | 'changed' | 'moved'

export interface DiffEntry {
  block_id: string
  kind: DiffKind
  before?: Block
  after?: Block
}

export interface Diff {
  from: number
  to: number
  title_before: string
  title_after: string
  entries: DiffEntry[] | null
}

export interface Sidebar {
  home: PaperRef | null
  favorites: PaperRef[]
  recent: PaperRef[]
  /** The user's own unfiled and private papers, counted up to 100 (100 means 100 or more). */
  unfiled_count: number
}

/** One row of the papers list. */
export interface PaperListItem extends PaperRef {
  created_at: string
  updated_at: string
  parent: { id: string; title: string } | null
}

export interface PaperListQuery {
  place?: 'unfiled' | 'filed' | 'all'
  creator?: 'me' | 'all'
  visibility?: 'all' | 'public' | 'private'
  archived?: boolean
  sort?: 'updated' | 'created'
  limit?: number
  cursor?: string
}

export interface SearchHit {
  paper: PaperRef
  block_id?: string
  block_type?: BlockType
  text: string
}

export interface Attachment {
  id: string
  workspace_id: string
  paper_id: string
  name: string
  content_type: string
  size: number
}

export interface Presence {
  block_id: string
  actor: Actor
  until: string
  leaving?: boolean
}

export type EventType = 'block_put' | 'block_deleted' | 'paper_updated' | 'paper_deleted' | 'version_saved' | 'presence'

export interface PaperEvent {
  type: EventType
  workspace_id: string
  paper_id: string
  at: string
  block?: Block
  block_id?: string
  paper?: Paper
  version?: number
  presence?: Presence
}

export interface Me {
  user: User
  no_authn: boolean
  /** Only the in-browser demo sets this. */
  demo?: boolean
}

export interface ApiToken {
  id: string
  name: string
  kind: 'personal' | 'agent'
  agent_name?: string
  scope: 'read' | 'write'
  workspaces?: string[]
  created_at: string
  expires_at?: string
  last_used_at?: string
}
