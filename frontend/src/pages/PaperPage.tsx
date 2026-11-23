import type { Editor as TiptapEditor } from '@tiptap/core'
import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { flushSync } from 'react-dom'
import { Link, useNavigate } from 'react-router'
import { ApiError, api, loadAllBlocks } from '../api/client'
import { LiveConnection } from '../api/live'
import {
  applyBlocks, applyChildren, applyPaper, applyPresence, byOrder, cellsOf, editorsOf, initState, listNumbers, replaceTemp, topLevel, type PaperState,
} from '../api/paperState'
import type { Actor, BatchResult, Block, BlockProps, BlockType, ChildOutcome, DiffEntry, Me, Op, PaperEvent, PaperStatusInfo, PaperView, Sidebar, VersionSummary, Workspace } from '../api/types'
import { BlockView, type BlockContext, type EditingState, type Mark } from '../components/BlockView'
import { CONTINUING, TEXT_TYPES, TURN_INTO, matchChoices, type TypeChoice } from '../components/blockTypes'
import type { ConflictChoice, ConflictState } from '../components/ConflictPanel'
import { HistoryDrawer, totalOf, type ChangeCounts } from '../components/HistoryDrawer'
import { MoveDialog } from '../components/MoveDialog'
import type { Caret, Slash } from '../components/RichEditor'
import { SlashMenu } from '../components/SlashMenu'
import { detectShortcut } from '../components/shortcuts'
import { History, type Snap, type Step } from '../editing/history'
import { ago, useTranslation } from '../i18n'
import { plainText } from '../markup/markup'
import { Menu, type MenuItem } from '../ui/Menu'
import { Modal } from '../ui/Modal'
import { Popover } from '../ui/Popover'
import { useToast } from '../ui/Toast'
import { actorColor, actorName, initials } from '../utils/actors'
import { isDemo } from '../utils/demo'
import { isEnter, isNativeEnter, MOD_LABEL, modKey } from '../utils/keyboard'
import { keyBetween } from '../utils/order'
import type { Layout } from '../App'

const RECENT_MS = 1400
const PRESENCE_REFRESH_MS = 30000
const SAVE_PAUSE_MS = 700
const SLOW_MS = 300

let seq = 0
const tempId = () => `tmp:${++seq}`
const sessionId = () => `s${++seq}`

/** Removes a typed shortcut prefix ("# ", "- ") from markup. */
function stripPrefix(markup: string, prefixLen: number, plain: string): string {
  const rest = plain.slice(prefixLen)
  for (let i = 0; i <= markup.length; i++) {
    if (plainText(markup.slice(i)) === rest) return markup.slice(i)
  }
  return rest
}

const snapOf = (b: Block): Snap => ({ type: b.type, content: b.content, props: b.props })

/** The text, type and properties a block shows while my update of it is on its way. */
type Held = Pick<Block, 'content' | 'type' | 'props'>

/** The edit changed the block (its text, type or properties). */
function isDirty(ed: EditingState): boolean {
  return ed.textChanged || ed.type !== ed.orig.type || JSON.stringify(ed.props) !== JSON.stringify(ed.orig.props)
}

function tempBlock(paperId: string, ws: string, me: Me, order: string, type: BlockType, content: string, props: BlockProps): Block {
  const actor: Actor = { kind: 'human', user_id: me.user.id, name: me.user.name }
  const now = new Date().toISOString()
  return { id: tempId(), workspace_id: ws, paper_id: paperId, order, type, content, props, rev: 1, created_by: actor, created_at: now, updated_by: actor, updated_at: now }
}

/**
 * An order key just after `after` ('' = first, undefined = last), for showing
 * a block before the server answers. It is the key the server picks, so the
 * block keeps its place when the answer replaces it.
 */
function orderAfter(blocks: Block[], after: string | undefined): string {
  const i = after === undefined ? blocks.length - 1 : after === '' ? -1 : blocks.findIndex((x) => x.id === after)
  const lo = blocks[i]?.order ?? ''
  const hi = blocks[i + 1]?.order ?? ''
  try {
    return keyBetween(lo, hi)
  } catch {
    return `${lo}~`
  }
}

/** The block before `id` among the top-level blocks ('' = it is first). */
function prevOf(blocks: Block[], id: string): string {
  const i = blocks.findIndex((b) => b.id === id)
  return i > 0 ? blocks[i - 1]?.id ?? '' : ''
}

/** What one batch did, as steps the undo history can reverse. */
function stepsFor(ops: Op[], before: PaperState, res: BatchResult): Step[] {
  const steps: Step[] = []
  const was = topLevel(before)
  const after = res.blocks ?? []
  const refId = (a: string | undefined) => (a?.startsWith('ref:') ? res.refs?.[a.slice(4)] ?? '' : a)
  for (const op of ops) {
    const b0 = op.block_id ? before.blocks.get(op.block_id) : undefined
    switch (op.op) {
      case 'update': {
        const b1 = after.find((b) => b.id === op.block_id)
        if (b0 && b1 && b0.type !== 'table') steps.push({ k: 'update', id: b1.id, before: snapOf(b0), after: snapOf(b1) })
        break
      }
      case 'delete':
        if (b0 && !b0.parent_id && !['paper', 'table'].includes(b0.type)) steps.push({ k: 'delete', id: b0.id, after: prevOf(was, b0.id), snap: snapOf(b0) })
        break
      case 'insert': {
        const id = op.ref ? res.refs?.[op.ref] : undefined
        const b1 = id ? after.find((b) => b.id === id) : undefined
        if (b1 && !b1.parent_id && !['paper', 'table'].includes(b1.type)) {
          const anchor = op.after === undefined ? prevOf([...was, b1].sort(byOrder), b1.id) : refId(op.after) ?? ''
          steps.push({ k: 'insert', id: b1.id, after: anchor, snap: snapOf(b1) })
        }
        break
      }
      case 'move':
        if (b0 && !b0.parent_id) steps.push({ k: 'move', id: b0.id, from: prevOf(was, b0.id), to: refId(op.after) ?? '' })
        break
      case 'set_title':
        if (op.title !== undefined) steps.push({ k: 'title', before: before.paper.title, after: op.title })
        break
    }
  }
  return steps
}

/** Diff entries as marks, plus the removed blocks to show at their old place. */
function marksOf(entries: DiffEntry[] | null): { marks: Map<string, Mark>; removed: Block[] } {
  const marks = new Map<string, Mark>()
  const removed: Block[] = []
  for (const e of entries ?? []) {
    if (e.kind === 'added') marks.set(e.block_id, 'added')
    else if (e.kind === 'changed') marks.set(e.block_id, 'changed')
    else if (e.kind === 'removed' && e.before && !e.before.parent_id) {
      marks.set(e.block_id, 'removed')
      removed.push(e.before)
    }
  }
  return { marks, removed }
}

function countsOf(entries: DiffEntry[] | null, titleBefore: string, titleAfter: string): ChangeCounts {
  const c: ChangeCounts = { added: 0, changed: 0, removed: 0, title: titleBefore !== titleAfter }
  for (const e of entries ?? []) {
    if (e.before?.parent_id || e.after?.parent_id) {
      if (e.kind !== 'moved') c.changed++
      continue
    }
    if (e.kind === 'added') c.added++
    else if (e.kind === 'removed') c.removed++
    else if (e.kind === 'changed') c.changed++
  }
  return c
}

const EMOJI = ['💡', '⚠️', '🧭', '✅', '❗', '📌', '🤖', '🔒', '🔥', '📝']

type Dialog =
  | { kind: 'saved'; n: number; counts: ChangeCounts | null }
  | { kind: 'restore'; n: number }
  | { kind: 'move' }
  | { kind: 'private' }
  | { kind: 'trash' }
  | { kind: 'trashOther' }
  | { kind: 'trashChild'; block: Block }
  | { kind: 'cellBusy'; who: Actor; run: () => void }

export function PaperPage({ ws, paperId, version, me, workspaces, sidebar, onChanged, layout, onView }: {
  ws: string
  paperId: string
  /** 'latest' or a number: show that saved version read-only. */
  version?: 'latest' | number
  me: Me
  workspaces: Workspace[]
  sidebar?: Sidebar
  onChanged: () => void
  layout: Layout
  /** Tells the shell where the paper is (the sidebar opens its ancestors). */
  onView?: (v: PaperView | undefined) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const toast = useToast()

  const [view, setView] = useState<PaperView>()
  const [state, setState] = useState<PaperState>()
  const [shown, setShown] = useState<VersionSummary>()
  // What restoring the shown version does with its child papers.
  const [shownKids, setShownKids] = useState<ChildOutcome[]>([])
  const [status, setStatus] = useState<PaperStatusInfo>()
  const [conn, setConn] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [sending, setSending] = useState(0)
  const [failed, setFailed] = useState<Op[] | null>(null)
  const [fatal, setFatal] = useState<'notfound' | 'revoked' | 'deleted' | null>(null)
  const [slow, setSlow] = useState(false)
  const [editing, setEditing] = useState<EditingState | null>(null)
  const [held, setHeld] = useState<Map<string, Held>>(() => new Map())
  const [recent, setRecent] = useState<Set<string>>(new Set())
  const [conflict, setConflict] = useState<ConflictState | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [slash, setSlash] = useState<(Slash & { blockId: string; active: number; mode: 'type' | 'insert'; anchor?: HTMLElement; filter?: string }) | null>(null)
  const [blockMenu, setBlockMenu] = useState<{ block: Block; anchor: HTMLElement } | null>(null)
  const [pageMenu, setPageMenu] = useState<HTMLElement | null>(null)
  const [emoji, setEmoji] = useState<{ block: Block; anchor: HTMLElement } | null>(null)
  const [dialog, setDialog] = useState<Dialog | null>(null)
  const [versions, setVersions] = useState<VersionSummary[]>([])
  const [counts, setCounts] = useState<ChangeCounts | null>(null)
  const [showChanges, setShowChanges] = useState(false)
  const [diffMode, setDiffMode] = useState<'version' | 'now'>('version')
  const [diffMarks, setDiffMarks] = useState<{ marks: Map<string, Mark>; removed: Block[] } | null>(null)
  const [title, setTitle] = useState('')
  const [scrolled, setScrolled] = useState(false)

  const stateRef = useRef<PaperState | undefined>(undefined)
  stateRef.current = state
  const editingRef = useRef<EditingState | null>(null)
  editingRef.current = editing
  const conflictRef = useRef<ConflictState | null>(null)
  conflictRef.current = conflict
  // Temporary block keys and the IDs their inserts got. Kept after the answer: an edit or a later insert can still name the key.
  const pending = useRef(new Map<string, Promise<string | undefined>>())
  const viewKeys = useRef(new Map<string, string>())
  const history = useRef(new History())
  const chain = useRef<Promise<unknown>>(Promise.resolve())
  const ownRev = useRef(new Map<string, number>())
  /** The text of an update of mine still on its way, by block. */
  const inflight = useRef(new Map<string, string>())
  /** What blocks I have just left show until my update of them is answered (by block or temporary key). */
  const heldRef = useRef(held)
  /** Blocks my batches still on their way write, and how many such batches insert. */
  const writing = useRef(new Map<string, number>())
  const inserting = useRef(0)
  /** Blocks of mine the change feed brought while my inserts were on their way; applied when they are answered. */
  const deferred = useRef(new Map<string, Block>())
  const saveTimer = useRef<number | undefined>(undefined)
  const editorRef = useRef<TiptapEditor | null>(null)
  const titleRef = useRef<HTMLTextAreaElement>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const fileFor = useRef<{ replace?: Block; after?: string } | null>(null)

  const readOnlyVersion = version !== undefined
  const base = `/ws/${ws}/p/${paperId}`

  // Also read while a saved version is shown: restoring it asks to save unsaved changes first.
  const refreshStatus = useCallback(() => {
    void api.status(ws, paperId).then(setStatus).catch(() => undefined)
  }, [ws, paperId])

  const load = useCallback(async () => {
    try {
      const v = await api.getPaper(ws, paperId)
      setView(v)
      if (version !== undefined) {
        const vv = await api.version(ws, paperId, version)
        setState(initState(vv.paper, vv.blocks))
        setShown(vv.version)
        setShownKids(vv.children ?? [])
        setTitle(vv.paper.title)
        const kids = await api.children(ws, paperId).catch(() => [])
        setState((s) => (s ? applyChildren(s, Object.fromEntries(kids.map((k) => [k.id, k]))) : s))
        refreshStatus()
        return
      }
      const first = await api.blocks(ws, paperId, '', 200)
      // A reload (the live connection's hello) keeps who is editing what:
      // presence that arrived meanwhile is not sent again for a while.
      setState((s) => ({ ...initState(v.paper, first.blocks, first.papers), presence: s?.paper.id === paperId ? s.presence : new Map() }))
      setTitle(v.paper.title)
      setShown(undefined)
      let after = first.next
      while (after) {
        const page = await api.blocks(ws, paperId, after, 500)
        setState((s) => (s ? applyChildren(applyBlocks(s, page.blocks), page.papers) : s))
        after = page.next
      }
      refreshStatus()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'moved' && e.movedTo) {
        const to = workspaces.find((w) => w.id === e.movedTo?.to_workspace_id)
        toast(t('movedTo', { ws: to?.name ?? e.movedTo.to_workspace_id }))
        navigate(`/ws/${e.movedTo.to_workspace_id}/p/${e.movedTo.to_paper_id}`, { replace: true })
        return
      }
      if (e instanceof ApiError && e.status === 404) setFatal('notfound')
      else toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }, [ws, paperId, version, navigate, refreshStatus, t, toast, workspaces])

  const markRecent = useCallback((ids: string[]) => {
    if (ids.length === 0) return
    setRecent((prev) => new Set([...prev, ...ids]))
    window.setTimeout(() => setRecent((prev) => {
      const next = new Set(prev)
      ids.forEach((id) => next.delete(id))
      return next
    }), RECENT_MS)
  }, [])

  const commit = useCallback((next: PaperState) => {
    stateRef.current = next
    setState(next)
  }, [])

  const setEdit = useCallback((next: EditingState | null) => {
    editingRef.current = next
    setEditing(next)
  }, [])

  /**
   * Shows a block I have just left with the text I am sending (the editor
   * is gone, and the block would show its last answered text until the
   * answer comes). The returned function stops it once the send is over.
   */
  const hold = useCallback((id: string, h: Held): (() => void) => {
    const put = (next: Map<string, Held>) => {
      heldRef.current = next
      setHeld(next)
    }
    put(new Map(heldRef.current).set(id, h))
    return () => {
      if (heldRef.current.get(id) !== h) return
      const next = new Map(heldRef.current)
      next.delete(id)
      put(next)
    }
  }, [])
  /** A block as it shows: with the text held for it, under its own ID or its temporary key. */
  const shownOf = useCallback((b: Block, from: Map<string, Held> = heldRef.current): Block => {
    const h = from.get(b.id) ?? from.get(viewKeys.current.get(b.id) ?? '')
    return h ? { ...b, ...h } : b
  }, [])

  const openConflict = useCallback((c: ConflictState | null) => {
    conflictRef.current = c
    setConflict(c)
  }, [])

  // Applies resulting block states (from our own ops or the change feed).
  // Another writer's change to the block being edited replaces it when
  // nothing is waiting to be sent, and opens the conflict choice otherwise.
  const receive = useCallback((put: Block[], deleted: string[], paper: BatchResult['paper'] | undefined, own: boolean, by?: Actor) => {
    const cur = stateRef.current
    if (!cur) return
    const ed = editingRef.current
    if (!own && ed) {
      const gone = deleted.includes(ed.id)
      const theirs = put.find((b) => b.id === ed.id && b.rev > ed.base)
      const dirty = isDirty(ed)
      // My own write can come back through the feed before its answer does.
      const mine = theirs && (inflight.current.get(theirs.id) === theirs.content || theirs.content === ed.draft)
      if (theirs && mine) {
        setEdit({ ...ed, base: theirs.rev, orig: { content: theirs.content, type: theirs.type, props: theirs.props }, textChanged: ed.draft !== theirs.content })
      } else if (gone) {
        const ghost = cur.blocks.get(ed.id)
        openConflict({ blockId: ed.id, theirs: null, by, mine: ed.draft, ghost })
      } else if (theirs && !dirty) {
        setEdit({ ...ed, draft: theirs.content, base: theirs.rev, type: theirs.type, props: theirs.props, orig: { content: theirs.content, type: theirs.type, props: theirs.props } })
      } else if (theirs && !conflictRef.current) {
        openConflict({ blockId: ed.id, theirs, mine: ed.draft })
      }
    }
    // My own writes come back through the feed too, often before their
    // answer. A block of mine the feed brings while my insert is on its way
    // waits for the answer (which shows it in place of its temporary key),
    // so it is not shown twice; and my own writes do not flash.
    const byMe = (b: Block) => b.updated_by.kind === 'human' && b.updated_by.user_id === me.user.id
    for (const id of deleted) deferred.current.delete(id)
    const wait = !own && inserting.current > 0 ? put.filter((b) => byMe(b) && !cur.blocks.has(b.id)) : []
    wait.forEach((b) => deferred.current.set(b.id, b))
    const apply = wait.length ? put.filter((b) => !wait.includes(b)) : put
    let next = applyBlocks(cur, apply, deleted)
    if (paper) next = applyPaper(next, paper)
    if (next === cur) return
    commit(next)
    if (paper && document.activeElement !== titleRef.current) setTitle(paper.title)
    if (!own) markRecent(apply.filter((b) => next.blocks.get(b.id) === b && !b.parent_id && !(byMe(b) && writing.current.has(b.id))).map((b) => b.id))
  }, [markRecent, commit, openConflict, setEdit, me.user.id])

  // Initial load and the live connection (none for a saved version).
  useEffect(() => {
    setState(undefined)
    setView(undefined)
    setFatal(null)
    setEdit(null)
    openConflict(null)
    heldRef.current = new Map()
    setHeld(heldRef.current)
    deferred.current.clear()
    setSelected(null)
    setDiffMarks(null)
    setShowChanges(false)
    history.current.clear()
    setSlow(false)
    const slowTimer = window.setTimeout(() => setSlow(true), SLOW_MS)
    void load().then(() => { window.clearTimeout(slowTimer); onChanged() })
    if (readOnlyVersion) return () => window.clearTimeout(slowTimer)
    const live = new LiveConnection(api.liveURL(ws, paperId), {
      onHello: () => void load(),
      onStatus: setConn,
      onEvent: (ev: PaperEvent) => {
        const cur = stateRef.current
        if (!cur) return
        switch (ev.type) {
          case 'block_put':
            if (ev.block) {
              receive([ev.block], [], undefined, false)
              if (ev.block.type === 'paper' && ev.block.props.paper_id && !cur.children.has(ev.block.props.paper_id)) {
                void api.children(ws, paperId).then((kids) => {
                  const s = stateRef.current
                  if (s) commit(applyChildren(s, Object.fromEntries(kids.map((k) => [k.id, k]))))
                })
              }
            }
            break
          case 'block_deleted':
            if (ev.block_id) receive([], [ev.block_id], undefined, false)
            break
          case 'paper_updated':
            if (ev.paper && ev.paper.id === paperId) {
              receive([], [], ev.paper, false)
              if (ev.paper.deleted_at) void load()
            }
            break
          case 'version_saved':
            refreshStatus()
            void api.versions(ws, paperId).then(setVersions).catch(() => undefined)
            break
          case 'presence':
            if (ev.presence) commit(applyPresence(cur, ev.presence, me.user.id))
            break
        }
      },
      onClosing: (reason) => {
        if (reason === 'access revoked') setFatal('revoked')
        if (reason === 'paper deleted') setFatal('deleted')
      },
    })
    return () => {
      window.clearTimeout(slowTimer)
      live.close()
    }
  }, [ws, paperId, readOnlyVersion, load, receive, commit, refreshStatus, me.user.id, onChanged, setEdit, openConflict])

  // Presence: tell others which block I am editing, and when I stop.
  const editingId = editing?.id
  useEffect(() => {
    if (!editingId || editingId.startsWith('tmp:') || readOnlyVersion || isDemo) return
    const report = (leaving: boolean) => void api.presence(ws, paperId, editingId, leaving).catch(() => undefined)
    report(false)
    const timer = window.setInterval(() => report(false), PRESENCE_REFRESH_MS)
    return () => {
      window.clearInterval(timer)
      report(true)
    }
  }, [editingId, ws, paperId, readOnlyVersion])

  // ---- sending ----

  // Sends ops. A conflict (someone changed a block since the revision an op
  // is based on) is not an error: the block being edited opens the conflict
  // choice; any other block takes the current state and says who changed it.
  const run = useCallback(async (ops: Op[], opts: { force?: boolean; record?: boolean; session?: string; beforeApply?: (res: BatchResult) => void } = {}): Promise<BatchResult | null> => {
    const before = stateRef.current
    setSending((n) => n + 1)
    const ids = ops.flatMap((op) => (op.block_id ? [op.block_id] : []))
    ids.forEach((id) => writing.current.set(id, (writing.current.get(id) ?? 0) + 1))
    const inserts = ops.some((op) => op.op === 'insert' || op.op === 'insert_markdown')
    if (inserts) inserting.current++
    try {
      const res = await api.ops(ws, paperId, ops, opts.force ?? false)
      opts.beforeApply?.(res)
      receive(res.blocks ?? [], res.deleted ?? [], res.paper, true)
      if (before && opts.record !== false) history.current.record(stepsFor(ops, before, res), opts.session)
      const kids = (res.blocks ?? []).filter((b) => b.type === 'paper')
      if (kids.length || (res.deleted ?? []).length) {
        const refs = await api.children(ws, paperId)
        const s = stateRef.current
        if (s) commit(applyChildren(s, Object.fromEntries(refs.map((k) => [k.id, k]))))
        if (kids.length) onChanged()
      }
      if (res.editing?.length) toast(t('removedWhileEditing', { names: res.editing.map((e) => actorName(e.actor)).join(', ') }))
      if (res.not_restored?.length) toast(t('notRestored', { count: res.not_restored.length }))
      for (const b of res.blocks ?? []) ownRev.current.set(b.id, b.rev)
      setFailed(null)
      refreshStatus()
      return res
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') {
        const ed = editingRef.current
        for (const c of e.conflicts) {
          if (ed && c.block_id === ed.id) {
            openConflict({ blockId: ed.id, theirs: c.current, mine: ed.draft, ghost: c.current ? undefined : stateRef.current?.blocks.get(ed.id) })
          } else if (c.current) {
            toast(t('conflictTitle', { name: actorName(c.current.updated_by) }))
          }
        }
        const current = e.conflicts.flatMap((c) => (c.current ? [c.current] : []))
        const gone = e.conflicts.filter((c) => !c.current && c.block_id !== ed?.id).map((c) => c.block_id)
        const s = stateRef.current
        if (s) commit(applyBlocks(s, current, gone))
      } else if (e instanceof ApiError && e.code === 'busy') {
        toast(t('busy'))
      } else if (e instanceof ApiError && e.status < 500 && e.status !== 0) {
        toast(t('error', { message: e.message }))
      } else {
        setFailed(ops)
      }
      return null
    } finally {
      setSending((n) => n - 1)
      for (const id of ids) {
        const n = (writing.current.get(id) ?? 1) - 1
        if (n > 0) writing.current.set(id, n)
        else writing.current.delete(id)
      }
      if (inserts && --inserting.current === 0 && deferred.current.size) {
        const late = [...deferred.current.values()]
        deferred.current.clear()
        receive(late, [], undefined, false)
      }
    }
  }, [ws, paperId, receive, commit, onChanged, refreshStatus, t, toast, openConflict])

  const runOk = useCallback(async (ops: Op[]) => (await run(ops)) !== null, [run])

  /** Resolves a block ID that may still be a temporary key. */
  const realId = useCallback(async (id: string) => (id.startsWith('tmp:') ? pending.current.get(id) ?? Promise.resolve(undefined) : id), [])

  const startEdit = useCallback((from: Block, caret: Caret = 'end') => {
    if (readOnlyVersion) return
    const b = shownOf(from)
    const cur = editingRef.current
    if (cur && cur.id !== b.id) finishRef.current(cur.id)
    if (conflictRef.current && conflictRef.current.blockId !== b.id) openConflict(null)
    setSelected(null)
    setEdit({ id: b.id, draft: b.content, type: b.type, props: b.props, base: b.rev, orig: { content: b.content, type: b.type, props: b.props }, textChanged: false, session: sessionId(), caret })
  }, [readOnlyVersion, setEdit, openConflict, shownOf])

  /** Sends run one after another, so each is based on the revisions the one before produced. */
  const serial = useCallback(<T,>(fn: () => Promise<T>): Promise<T> => {
    const p = chain.current.then(fn, fn)
    chain.current = p.catch(() => undefined)
    return p
  }, [])
  /** The base for a block: the edit's start, or the revision my own last write produced. */
  const baseFor = useCallback((id: string, base: number) => Math.max(base, ownRev.current.get(id) ?? 0), [])

  /**
   * Inserts a block: it shows at once under a temporary key and becomes
   * editable in the same task (keys typed next land in it); the server's ID
   * replaces the key when the answer comes. extra runs in the same batch.
   */
  const insertShown = useCallback((op: Op & { op: 'insert' }, extra: () => Promise<Op[]> = async () => [], edit = true, caret: Caret = 'start'): Promise<string | undefined> => {
    const cur = stateRef.current
    if (!cur) return Promise.resolve(undefined)
    const tmp = tempBlock(paperId, ws, me, orderAfter(topLevel(cur), op.after), op.type ?? 'paragraph', op.content ?? '', op.props ?? {})
    flushSync(() => {
      commit({ ...cur, blocks: new Map(cur.blocks).set(tmp.id, tmp) })
      if (edit) startEdit(tmp, caret)
    })
    const ref = tmp.id.slice(4)
    const p = serial(async () => {
      const pre = await extra()
      const after = op.after?.startsWith('tmp:') ? await realId(op.after) : op.after
      return run([...pre, { ...op, after, ref }], {
        beforeApply: (res) => {
          const real = res.refs?.[ref]
          if (real) viewKeys.current.set(real, tmp.id)
          const s = stateRef.current
          if (s) commit(replaceTemp(s, tmp.id, undefined))
          const ed = editingRef.current
          const fresh = (res.blocks ?? []).find((b) => b.id === real)
          if (ed?.id === tmp.id && fresh) setEdit({ ...ed, id: fresh.id, base: fresh.rev })
        },
      })
    }).then((res) => {
      const real = res?.refs?.[ref]
      const s = stateRef.current
      if (s) commit(replaceTemp(s, tmp.id, real ? s.blocks.get(real) : undefined))
      const ed = editingRef.current
      if (ed?.id === tmp.id) setEdit(null)
      return real
    })
    pending.current.set(tmp.id, p)
    return p
  }, [paperId, ws, me, commit, startEdit, run, setEdit, serial, realId])

  // An edit as an update op, based on the revision the edit started from
  // (not the latest one received since; my own saves count). It takes a
  // copy of the edit made when the send was asked for: by the time the send
  // runs, the caret may be in another block.
  const opsFor = useCallback(async (ed: EditingState | null): Promise<Op[] | null> => {
    if (!ed) return []
    if (conflictRef.current && conflictRef.current.blockId === ed.id) return null
    const id = await realId(ed.id)
    if (!id) return []
    if (!isDirty(ed)) return []
    return [{ op: 'update', block_id: id, content: ed.draft, type: ed.type === ed.orig.type ? undefined : ed.type, props: ed.props, base_rev: baseFor(id, ed.base) }]
  }, [realId, baseFor])

  /** Sends the edit so far without leaving the block (a pause in typing). */
  const flushEdit = useCallback(() => {
    window.clearTimeout(saveTimer.current)
    const ed = editingRef.current
    return serial(async () => {
    if (!ed) return
    const ops = await opsFor(ed)
    if (!ops || ops.length === 0) return
    const target = ops[0]?.block_id ?? ''
    inflight.current.set(target, ops[0]?.content ?? '')
    const res = await run(ops, { session: ed.session }).finally(() => inflight.current.delete(target))
    const fresh = res?.blocks?.find((b) => b.id === ops[0]?.block_id)
    const now = editingRef.current
    if (fresh && now && now.session === ed.session) {
      const sameText = now.draft === ops[0]?.content
      setEdit({ ...now, id: fresh.id, base: fresh.rev, orig: { content: fresh.content, type: fresh.type, props: fresh.props }, textChanged: !sameText })
    }
    })
  }, [opsFor, run, setEdit, serial])

  // An editor that loses focus finishes only its own block: when Enter has
  // moved the edit to a new block, the old editor's blur must not send the
  // new block's half-typed text.
  const finishEdit = useCallback((from?: string) => {
    window.clearTimeout(saveTimer.current)
    const ed = editingRef.current
    if (!ed || conflictRef.current?.blockId === ed.id) return
    if (from !== undefined && from !== ed.id) return
    // Another block may take the editor before this send is answered.
    const release = isDirty(ed) ? hold(ed.id, { content: ed.draft, type: ed.type, props: ed.props }) : () => undefined
    void serial(async () => {
      const ops = await opsFor(ed)
      if (ops === null) return
      const target = ops[0]?.block_id ?? ''
      if (ops.length) inflight.current.set(target, ops[0]?.content ?? '')
      const ok = !ops.length || !!(await run(ops, { session: ed.session }).finally(() => inflight.current.delete(target)))
      if (!ok) return
      if (editingRef.current?.session === ed.session) setEdit(null)
    }).finally(release)
  }, [opsFor, run, setEdit, serial, hold])
  const finishRef = useRef(finishEdit)
  finishRef.current = finishEdit

  const scheduleSave = useCallback(() => {
    window.clearTimeout(saveTimer.current)
    saveTimer.current = window.setTimeout(() => void flushEdit(), SAVE_PAUSE_MS)
  }, [flushEdit])

  const ordered = useMemo(() => {
    if (!state) return []
    const list = topLevel(state)
    const extra: Block[] = []
    if (conflict?.ghost && !state.blocks.has(conflict.ghost.id)) extra.push(conflict.ghost)
    const marks = readOnlyVersion || showChanges ? diffMarks : null
    if (marks) extra.push(...marks.removed.filter((b) => !state.blocks.has(b.id)))
    return extra.length ? [...list, ...extra].sort(byOrder) : list
  }, [state, conflict, diffMarks, readOnlyVersion, showChanges])
  const live = useMemo(() => (state ? topLevel(state) : []), [state])
  // Blocks just left show the text being sent for them.
  const shownList = useMemo(() => (held.size ? ordered.map((b) => shownOf(b, held)) : ordered), [ordered, held, shownOf])
  const numbers = useMemo(() => listNumbers(shownList), [shownList])

  // Enter in the middle of a block: update it with the text before the
  // caret and insert a block with the rest after it, in one batch. The new
  // block is editable at once.
  const split = useCallback((before: string, after: string) => {
    const ed = editingRef.current
    if (!ed) return
    window.clearTimeout(saveTimer.current)
    const continuing = CONTINUING.has(ed.type)
    if (continuing && before === '' && after === '') {
      setEdit({ ...ed, type: 'paragraph', props: {}, textChanged: true })
      void flushEdit()
      return
    }
    const type = continuing ? ed.type : 'paragraph'
    const props: BlockProps = continuing ? { indent: ed.props.indent ?? 0, ...(ed.type === 'todo' ? { checked: false } : {}) } : {}
    const prev = { ...ed, draft: before, textChanged: ed.textChanged || before !== ed.draft }
    // The old block's text goes with the insert, not through its editor's
    // blur, and it shows that text until the batch is answered.
    editingRef.current = null
    const release = isDirty(prev) ? hold(prev.id, { content: before, type: prev.type, props: prev.props }) : () => undefined
    let sent = ''
    void insertShown({ op: 'insert', type, content: after, props, after: ed.id }, async () => {
      const id = await realId(prev.id)
      if (!id || !isDirty(prev)) return []
      // Its echo in the feed is mine, should the block be edited again meanwhile.
      sent = id
      inflight.current.set(id, before)
      return [{ op: 'update', block_id: id, content: before, type: prev.type === prev.orig.type ? undefined : prev.type, props: prev.props, base_rev: baseFor(id, prev.base) }]
    }, true, 'start').finally(() => {
      if (sent && inflight.current.get(sent) === before) inflight.current.delete(sent)
      release()
    })
  }, [insertShown, realId, flushEdit, setEdit, baseFor, hold])

  // Backspace at the start: a list item becomes text first; text joins the block above.
  const join = useCallback((text: string) => {
    const ed = editingRef.current
    if (!ed) return
    if (ed.type !== 'paragraph') {
      const indent = ed.props.indent ?? 0
      setEdit(indent > 0 ? { ...ed, props: { ...ed.props, indent: indent - 1 }, caret: 'start' } : { ...ed, type: 'paragraph', props: {}, caret: 'start' })
      void flushEdit()
      return
    }
    const idx = live.findIndex((b) => b.id === ed.id)
    const prev = idx > 0 ? live[idx - 1] : undefined
    if (!prev) return
    const joinable = TEXT_TYPES.has(prev.type)
    void serial(async () => {
      const id = await realId(ed.id)
      if (!id) return
      if (!joinable) {
        if (text === '') {
          setEdit(null)
          await run([{ op: 'delete', block_id: id, base_rev: baseFor(id, ed.base) }])
        }
        setSelected(prev.id)
        return
      }
      setEdit(null)
      const offset = plainText(prev.content).length
      const cur = stateRef.current?.blocks.get(prev.id) ?? prev
      const ops: Op[] = [{ op: 'update', block_id: prev.id, content: cur.content + text, base_rev: cur.rev }, { op: 'delete', block_id: id, base_rev: baseFor(id, ed.base) }]
      if (await runOk(ops)) {
        const fresh = stateRef.current?.blocks.get(prev.id)
        if (fresh) startEdit(fresh, { offset })
      }
    })
  }, [live, runOk, run, realId, startEdit, flushEdit, setEdit, serial, baseFor])

  const arrowOut = useCallback((dir: 'up' | 'down') => {
    const ed = editingRef.current
    if (!ed) return
    const idx = live.findIndex((b) => b.id === ed.id)
    const step = dir === 'up' ? -1 : 1
    for (let i = idx + step; i >= 0 && i < live.length; i += step) {
      const b = live[i]
      if (!b) break
      if (TEXT_TYPES.has(b.type) || b.type === 'code') {
        startEdit(b, dir === 'up' ? 'end' : 'start')
        return
      }
      finishEdit()
      setSelected(b.id)
      return
    }
  }, [live, startEdit, finishEdit])

  const setDraft = useCallback((text: string, changed: boolean) => {
    const ed = editingRef.current
    if (!ed) return
    let next: EditingState = { ...ed, draft: text, textChanged: changed }
    if (ed.type === 'paragraph') {
      const plain = plainText(text)
      const conv = detectShortcut(plain)
      if (conv) {
        const prefix = plain.length - conv.content.length
        next = { ...next, draft: stripPrefix(text, prefix, plain), type: conv.type, props: conv.props, textChanged: true, caret: 'start' }
      }
    }
    setEdit(next)
    if (next.type === 'divider') {
      void (async () => {
        await flushEdit()
        const id = await realId(next.id)
        setEdit(null)
        if (id) await insertShown({ op: 'insert', type: 'paragraph', content: '', after: id })
      })()
      return
    }
    if (next.type !== ed.type) void flushEdit()
    else scheduleSave()
  }, [flushEdit, scheduleSave, realId, insertShown, setEdit])

  const setDraftProps = useCallback((props: BlockProps) => {
    const ed = editingRef.current
    if (!ed) return
    setEdit({ ...ed, props })
    scheduleSave()
  }, [scheduleSave, setEdit])

  const indent = useCallback((shift: boolean) => {
    const ed = editingRef.current
    if (!ed) return
    const next = Math.max(0, Math.min(6, (ed.props.indent ?? 0) + (shift ? -1 : 1)))
    setEdit({ ...ed, props: { ...ed.props, indent: next } })
    void flushEdit()
  }, [flushEdit, setEdit])

  const pasteLines = useCallback((lines: string[]) => {
    const ed = editingRef.current
    if (!ed) return
    void serial(async () => {
      const id = await realId(ed.id)
      if (!id) return
      const ops: Op[] = []
      let after = id
      lines.forEach((line, i) => {
        const conv = detectShortcut(line.trim())
        const content = (conv ? conv.content : line).replace(/[*`[\\~]/g, (m) => `\\${m}`)
        ops.push({ op: 'insert', ref: `p${i}`, type: conv?.type ?? 'paragraph', content: conv?.type === 'divider' ? '' : content, props: conv?.props ?? {}, after })
        after = `ref:p${i}`
      })
      const edit = await opsFor(ed)
      if (edit === null) return
      if (editingRef.current?.session === ed.session) setEdit(null)
      await run([...edit, ...ops], { session: ed.session })
    })
  }, [realId, opsFor, run, setEdit, serial])

  const upload = useCallback(async (file: File, target: { replace?: Block; after?: string }) => {
    try {
      const att = await api.upload(ws, paperId, file)
      if (target.replace && !target.replace.content && target.replace.type === 'image') {
        await run([{ op: 'update', block_id: target.replace.id, props: { attachment_id: att.id }, base_rev: target.replace.rev }])
      } else {
        await run([{ op: 'insert', type: 'image', content: '', props: { attachment_id: att.id }, after: target.after }])
      }
    } catch (e) {
      toast(t('uploadFailed', { message: e instanceof Error ? e.message : String(e) }))
    }
  }, [ws, paperId, run, toast, t])

  const pasteImage = useCallback((file: File) => {
    const ed = editingRef.current
    void (async () => {
      const after = ed ? await realId(ed.id) : undefined
      await upload(file, { after })
    })()
  }, [realId, upload])

  const pickImage = useCallback((b: Block) => {
    fileFor.current = { replace: b }
    fileRef.current?.click()
  }, [])

  // ---- block kinds from "/" or "Insert below" ----
  const applyKind = useCallback(async (bid: string, c: TypeChoice, replace: boolean) => {
    // A text type for the block being edited changes it at once, before its insert is acknowledged,
    // so keystrokes that follow land in the new type.
    if ((TEXT_TYPES.has(c.type) || c.type === 'code') && replace) {
      const ed0 = editingRef.current
      if (ed0 && ed0.id === bid) {
        setEdit({ ...ed0, type: c.type, props: c.props, textChanged: true, caret: 'start' })
        await flushEdit()
        return
      }
    }
    const id = await realId(bid)
    if (!id) return
    const cur = stateRef.current?.blocks.get(id)
    if (TEXT_TYPES.has(c.type) || c.type === 'code') {
      const ed = editingRef.current
      if (replace && ed && (ed.id === id || ed.id === bid)) {
        setEdit({ ...ed, type: c.type, props: c.props, textChanged: true, caret: 'start' })
        await flushEdit()
        return
      }
      finishEdit()
      await insertShown({ op: 'insert', type: c.type, content: '', props: c.props, after: id })
      return
    }
    // Structure: the empty block the "/" was typed in goes away.
    const drop: Op[] = replace && cur ? [{ op: 'delete', block_id: id, base_rev: editingRef.current?.id === id ? editingRef.current.base : cur.rev }] : []
    if (replace) setEdit(null)
    else finishEdit()
    const after = replace ? prevOf(live, id) : id
    switch (c.type) {
      case 'divider':
        await run([...drop, { op: 'insert', ref: 'd', type: 'divider', content: '', after }])
        await insertShown({ op: 'insert', type: 'paragraph', content: '', after: stateRef.current ? topLevel(stateRef.current).find((b) => b.type === 'divider' && b.order > (cur?.order ?? ''))?.id : undefined })
        return
      case 'table':
        await run([...drop, { op: 'insert', type: 'table', props: { header_row: true }, rows: 3, cols: 3, after }])
        return
      case 'image':
        if (drop.length) await run(drop)
        fileFor.current = { after }
        fileRef.current?.click()
        return
      case 'paper': {
        const res = await run([...drop, { op: 'insert', type: 'paper', title: '', after }], { record: false })
        const child = res?.blocks?.find((b) => b.type === 'paper' && !stateRef.current?.children.has(b.props.paper_id ?? ''))?.props.paper_id
          ?? res?.blocks?.find((b) => b.type === 'paper')?.props.paper_id
        if (child) navigate(`/ws/${ws}/p/${child}`, { state: { focusTitle: true } })
        return
      }
    }
  }, [realId, setEdit, flushEdit, finishEdit, insertShown, run, live, navigate, ws])

  const slashItems = useMemo(() => {
    if (!slash || !state) return []
    const p = state.paper
    const canChildren = p.visibility === 'public' && (!!view?.is_home || !!p.parent_id)
    return matchChoices(slash.mode === 'insert' ? slash.filter ?? '' : slash.query, t, canChildren)
  }, [slash, state, view, t])

  const pickSlash = useCallback((c: TypeChoice) => {
    const s = slash
    if (!s) return
    setSlash(null)
    if (s.mode === 'type') {
      const ed = editorRef.current
      ed?.chain().deleteRange({ from: s.from, to: s.to }).run()
      const empty = !ed || ed.state.doc.textContent.trim() === ''
      void applyKind(s.blockId, c, empty)
    } else {
      const b = stateRef.current?.blocks.get(s.blockId)
      void applyKind(s.blockId, c, !!b && b.type === 'paragraph' && b.content === '')
    }
  }, [slash, applyKind])

  const slashKey = useCallback((key: 'ArrowUp' | 'ArrowDown' | 'Enter' | 'Escape') => {
    if (!slash) return false
    const n = slashItems.length
    if (key === 'Escape') setSlash(null)
    else if (key === 'ArrowDown' && n) setSlash({ ...slash, active: (slash.active + 1) % n })
    else if (key === 'ArrowUp' && n) setSlash({ ...slash, active: (slash.active - 1 + n) % n })
    else if (key === 'Enter') {
      const c = slashItems[slash.active]
      if (c) pickSlash(c)
      else setSlash(null)
    }
    return true
  }, [slash, slashItems, pickSlash])

  // ---- conflicts ----
  const resolveConflict = useCallback((choice: ConflictChoice) => {
    const c = conflictRef.current
    const ed = editingRef.current
    if (!c) return
    openConflict(null)
    const theirs = c.theirs
    if (theirs && ed) {
      const adopt = { ...ed, draft: theirs.content, base: theirs.rev, type: theirs.type, props: theirs.props, orig: { content: theirs.content, type: theirs.type, props: theirs.props }, textChanged: false, caret: 'end' as Caret }
      if (choice === 'theirs') {
        setEdit(adopt)
        return
      }
      if (choice === 'keepBoth') {
        setEdit(null)
        void insertShown({ op: 'insert', type: theirs.type, content: c.mine, props: theirs.props, after: theirs.id }, undefined, true, 'end')
        return
      }
      if (choice === 'overwrite') {
        setEdit({ ...adopt, draft: c.mine, textChanged: true })
        void run([{ op: 'update', block_id: theirs.id, content: c.mine, base_rev: theirs.rev }], { force: true, session: ed.session })
          .then((res) => {
            const fresh = res?.blocks?.find((b) => b.id === theirs.id)
            const now = editingRef.current
            if (fresh && now?.id === theirs.id) setEdit({ ...now, base: fresh.rev, orig: { content: fresh.content, type: fresh.type, props: fresh.props }, textChanged: false })
          })
      }
      return
    }
    // The block was deleted: add my text again, or let it go.
    setEdit(null)
    if (choice === 'addAsNew' && c.ghost) {
      const s = stateRef.current
      const after = s ? prevOf([...topLevel(s), c.ghost].sort(byOrder), c.ghost.id) : undefined
      void insertShown({ op: 'insert', type: c.ghost.type, content: c.mine, props: c.ghost.props, after }, undefined, true, 'end')
    }
  }, [openConflict, setEdit, insertShown, run])

  // ---- block actions ----
  const deleteBlock = useCallback((b: Block) => {
    if (b.type === 'paper') {
      setDialog({ kind: 'trashChild', block: b })
      return
    }
    if (editingRef.current?.id === b.id) setEdit(null)
    void run([{ op: 'delete', block_id: b.id, base_rev: b.rev }]).then((res) => {
      if (res) toast(t('blockDeleted'), { label: t('undo'), run: () => void undoRef.current('undo') })
    })
  }, [run, setEdit, toast, t])

  const moveBlock = useCallback((b: Block, dir: -1 | 1) => {
    const idx = live.findIndex((x) => x.id === b.id)
    if (dir === -1 && idx > 0) void run([{ op: 'move', block_id: b.id, after: idx >= 2 ? live[idx - 2]?.id ?? '' : '' }])
    if (dir === 1 && idx >= 0 && idx < live.length - 1) void run([{ op: 'move', block_id: b.id, after: live[idx + 1]?.id }])
  }, [live, run])

  const duplicate = useCallback((b: Block) => {
    if (b.type === 'paper' || b.type === 'table') return
    void run([{ op: 'insert', ref: 'dup', type: b.type, content: b.content, props: b.props, after: b.id }])
  }, [run])

  const deleteLine = useCallback((table: Block, kind: 'row' | 'col', id: string) => {
    const s = stateRef.current
    if (!s) return
    const cells = [...cellsOf(s.blocks.values(), table.id).values()].filter((c) => (kind === 'row' ? c.props.row_id : c.props.col_id) === id)
    const who = cells.flatMap((c) => editorsOf(s, c.id))[0]
    const go = () => void run([{ op: kind === 'row' ? 'delete_row' : 'delete_col', block_id: table.id, line_id: id }], { record: false })
    if (who) setDialog({ kind: 'cellBusy', who, run: go })
    else go()
  }, [run])

  // Dragging a block's handle; a click without moving opens the block menu.
  const startDrag = useCallback((e: ReactPointerEvent, b: Block) => {
    if (e.button !== 0) return
    e.preventDefault()
    const anchor = e.currentTarget as HTMLElement
    const sx = e.clientX
    const sy = e.clientY
    let moved = false
    let target: { id: string; before: boolean } | null = null
    const line = document.createElement('div')
    line.className = 'dropline'
    line.hidden = true
    document.body.append(line)
    const el = anchor.closest('.blk') as HTMLElement | null
    const mv = (ev: PointerEvent) => {
      if (!moved && Math.hypot(ev.clientX - sx, ev.clientY - sy) < 5) return
      if (!moved) {
        moved = true
        finishEdit()
        if (el) el.style.opacity = '0.4'
      }
      const blocks = [...document.querySelectorAll<HTMLElement>('.blocks > .blk[data-block-id]')]
      let best: { el: HTMLElement; before: boolean } | null = null
      for (const x of blocks) {
        const r = x.getBoundingClientRect()
        if (ev.clientY < r.top + r.height / 2) {
          best = { el: x, before: true }
          break
        }
      }
      const last = blocks[blocks.length - 1]
      if (!best && last) best = { el: last, before: false }
      if (!best) return
      const r = best.el.getBoundingClientRect()
      const bd = best.el.querySelector('.bd')?.getBoundingClientRect() ?? r
      line.hidden = false
      Object.assign(line.style, { left: `${bd.left}px`, width: `${bd.width}px`, top: `${(best.before ? r.top : r.bottom) - 1}px` })
      target = { id: best.el.dataset.blockId ?? '', before: best.before }
    }
    const up = () => {
      document.removeEventListener('pointermove', mv)
      document.removeEventListener('pointerup', up)
      line.remove()
      if (el) el.style.opacity = ''
      if (!moved) {
        setBlockMenu({ block: b, anchor })
        return
      }
      const tg = target as { id: string; before: boolean } | null
      if (!tg || tg.id === b.id) return
      const rest = live.filter((x) => x.id !== b.id)
      const i = rest.findIndex((x) => x.id === tg.id)
      const after = tg.before ? (i > 0 ? rest[i - 1]?.id ?? '' : '') : tg.id
      if (after === prevOf(live, b.id)) return
      void run([{ op: 'move', block_id: b.id, after }])
    }
    document.addEventListener('pointermove', mv)
    document.addEventListener('pointerup', up)
  }, [finishEdit, live, run])

  // ---- undo / redo ----
  const undo = useCallback(async (dir: 'undo' | 'redo') => {
    if (editingRef.current) {
      await flushEdit()
      if (conflictRef.current) return
      setEdit(null)
    }
    const s = stateRef.current
    if (!s) return
    const plan = history.current.plan(dir, (id) => {
      const b = stateRef.current?.blocks.get(id)
      return b ? { rev: b.rev, snap: snapOf(b) } : undefined
    }, stateRef.current?.paper.title)
    if (!plan.ok) {
      if (plan.reason === 'empty') toast(dir === 'undo' ? t('nothingToUndo') : t('nothingToRedo'))
      else toast(t('undoBlocked', { name: actorName(plan.blockId ? s.blocks.get(plan.blockId)?.updated_by : s.paper.updated_by) || t('someone') }))
      return
    }
    const res = await run(plan.ops, { record: false })
    if (!res) return
    history.current.applied(dir, plan, res.refs)
    const touched = (res.blocks ?? []).find((b) => !b.parent_id)
    if (touched) markRecent([touched.id])
  }, [flushEdit, run, setEdit, toast, t, markRecent])
  const undoRef = useRef(undo)
  undoRef.current = undo

  // ---- versions ----
  useEffect(() => {
    if (!layout.drawer && !readOnlyVersion) return
    void api.versions(ws, paperId).then(setVersions).catch(() => setVersions([]))
  }, [layout.drawer, ws, paperId, readOnlyVersion, status?.version_number])

  const latestN = state?.paper.version_number ?? 0
  useEffect(() => {
    // A shown version has its own marks (the effect below).
    if (readOnlyVersion) return
    if (!latestN || !status?.changed) {
      setCounts(null)
      setDiffMarks(null)
      return
    }
    if (!layout.drawer && !showChanges) return
    void api.diff(ws, paperId, String(latestN), 'current').then((d) => {
      setCounts(countsOf(d.entries, d.title_before, d.title_after))
      setDiffMarks(marksOf(d.entries))
    }).catch(() => undefined)
  }, [readOnlyVersion, latestN, status, layout.drawer, showChanges, ws, paperId, state?.blocks])

  useEffect(() => {
    if (!readOnlyVersion || !shown) return
    const n = shown.number
    if (diffMode === 'version' && n <= 1) {
      setDiffMarks(null)
      return
    }
    const req = diffMode === 'version' ? api.diff(ws, paperId, String(n - 1), String(n)) : api.diff(ws, paperId, String(n), 'current')
    void req.then((d) => {
      if (diffMode === 'version') {
        setDiffMarks(marksOf(d.entries))
        return
      }
      // Compared to now: blocks only the current state has are shown as added.
      const m = new Map<string, Mark>()
      const extra: Block[] = []
      for (const e of d.entries ?? []) {
        if (e.kind === 'removed') m.set(e.block_id, 'removed')
        else if (e.kind === 'changed') m.set(e.block_id, 'changed')
        else if (e.kind === 'added' && e.after && !e.after.parent_id) {
          m.set(e.block_id, 'added')
          extra.push(e.after)
        }
      }
      setDiffMarks({ marks: m, removed: extra })
    }).catch(() => setDiffMarks(null))
  }, [readOnlyVersion, shown, diffMode, ws, paperId])

  const saveVersion = useCallback(async () => {
    if (readOnlyVersion || !stateRef.current) return
    if (editingRef.current) {
      await flushEdit()
      if (conflictRef.current) return
    }
    if (!stateRef.current) return
    const latest = stateRef.current.paper.version_number
    let c: ChangeCounts | null = null
    if (latest) {
      const d = await api.diff(ws, paperId, String(latest), 'current').catch(() => null)
      c = d ? countsOf(d.entries, d.title_before, d.title_after) : null
    }
    try {
      const v = await api.saveVersion(ws, paperId, '')
      if (!v) {
        toast(t('nothingToSave'))
        return
      }
      setDialog({ kind: 'saved', n: v.number, counts: latest ? c : null })
      setShowChanges(false)
      refreshStatus()
      void load()
      onChanged()
    } catch (e) {
      toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }, [readOnlyVersion, flushEdit, ws, paperId, toast, t, refreshStatus, load, onChanged])

  const restore = useCallback(async (n: number, saveFirst: boolean) => {
    try {
      if (saveFirst) await api.saveVersion(ws, paperId, '')
      const res = await api.restoreVersion(ws, paperId, n)
      if (res.not_restored?.length) toast(t('notRestored', { count: res.not_restored.length }))
      history.current.clear()
      toast(t('restored', { n }))
      onChanged()
      navigate(base)
    } catch (e) {
      toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }, [ws, paperId, toast, t, onChanged, navigate, base])

  // ---- keys ----
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement
      const field = target.closest('input, textarea:not(.editor), select, .scrim, .pal')
      if (modKey(e) && e.key.toLowerCase() === 's') {
        e.preventDefault()
        void saveVersion()
        return
      }
      if (modKey(e) && e.shiftKey && e.key.toLowerCase() === 'h') {
        e.preventDefault()
        layout.setDrawer(!layout.drawer)
        return
      }
      if (!field && modKey(e) && (e.key.toLowerCase() === 'z' || (!e.metaKey && e.key.toLowerCase() === 'y'))) {
        e.preventDefault()
        void undo(e.key.toLowerCase() === 'y' || e.shiftKey ? 'redo' : 'undo')
        return
      }
      // A selected block (Esc from the editor): arrows move, Delete removes.
      const sel = selectedRef.current
      if (!sel || target.closest('.ProseMirror, input, textarea')) return
      const b = stateRef.current?.blocks.get(sel)
      if (!b) return
      const idx = live.findIndex((x) => x.id === sel)
      if (e.key === 'Backspace' || e.key === 'Delete') {
        e.preventDefault()
        setSelected(live[idx + 1]?.id ?? live[idx - 1]?.id ?? null)
        deleteBlock(b)
      } else if ((e.key === 'ArrowUp' || e.key === 'ArrowDown') && e.altKey) {
        e.preventDefault()
        moveBlock(b, e.key === 'ArrowUp' ? -1 : 1)
      } else if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
        e.preventDefault()
        const n = live[idx + (e.key === 'ArrowUp' ? -1 : 1)]
        if (n) setSelected(n.id)
      } else if (isNativeEnter(e)) {
        e.preventDefault()
        if (TEXT_TYPES.has(b.type) || b.type === 'code') startEdit(b, 'end')
      } else if (modKey(e) && e.key.toLowerCase() === 'd') {
        e.preventDefault()
        duplicate(b)
      } else if (e.key === 'Escape') {
        setSelected(null)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [saveVersion, undo, layout, live, deleteBlock, moveBlock, startEdit, duplicate])
  const selectedRef = useRef(selected)
  selectedRef.current = selected
  useEffect(() => {
    if (selected) document.querySelector<HTMLElement>(`.blk[data-block-id="${selected}"]`)?.focus()
  }, [selected])

  const onViewRef = useRef(onView)
  onViewRef.current = onView
  useEffect(() => onViewRef.current?.(view), [view])

  // A search hit (#block) scrolls to its block and flashes it once loaded.
  const hashDone = useRef('')
  useEffect(() => {
    const id = window.location.hash.slice(1)
    if (!state || !id || hashDone.current === id || !state.blocks.has(id)) return
    hashDone.current = id
    window.setTimeout(() => {
      document.querySelector(`.blk[data-block-id="${id}"]`)?.scrollIntoView({ block: 'center' })
      markRecent([id])
    }, 30)
  }, [state, markRecent])

  // A new child paper opens with its title ready for typing.
  const titleFocused = useRef(false)
  const loadedPaper = !!state
  useEffect(() => {
    if (!loadedPaper || titleFocused.current) return
    titleFocused.current = true
    const st = window.history.state as { usr?: { focusTitle?: boolean } } | null
    if (st?.usr?.focusTitle && !stateRef.current?.paper.title) titleRef.current?.focus()
  }, [loadedPaper])

  // ---- render ----
  const paper = state?.paper
  const canEdit = !readOnlyVersion && !!paper && !paper.deleted_at && !paper.job_id
  const owner = paper?.created_by.user_id === me.user.id
  const isHome = !!view?.is_home
  const filed = !!paper && paper.visibility === 'public' && (isHome || !!paper.parent_id)
  const unfiled = !!paper && paper.visibility === 'public' && !isHome && !paper.parent_id
  const hasChildren = live.some((b) => b.type === 'paper')
  const favorite = !!sidebar?.favorites.some((f) => f.id === paperId)
  const workspace = workspaces.find((w) => w.id === ws)

  // Enter sends the title and moves on, and the blur that follows must not send it again.
  const sentTitle = useRef<string | null>(null)
  const saveTitle = () => {
    const next = title.trim()
    if (!state || next === state.paper.title || next === sentTitle.current) return
    sentTitle.current = next
    void run([{ op: 'set_title', title: next }]).finally(() => { sentTitle.current = null })
  }

  const toggleFavorite = async () => {
    try {
      if (favorite) await api.unfavorite(ws, paperId)
      else await api.favorite(ws, paperId)
      toast(favorite ? t('favoriteRemoved') : t('favoriteAdded'))
      onChanged()
    } catch (e) {
      toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }

  const doPaper = async (fn: () => Promise<unknown>, done: string) => {
    try {
      await fn()
      toast(done)
      onChanged()
      await load()
    } catch (e) {
      toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }

  const print = async () => {
    const all = await loadAllBlocks(ws, paperId)
    const s = stateRef.current
    if (s) commit(applyChildren(applyBlocks(s, all.blocks), all.papers))
    window.setTimeout(() => window.print(), 50)
  }

  const pageItems = (): MenuItem[] => {
    if (!paper) return []
    const items: MenuItem[] = [
      { label: t('copyLink'), icon: '🔗', onSelect: () => void navigator.clipboard?.writeText(`${window.location.origin}${base}`).then(() => toast(t('copied')), () => undefined) },
      { label: t('exportMarkdown'), icon: '⇩', onSelect: () => { window.location.href = api.markdownURL(ws, paperId) } },
      { label: t('print'), icon: '⎙', onSelect: () => void print() },
      { label: t('latestVersion'), icon: 'v', disabled: !paper.version_number, onSelect: () => navigate(`${base}/latest`) },
      '-',
    ]
    if (!isHome) {
      if (filed) {
        items.push({ label: t('moveEllipsis'), icon: '↗', disabled: !!paper.job_id, onSelect: () => setDialog({ kind: 'move' }) })
        items.push({ label: t('unfile'), icon: '⇱', disabled: hasChildren || !!paper.job_id, desc: hasChildren ? t('unfileNo') : undefined, onSelect: () => void doPaper(() => api.unfile(ws, paperId), t('unfiledDone')) })
      }
      if (unfiled) items.push({ label: t('moveEllipsis'), icon: '↘', disabled: !!paper.job_id, onSelect: () => setDialog({ kind: 'move' }) })
      if (paper.visibility === 'public') {
        items.push({ label: t('makePrivate'), icon: '🔒', disabled: !owner || hasChildren, desc: !owner ? t('onlyCreatorVisibility') : hasChildren ? t('privateNoChildren') : undefined, onSelect: () => setDialog({ kind: 'private' }) })
      } else {
        items.push({ label: t('makePublic'), icon: '🌐', onSelect: () => void doPaper(() => api.setVisibility(ws, paperId, 'public'), t('publishedUnfiled')) })
      }
      items.push(paper.archived_at
        ? { label: t('unarchive'), icon: '🗄', onSelect: () => void doPaper(() => api.archive(ws, paperId, false, true), t('unarchivedDone')) }
        : { label: t('archive'), icon: '🗄', onSelect: () => void doPaper(() => api.archive(ws, paperId, true, true), t('archivedDone')) })
      items.push({ label: t('moveToTrash'), icon: '🗑', danger: true, onSelect: () => setDialog({ kind: owner ? 'trash' : 'trashOther' }) })
      items.push('-')
    }
    items.push({ note: t('createdBy', { name: actorName(paper.created_by) }) })
    return items
  }

  const blockItems = (b: Block): MenuItem[] => {
    const text = TEXT_TYPES.has(b.type)
    return [
      { label: t('insertBelow'), icon: '+', onSelect: () => {
        const anchor = document.querySelector<HTMLElement>(`.blk[data-block-id="${b.id}"] .hdl`)
        if (anchor) setSlash({ blockId: b.id, mode: 'insert', anchor, query: '', filter: '', from: 0, to: 0, rect: anchor.getBoundingClientRect(), active: 0 })
      } },
      ...(text ? ['-' as const, { head: t('turnInto') }, ...TURN_INTO.map((c) => ({
        label: t(c.label), icon: c.icon,
        onSelect: () => void run([{ op: 'update', block_id: b.id, type: c.type, props: { ...c.props, indent: c.props.indent !== undefined ? b.props.indent ?? 0 : undefined }, content: b.content, base_rev: b.rev }]),
      }))] : []),
      '-',
      { label: t('moveUp'), icon: '↑', kbd: `${MOD_LABEL === '⌘' ? '⌥' : 'Alt'}↑`, onSelect: () => moveBlock(b, -1) },
      { label: t('moveDown'), icon: '↓', kbd: `${MOD_LABEL === '⌘' ? '⌥' : 'Alt'}↓`, onSelect: () => moveBlock(b, 1) },
      ...(b.type !== 'paper' && b.type !== 'table' ? [{ label: t('duplicate'), icon: '⧉', kbd: `${MOD_LABEL}D`, onSelect: () => duplicate(b) }] : []),
      { label: t('copyLink'), icon: '🔗', onSelect: () => void navigator.clipboard?.writeText(`${window.location.origin}${base}#${b.id}`).then(() => toast(t('copied')), () => undefined) },
      '-',
      { label: t('deleteBlock'), icon: '🗑', danger: true, kbd: 'Del', onSelect: () => deleteBlock(b) },
      { note: t('editedBy', { name: actorName(b.updated_by), when: ago(b.updated_at) }) },
    ]
  }

  if (fatal === 'notfound') {
    return (
      <section className="main">
        <header className="top"><button type="button" className="iconbtn" aria-label={t('toggleSidebar')} onClick={layout.toggleSidebar}>☰</button></header>
        <div className="scroll"><div className="col">
          <h1 className="page-h" style={{ paddingLeft: 'var(--gutter)' }}>{t('paperNotFound')}</h1>
          <p className="page-sub" style={{ paddingLeft: 'var(--gutter)' }}>{t('paperNotFoundBody')}</p>
          <div style={{ display: 'flex', gap: 'var(--sp-4)', paddingLeft: 'var(--gutter)' }}>
            <Link className="btn primary" to={`/ws/${ws}/papers`}>{t('openPapers')}</Link>
            <Link className="btn" to={`/ws/${ws}/trash`}>{t('openTrash')}</Link>
          </div>
        </div></div>
      </section>
    )
  }

  const faces = state ? [...new Map([...state.presence.values()].filter((p) => Date.parse(p.until) > Date.now()).map((p) => [`${p.actor.user_id}:${p.actor.agent_name ?? ''}`, p.actor])).values()] : []
  const changed = !!status?.changed || !paper?.version_number
  const canSave = canEdit && changed && conn === 'open'
  const syncState = failed ? 'fail' : conn !== 'open' ? 'off' : sending > 0 ? 'busy' : ''
  const marks = readOnlyVersion || (showChanges && !readOnlyVersion) ? diffMarks?.marks : undefined

  const ctx: BlockContext | null = state ? {
    ws, state, canEdit, editing, conflict, recent, numbers, selected, menuFor: blockMenu?.block.id ?? null, marks, slashOpen: !!slash && slash.mode === 'type',
    actions: {
      startEdit, setDraft, setDraftProps, finishEdit, split, join, arrowOut, indent, pasteLines, pasteImage, pickImage, run: runOk,
      openBlockMenu: (b, anchor) => setBlockMenu({ block: b, anchor }), startDrag, resolveConflict,
      slash: (s) => {
        const ed = editingRef.current
        setSlash(s && ed ? { ...s, blockId: ed.id, active: 0, mode: 'type' } : null)
      },
      slashKey,
      setField: (b, name, value) => void run([{ op: 'update', block_id: b.id, props: { ...b.props, fields: { ...(b.props.fields ?? {}), [name]: value } }, base_rev: b.rev }]),
      select: (id) => { finishEdit(); setSelected(id) },
      emojiMenu: (b, anchor) => setEmoji({ block: b, anchor }),
      onEditor: (ed) => { editorRef.current = ed },
      deleteRow: (table, row) => deleteLine(table, 'row', row),
      deleteCol: (table, col) => deleteLine(table, 'col', col),
      current: () => stateRef.current,
    },
  } : null

  return (
    <>
      <section className="main">
        <header className={`top${scrolled ? ' scrolled' : ''}`}>
          <button type="button" className="iconbtn" aria-label={t('toggleSidebar')} title={`${t('toggleSidebar')} (${MOD_LABEL}\\)`} onClick={layout.toggleSidebar}>☰</button>
          <nav className="crumbs" aria-label={t('location')}>
            {paper && !filed && (
              <><Link className="crumb" to={`/ws/${ws}/papers`}>{paper.visibility === 'private' ? `🔒 ${t('private')}` : t('unfiled')}</Link><span className="crumb-sep">/</span></>
            )}
            {view && filed && (
              <Link className={`crumb${isHome ? ' cur' : ''}`} to={`/ws/${ws}`}>{workspace?.emoji ? `${workspace.emoji} ` : ''}{workspace?.name ?? ws}</Link>
            )}
            {view && filed && (view.path ?? []).slice(1).map((p) => (
              <span key={p.id} style={{ display: 'contents' }}><span className="crumb-sep">/</span><Link className="crumb" to={`/ws/${ws}/p/${p.id}`}>{p.title || t('untitled')}</Link></span>
            ))}
            {view && !isHome && <>{filed && <span className="crumb-sep">/</span>}<span className="crumb cur">{paper?.title || t('untitled')}</span></>}
          </nav>
          {paper && (
            <div className="top-r">
              <div className="faces">
                {faces.map((a) => (
                  <span key={`${a.user_id}:${a.agent_name ?? ''}`} className={`face${a.kind === 'agent' ? ' ai' : ''}`} style={{ background: actorColor(a) }} title={actorName(a)}>{a.kind === 'agent' ? '✦' : initials(a.name)}</span>
                ))}
              </div>
              {!readOnlyVersion && (
                <span className={`sync ${syncState}`} role="status">
                  <span className="dot" />
                  <span className="tx">{failed ? t('sendFailed') : conn !== 'open' ? t('reconnecting') : sending > 0 ? t('syncing') : t('synced')}</span>
                  {failed && <button type="button" className="btn sm" onClick={() => { const ops = failed; setFailed(null); void run(ops) }}>{t('retry')}</button>}
                </span>
              )}
              {!readOnlyVersion && canEdit && (changed
                ? (
                  <button type="button" className="btn primary savebtn" disabled={!canSave} title={`${t('saveVersion')} (${MOD_LABEL}S)`} onClick={() => void saveVersion()}>
                    <span className="si" aria-hidden><svg width="14" height="14" viewBox="0 0 16 16"><path d="M4 2h8a1 1 0 0 1 1 1v11l-5-3-5 3V3a1 1 0 0 1 1-1z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" /></svg></span>
                    <span className="lbl-full">{t('saveVersion')}</span><span className="lbl-short">{t('save')}</span>
                    <span className="kbd hide-sm" style={{ background: 'transparent', color: 'inherit', borderColor: 'currentColor', opacity: 0.7 }}>{MOD_LABEL}S</span>
                  </button>
                )
                : (
                  <span className="btn saved" role="status" title={t('nothingToSave')}>
                    <span className="si" aria-hidden><svg width="14" height="14" viewBox="0 0 16 16"><path d="M3 8.5l3 3 7-7" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg></span>
                    <span>{t('savedState')}</span><span className="vnum" style={{ fontFamily: 'var(--font-code)', fontSize: 'var(--text-sm)' }}>v{paper.version_number}</span>
                  </span>
                ))}
              {paper.visibility === 'public' && !isHome && (
                <button type="button" className={`iconbtn star${favorite ? ' on' : ''}`} aria-pressed={favorite} aria-label={favorite ? t('removeFavorite') : t('addFavorite')} title={favorite ? t('removeFavorite') : t('addFavorite')} onClick={() => void toggleFavorite()}>
                  <svg width="17" height="17" viewBox="0 0 24 24" aria-hidden><path d="M12 3.5l2.6 5.3 5.9.9-4.25 4.1 1 5.8L12 16.9l-5.25 2.7 1-5.8L3.5 9.7l5.9-.9z" fill={favorite ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" /></svg>
                </button>
              )}
              <button type="button" className={`iconbtn${layout.drawer ? ' on' : ''}`} aria-label={t('history')} title={`${t('history')} (${MOD_LABEL}⇧H)`} onClick={() => layout.setDrawer(!layout.drawer)}>🕘</button>
              <button type="button" className="iconbtn" aria-label={t('more')} aria-haspopup="menu" onClick={(e) => setPageMenu(e.currentTarget)}>⋯</button>
            </div>
          )}
        </header>
        <div className="scroll" onScroll={(e) => setScrolled((e.target as HTMLElement).scrollTop > 4)}>
          <div className="col">
            {!state && slow && (
              <div aria-busy="true"><div className="skel" style={{ width: '30%' }} /><div className="skel t" /><div className="skel" /><div className="skel" style={{ width: '85%' }} /><div className="skel" style={{ width: '60%' }} /></div>
            )}
            {fatal && <div className="banner warn">{fatal === 'revoked' ? t('accessRevoked') : t('paperDeleted')}</div>}
            {state && paper && ctx && (
              <>
                {!readOnlyVersion && (
                  <div className="meta">
                    {paper.version_number > 0
                      ? <Link className="vmark" to={`${base}/latest`} title={t('latestVersion')}><span className="vdot" /><span className="vnum">v{paper.version_number}</span></Link>
                      : <span className="vmark"><span className="vdot open" /><span>{t('draft')}</span></span>}
                    {paper.visibility === 'private' && <><span className="mdot" /><span>🔒 {t('private')}</span><button type="button" className="linkbtn" onClick={() => void doPaper(() => api.setVisibility(ws, paperId, 'public'), t('publishedUnfiled'))}>{t('makePublic')}</button></>}
                    {unfiled && <><span className="mdot" /><span>{t('unfiled')}</span><button type="button" className="linkbtn" onClick={() => setDialog({ kind: 'move' })}>{t('moveEllipsis')}</button></>}
                    {paper.archived_at && <><span className="mdot" /><span>{t('archived')}</span></>}
                    <span className="mdot" /><span>{t('editedBy', { name: actorName(paper.updated_by), when: ago(paper.updated_at) })}</span>
                  </div>
                )}
                {readOnlyVersion && shown && (
                  <div className="banner ver">
                    <span className="vmark"><span className="vdot" /><span className="vnum">v{shown.number}</span></span>
                    <span className="sp"><b>{t('viewing', { n: shown.number })}</b> · {t('savedBy', { name: actorName(shown.created_by), when: ago(shown.created_at) })}{shown.note && <span className="muted" style={{ display: 'block' }}>{shown.note}</span>}</span>
                    <span className="seg" role="group">
                      <button type="button" className={diffMode === 'version' ? 'on' : ''} aria-pressed={diffMode === 'version'} onClick={() => setDiffMode('version')}>{t('changesInVersion')}</button>
                      <button type="button" className={diffMode === 'now' ? 'on' : ''} aria-pressed={diffMode === 'now'} onClick={() => setDiffMode('now')}>{t('comparedToNow')}</button>
                    </span>
                    <Link className="btn sm" to={base}>{t('backToCurrent')}</Link>
                    <button type="button" className="btn sm primary" onClick={() => setDialog({ kind: 'restore', n: shown.number })}>{t('restoreThis')}</button>
                  </div>
                )}
                {paper.job_id && <div className="banner warn">⏳ {t('movingBanner')}</div>}
                {paper.archived_at && !readOnlyVersion && (
                  <div className="banner info"><span className="sp">{t('archivedBanner')}</span><button type="button" className="btn sm" onClick={() => void doPaper(() => api.archive(ws, paperId, false, true), t('unarchivedDone'))}>{t('unarchive')}</button></div>
                )}
                {paper.deleted_at && (
                  <div className="banner info"><span className="sp">{t('inTrash')}</span>{owner && <button type="button" className="btn sm" onClick={() => void doPaper(() => api.restorePaper(ws, paperId), t('restoredDone'))}>{t('restoreFromTrash')}</button>}</div>
                )}
                <textarea ref={titleRef} className="title-input" rows={1} value={title} placeholder={t('untitled')} aria-label={t('paperTitle')} readOnly={!canEdit}
                  onChange={(e) => {
                    setTitle(e.target.value.replace(/\n/g, ''))
                    e.target.style.height = '0px'
                    e.target.style.height = `${e.target.scrollHeight}px`
                  }}
                  onBlur={saveTitle}
                  onKeyDown={(e) => {
                    if (!isEnter(e)) return
                    e.preventDefault()
                    saveTitle()
                    const first = live.find((b) => TEXT_TYPES.has(b.type))
                    if (first) flushSync(() => startEdit(first, 'start'))
                    else void insertShown({ op: 'insert', type: 'paragraph', content: '', after: '' })
                  }} />
                <div className="blocks">
                  {shownList.map((b, i) => (
                    <BlockView key={viewKeys.current.get(b.id) ?? b.id} block={b} prev={shownList[i - 1]} ctx={ctx} />
                  ))}
                </div>
                {canEdit && (
                  <div className="addline">
                    <button type="button" onClick={() => {
                      const last = live[live.length - 1]
                      if (last && last.type === 'paragraph' && !last.content) startEdit(last, 'start')
                      else void insertShown({ op: 'insert', type: 'paragraph', content: '', after: last?.id })
                    }}>{t('clickToAdd')}</button>
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      </section>
      {layout.drawer && paper && (
        <HistoryDrawer base={base} versions={versions} viewing={shown?.number} counts={counts} showChanges={showChanges} onShowChanges={setShowChanges} onClose={() => layout.setDrawer(false)} />
      )}

      <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/gif,image/webp" hidden onChange={(e) => {
        const f = e.target.files?.[0]
        const target = fileFor.current
        e.target.value = ''
        if (f && target) void upload(f, target)
      }} />

      {slash && (
        <SlashMenu anchor={slash.mode === 'insert' && slash.anchor ? slash.anchor : slash.rect} items={slashItems} active={slash.active}
          onPick={pickSlash} onHover={(i) => setSlash({ ...slash, active: i })} onClose={() => setSlash(null)}
          filter={slash.mode === 'insert' ? {
            value: slash.filter ?? '',
            onChange: (v) => setSlash({ ...slash, filter: v, active: 0 }),
            onKey: (k) => { if (k === 'Escape') setSlash(null); else slashKey(k as 'Enter') },
          } : undefined} />
      )}
      {blockMenu && <Menu anchor={blockMenu.anchor} items={blockItems(blockMenu.block)} onClose={() => setBlockMenu(null)} />}
      {pageMenu && <Menu anchor={pageMenu} items={pageItems()} onClose={() => setPageMenu(null)} label={t('more')} />}
      {emoji && (
        <Popover anchor={emoji.anchor} onClose={() => setEmoji(null)}>
          <div className="swatches">
            {EMOJI.map((x) => (
              <button key={x} type="button" className="sw" style={{ fontSize: 'var(--text-body)' }} onClick={() => {
                const b = emoji.block
                setEmoji(null)
                void run([{ op: 'update', block_id: b.id, props: { ...b.props, icon: x }, base_rev: stateRef.current?.blocks.get(b.id)?.rev ?? b.rev }])
              }}>{x}</button>
            ))}
          </div>
        </Popover>
      )}

      {dialog?.kind === 'saved' && (
        <SavedDialog n={dialog.n} counts={dialog.counts} url={`${window.location.origin}${base}/v/${dialog.n}`} onOpen={() => navigate(`${base}/v/${dialog.n}`)} onClose={() => setDialog(null)} />
      )}
      {dialog?.kind === 'restore' && (
        <Modal title={t('restoreTitle', { n: dialog.n })} onClose={() => setDialog(null)}
          actions={status?.changed && latestN !== dialog.n
            ? [{ label: t('cancel') }, { label: t('restoreWithoutSaving'), kind: 'danger', onClick: () => void restore(dialog.n, false) }, { label: t('saveThenRestore'), kind: 'primary', onClick: () => void restore(dialog.n, true) }]
            : [{ label: t('cancel') }, { label: t('restore'), kind: 'primary', onClick: () => void restore(dialog.n, false) }]}>
          <p>{status?.changed && latestN !== dialog.n ? t('restoreDirty', { n: dialog.n, m: latestN }) : t('restoreClean', { n: dialog.n })}</p>
          <RestoreKids kids={shownKids} />
        </Modal>
      )}
      {dialog?.kind === 'move' && paper && (
        <MoveDialog paper={paper} ws={ws} workspaces={workspaces} exclude={new Set([paperId])} onClose={() => setDialog(null)}
          onMove={async (parent, target) => {
            try {
              await api.move(ws, paperId, { parent, workspace: target === ws ? '' : target })
              toast(t('movedDone'))
              onChanged()
              await load()
            } catch (e) {
              toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
            }
          }} />
      )}
      {dialog?.kind === 'private' && (
        <Modal title={t('privateTitle')} onClose={() => setDialog(null)} actions={[{ label: t('cancel') }, { label: t('makePrivate'), kind: 'primary', onClick: () => void doPaper(() => api.setVisibility(ws, paperId, 'private'), t('makePrivate')) }]}>
          <p>{t('privateBody')}</p>
        </Modal>
      )}
      {(dialog?.kind === 'trash' || dialog?.kind === 'trashChild') && (
        <Modal title={t('trashTitle', { title: (dialog.kind === 'trashChild' ? state?.children.get(dialog.block.props.paper_id ?? '')?.title : paper?.title) || t('untitled') })} onClose={() => setDialog(null)}
          actions={[{ label: t('cancel') }, {
            label: t('moveToTrash'), kind: 'danger solid', onClick: async () => {
              if (dialog.kind === 'trashChild') {
                await run([{ op: 'delete', block_id: dialog.block.id, base_rev: dialog.block.rev }], { record: false })
                toast(t('trashed'))
                return
              }
              try {
                await api.trashPaper(ws, paperId)
                toast(t('trashed'), { label: t('undo'), run: () => void api.restorePaper(ws, paperId).then(() => { onChanged(); navigate(base) }) })
                onChanged()
                navigate(paper?.parent_id ? `/ws/${ws}/p/${paper.parent_id}` : `/ws/${ws}/papers`)
              } catch (e) {
                toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
              }
            },
          }]}>
          <p>{t('trashBody')}</p>
        </Modal>
      )}
      {dialog?.kind === 'trashOther' && (
        <Modal title={t('trashNotOwner')} onClose={() => setDialog(null)} actions={[{ label: t('cancel') }, { label: t('archiveInstead'), kind: 'primary', onClick: () => void doPaper(() => api.archive(ws, paperId, true, true), t('archivedDone')) }]}>
          <p>{t('trashNotOwnerBody')}</p>
        </Modal>
      )}
      {dialog?.kind === 'cellBusy' && (
        <Modal title={t('cellBusyTitle', { name: actorName(dialog.who) })} onClose={() => setDialog(null)} actions={[{ label: t('cancel') }, { label: t('deleteAnyway'), kind: 'danger solid', onClick: dialog.run }]}>
          <p>{t('cellBusyBody')}</p>
        </Modal>
      )}
    </>
  )
}

function SavedDialog({ n, counts, url, onOpen, onClose }: { n: number; counts: ChangeCounts | null; url: string; onOpen: () => void; onClose: () => void }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  return (
    <Modal title={t('savedVersion', { n })} onClose={onClose} initialFocus=".copy-url"
      actions={[{ label: t('openVersion'), kind: 'ghost', onClick: onOpen }, { label: t('done') }]}>
      {counts && totalOf(counts) > 0
        ? (
          <div className="stat">
            <span><b className="d-add">+{counts.added}</b> {t('added')}</span>
            <span><b className="d-chg">~{counts.changed}</b> {t('changed')}</span>
            <span><b className="d-del">−{counts.removed}</b> {t('removed')}</span>
            {counts.title && <span>{t('titleChanged')}</span>}
          </div>
        )
        : !counts && n === 1 ? <p style={{ color: 'var(--accent)', marginTop: 0 }}>{t('firstVersion')}</p> : null}
      <div className="urlrow">
        <input className="inp" readOnly value={url} aria-label="URL" onFocus={(e) => e.target.select()} />
        <button type="button" className="btn primary copy-url" onClick={() => void navigator.clipboard?.writeText(url).then(() => setCopied(true), () => undefined)}>{copied ? `✓ ${t('copied')}` : t('copy')}</button>
      </div>
      <p className="muted" style={{ fontSize: 'var(--text-sm)', margin: 'var(--sp-4) 0 0' }}>{t('versionUrlNote')}</p>
    </Modal>
  )
}

/** Before restoring a version: which child papers come back from the trash, and which do not come back. */
function RestoreKids({ kids }: { kids: ChildOutcome[] }) {
  const { t } = useTranslation()
  const back = kids.filter((k) => k.outcome === 'from_trash')
  const lost = kids.filter((k) => k.outcome === 'moved' || k.outcome === 'trashed' || k.outcome === 'missing')
  if (!back.length && !lost.length) return null
  const why = { moved: t('lostMoved'), trashed: t('lostTrashed'), missing: t('lostMissing') } as const
  return (
    <div className="kids">
      {back.length > 0 && (
        <>
          <div className="mh">{t('restoreBack')}</div>
          <ul>{back.map((k) => <li key={k.paper_id}>{k.title || t('untitled')}</li>)}</ul>
        </>
      )}
      {lost.length > 0 && (
        <>
          <div className="mh">{t('restoreLost')}</div>
          <ul>{lost.map((k) => <li key={k.paper_id}>{k.title || t('untitled')}<span className="muted"> · {why[k.outcome as keyof typeof why]}</span></li>)}</ul>
        </>
      )}
    </div>
  )
}
