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
  /** Set for users created by the server's anonymous mode. */
  anonymous?: boolean
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
  /** What AI-authored HTML drawn in a sandboxed frame may do here. */
  sandbox?: { external_resources: boolean }
  /** The paper the user opened last in this workspace, if it can still be shown. */
  last_opened?: { paper_id: string; title: string; opened_at: string } | null
}

export interface WorkspaceGroup {
  name: string
  workspaces: string[]
}

export type Visibility = 'public' | 'private'
export type PaperStatus = 'draft' | 'canonical' | 'archived' | 'trashed'
/** blocks: a body people edit; html: one HTML document written through the API; database: a table of items. */
export type PaperKind = 'blocks' | 'html' | 'database'

export interface Paper {
  id: string
  workspace_id: string
  kind?: PaperKind
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
  /** The database an item belongs to (an item has no parent_id). */
  database_id?: string
  /** A database's property definitions, in column order. */
  properties?: Property[]
  /** A database's items in display order. */
  items?: string[]
}

export type PropertyType = 'text' | 'date' | 'select' | 'multi_select'

/** A choice of a select or multi_select property: values store only its id. */
export interface Element {
  id: string
  value: string
  color: string
}

export interface Property {
  id: string
  name: string
  type: PropertyType
  elements?: Element[]
}

/** What one item holds for one property: a string (text, date, select element id) or element ids. */
export type Value = string | string[]

export interface PropertyValue {
  workspace_id: string
  database_id: string
  paper_id: string
  property_id: string
  value: Value
  updated_by: Actor
  updated_at: string
}

export interface ItemRow {
  item: Paper
  values: PropertyValue[]
}

export interface ItemsPage {
  items: ItemRow[]
  next_cursor: string
}

export interface FilterCondition {
  property: string
  op: 'is' | 'contains' | 'on' | 'before' | 'after' | 'between' | 'starts_with' | 'is_empty' | 'is_not_empty'
  elements?: string[]
  date?: string
  from?: string
  to?: string
  text?: string
}

export interface PaperRef {
  id: string
  workspace_id: string
  kind?: PaperKind
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
  | 'html_page'

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
  | 'add_property' | 'update_property' | 'move_property' | 'delete_property'
  | 'add_element' | 'update_element' | 'move_element' | 'delete_element'
  | 'insert_item' | 'set_values' | 'move_item'

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
  /** insert of a paper block: the kind of the child ('database' creates a database). */
  paper_kind?: PaperKind
  property?: string
  element?: string
  item?: string
  name?: string
  property_type?: PropertyType
  /** An element's value (its text). */
  value?: string
  color?: string
  /** Property values by property id (or ref); null clears. */
  set?: Record<string, Value | null>
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
  items?: Paper[]
  values?: PropertyValue[]
}

export interface PaperView {
  workspace: Workspace
  paper: Paper
  path: PaperRef[]
  is_home: boolean
  /** An item's property values, and its database. */
  values?: PropertyValue[]
  database?: { id: string; title: string; properties: Property[] }
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
  /** An item's property values in the version. */
  values?: PropertyValue[]
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
  | 'item_put' | 'item_deleted' | 'property_value_put' | 'property_value_deleted'

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
  property_value?: PropertyValue
  item_id?: string
  property_id?: string
}

export interface Me {
  user: User
  no_authn: boolean
  /** The server runs in anonymous mode: each browser is its own guest user. */
  anonymous?: boolean
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
