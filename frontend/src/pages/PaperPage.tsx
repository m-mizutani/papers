import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { ApiError, api, loadAllBlocks } from '../api/client'
import { LiveConnection } from '../api/live'
import {
  actorLabel, applyBlocks, applyChildren, applyPaper, applyPresence, initState, listNumbers, replaceTemp, topLevel, type PaperState,
} from '../api/paperState'
import type { BatchResult, Block, BlockProps, BlockType, Me, Op, PaperEvent, PaperStatusInfo, PaperView, VersionSummary } from '../api/types'
import { BlockView, ConflictBox, type BlockContext, type ConflictState, type EditingState } from '../components/BlockView'
import { CONTINUING, INSERT_CHOICES } from '../components/blockTypes'
import { PaperMenu } from '../components/PaperMenu'
import { VersionsPanel } from '../components/VersionsPanel'
import { detectShortcut } from '../components/shortcuts'
import { useTranslation } from '../i18n'
import { plainText } from '../markup/markup'
import { isDemo } from '../utils/demo'
import { errorMessage } from '../utils/errors'
import { isEnter } from '../utils/keyboard'

const RECENT_MS = 4000
const PRESENCE_REFRESH_MS = 30000

let tempSeq = 0
const tempId = () => `tmp:${++tempSeq}`

/** Removes a typed shortcut prefix ("# ", "- ") from markup. */
function stripPrefix(markup: string, prefixLen: number, plain: string): string {
  const rest = plain.slice(prefixLen)
  for (let i = 0; i <= markup.length; i++) {
    if (plainText(markup.slice(i)) === rest) return markup.slice(i)
  }
  return rest
}

function tempBlock(paperId: string, ws: string, me: Me, order: string, type: BlockType, content: string, props: BlockProps): Block {
  const actor = { kind: 'human' as const, user_id: me.user.id, name: me.user.name }
  const now = new Date().toISOString()
  return {
    id: tempId(), workspace_id: ws, paper_id: paperId, order, type, content, props, rev: 1,
    created_by: actor, created_at: now, updated_by: actor, updated_at: now,
  }
}

/** An order key just after `after` for showing a block before the server answers. */
function orderAfter(blocks: Block[], after: string | undefined): string {
  if (after === undefined) return `${blocks[blocks.length - 1]?.order ?? ''}~`
  if (after === '') return ''
  const b = blocks.find((x) => x.id === after)
  return `${b?.order ?? ''}~`
}

export function PaperPage({ ws, paperId, version, me, onChanged }: {
  ws: string
  paperId: string
  /** 'latest' or a number: show that saved version read-only. */
  version?: 'latest' | number
  me: Me
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const [view, setView] = useState<PaperView>()
  const [state, setState] = useState<PaperState>()
  const [shown, setShown] = useState<VersionSummary>()
  const [status, setStatus] = useState<PaperStatusInfo>()
  const [conn, setConn] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [notice, setNotice] = useState<string>()
  const [fatal, setFatal] = useState<string>()
  const [editing, setEditing] = useState<EditingState | null>(null)
  const [recent, setRecent] = useState<Set<string>>(new Set())
  const [conflict, setConflict] = useState<ConflictState | null>(null)
  const [panel, setPanel] = useState<'versions' | null>(null)
  const [title, setTitle] = useState('')

  const stateRef = useRef<PaperState | undefined>(undefined)
  stateRef.current = state
  const editingRef = useRef<EditingState | null>(null)
  editingRef.current = editing
  const conflictRef = useRef<ConflictState | null>(null)
  conflictRef.current = conflict
  // Temporary keys of blocks not yet created, and the ID each becomes.
  const pending = useRef(new Map<string, Promise<string | undefined>>())
  // React keys stay those of the temporary block, so the editor is not
  // remounted (and keeps the caret) when the real ID arrives.
  const viewKeys = useRef(new Map<string, string>())

  const readOnlyVersion = version !== undefined

  const refreshStatus = useCallback(() => {
    if (!readOnlyVersion) void api.status(ws, paperId).then(setStatus).catch(() => undefined)
  }, [ws, paperId, readOnlyVersion])

  const load = useCallback(async () => {
    try {
      const v = await api.getPaper(ws, paperId)
      setView(v)
      if (version !== undefined) {
        const vv = await api.version(ws, paperId, version)
        const page = { blocks: vv.blocks, papers: {} as Record<string, never> }
        setState(initState(vv.paper, page.blocks))
        setShown(vv.version)
        setTitle(vv.paper.title)
        // child titles of a version come from the current children
        const kids = await api.children(ws, paperId).catch(() => [])
        setState((s) => (s ? applyChildren(s, Object.fromEntries(kids.map((k) => [k.id, k]))) : s))
        return
      }
      // First range now, the rest in the background (long papers).
      const first = await api.blocks(ws, paperId, '', 200)
      setState(initState(v.paper, first.blocks, first.papers))
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
        navigate(`/ws/${e.movedTo.to_workspace_id}/p/${e.movedTo.to_paper_id}`, { replace: true })
        return
      }
      setFatal(e instanceof ApiError && e.status === 404 ? t('paperNotFound') : e instanceof Error ? e.message : String(e))
    }
  }, [ws, paperId, version, navigate, refreshStatus, t])

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

  // Applies resulting block states (from our own ops or the change feed).
  // Changes made by others are highlighted for a moment. If someone deletes
  // the block being edited, the draft is kept as a conflict so it can be
  // inserted again instead of vanishing with the block.
  const receive = useCallback((put: Block[], deleted: string[], paper: BatchResult['paper'] | undefined, own: boolean) => {
    const cur = stateRef.current
    if (!cur) return
    const ed = editingRef.current
    if (!own && ed && deleted.includes(ed.id)) {
      const c: ConflictState = {
        ops: [{ op: 'update', block_id: ed.id, content: ed.draft, type: ed.type, props: ed.props, base_rev: ed.base }],
        conflicts: [{ block_id: ed.id, base_rev: ed.base, current: null }], draft: ed.draft,
      }
      conflictRef.current = c
      setConflict(c)
      editingRef.current = null
      setEditing(null)
    }
    let next = applyBlocks(cur, put, deleted)
    if (paper) next = applyPaper(next, paper)
    if (next === cur) return
    commit(next)
    if (paper && !editingRef.current) setTitle(paper.title)
    if (!own) markRecent(put.filter((b) => next.blocks.get(b.id) === b).map((b) => b.id))
  }, [markRecent, commit])

  // Initial load and live connection (none for a saved version).
  useEffect(() => {
    setState(undefined)
    setView(undefined)
    setFatal(undefined)
    setEditing(null)
    setConflict(null)
    // Opening a paper adds it to the recent list (and a new one may be in
    // the private list), so the sidebar is read again once it has loaded.
    void load().then(onChanged)
    if (readOnlyVersion) return
    const live = new LiveConnection(api.liveURL(ws, paperId), {
      // Reload after (re)connecting: events may have been missed.
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
              // a finished move trashes the original: reading it again leads to the copy
              if (ev.paper.deleted_at) void load()
            }
            break
          case 'version_saved':
            refreshStatus()
            break
          case 'presence':
            if (ev.presence) commit(applyPresence(cur, ev.presence, me.user.id))
            break
        }
      },
      onClosing: (reason) => {
        if (reason === 'access revoked') setFatal(t('accessRevoked'))
        if (reason === 'paper deleted') setFatal(t('paperDeleted'))
      },
    })
    return () => live.close()
  }, [ws, paperId, readOnlyVersion, load, receive, commit, refreshStatus, me.user.id, onChanged, t])

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

  const showResult = useCallback((res: BatchResult) => {
    if (res.editing?.length) setNotice(t('removedWhileEditing', { names: res.editing.map((e) => actorLabel(e.actor)).join(', ') }))
    if (res.not_restored?.length) setNotice(t('notRestored', { count: res.not_restored.length }))
  }, [t])

  // Sends ops. A conflict (someone changed a block since the revision an op
  // is based on) is not an error: it opens the conflict choice, with the
  // current state of those blocks applied so the user sees what changed.
  const run = useCallback(async (ops: Op[], force = false, beforeApply?: (res: BatchResult) => void): Promise<BatchResult | null> => {
    try {
      const res = await api.ops(ws, paperId, ops, force)
      beforeApply?.(res)
      receive(res.blocks ?? [], res.deleted ?? [], res.paper, true)
      const kids = (res.blocks ?? []).filter((b) => b.type === 'paper')
      if (kids.length) {
        const refs = await api.children(ws, paperId)
        const s = stateRef.current
        if (s) commit(applyChildren(s, Object.fromEntries(refs.map((k) => [k.id, k]))))
        onChanged()
      }
      showResult(res)
      refreshStatus()
      return res
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') {
        const current = e.conflicts.flatMap((c) => (c.current ? [c.current] : []))
        const gone = e.conflicts.filter((c) => !c.current).map((c) => c.block_id)
        const ed = editingRef.current
        const c: ConflictState = { ops, conflicts: e.conflicts, draft: ed?.draft }
        conflictRef.current = c
        setConflict(c)
        if (ed && gone.includes(ed.id)) {
          editingRef.current = null
          setEditing(null)
        }
        receive(current, gone, undefined, false)
      } else if (e instanceof ApiError && e.code === 'busy') {
        setNotice(t('busy'))
      } else {
        setNotice(t('saveFailed', { message: e instanceof Error ? e.message : String(e) }))
      }
      return null
    }
  }, [ws, paperId, receive, commit, onChanged, showResult, refreshStatus, t])

  const runOk = useCallback(async (ops: Op[]) => (await run(ops)) !== null, [run])

  const startEdit = useCallback((b: Block) => {
    const next: EditingState = {
      id: b.id, draft: b.content, type: b.type, props: b.props, base: b.rev,
      orig: { content: b.content, type: b.type, props: b.props }, textChanged: false,
    }
    editingRef.current = next
    setEditing(next)
  }, [])

  /**
   * Inserts a block: it shows at once under a temporary key and becomes
   * editable; the server's ID replaces the key when the answer comes.
   */
  const insertShown = useCallback((op: Op & { op: 'insert' }, extra: Op[] = [], edit = true): Promise<string | undefined> => {
    const cur = stateRef.current
    if (!cur) return Promise.resolve(undefined)
    const tmp = tempBlock(paperId, ws, me, orderAfter(topLevel(cur), op.after), op.type ?? 'paragraph', op.content ?? '', op.props ?? {})
    commit({ ...cur, blocks: new Map(cur.blocks).set(tmp.id, tmp) })
    if (edit) startEdit(tmp)
    const ref = tmp.id.slice(4)
    const p = run([...extra, { ...op, ref }], false, (res) => {
      // Before the real block lands: it takes over the temporary one's
      // React key, and the temporary block goes.
      const real = res.refs?.[ref]
      if (real) viewKeys.current.set(real, tmp.id)
      const s = stateRef.current
      if (s) commit(replaceTemp(s, tmp.id, undefined))
      const ed = editingRef.current
      const fresh = (res.blocks ?? []).find((b) => b.id === real)
      if (ed?.id === tmp.id && fresh) {
        const next = { ...ed, id: fresh.id, base: fresh.rev }
        editingRef.current = next
        setEditing(next)
      }
    }).then((res) => {
      pending.current.delete(tmp.id)
      const real = res?.refs?.[ref]
      const block = real ? stateRef.current?.blocks.get(real) : undefined
      const s = stateRef.current
      if (s) commit(replaceTemp(s, tmp.id, block))
      const ed = editingRef.current
      if (ed?.id === tmp.id) {
        if (block) {
          const next = { ...ed, id: block.id, base: block.rev }
          editingRef.current = next
          setEditing(next)
        } else {
          editingRef.current = null
          setEditing(null)
        }
      }
      return real
    })
    pending.current.set(tmp.id, p)
    return p
  }, [paperId, ws, me, commit, startEdit, run])

  /** Resolves a block ID that may still be a temporary key. */
  const realId = useCallback(async (id: string) => (id.startsWith('tmp:') ? pending.current.get(id) ?? Promise.resolve(undefined) : id), [])

  // Saves the edit in progress, based on the revision the edit started
  // from (not the latest one received since), so that someone else's change
  // in between is caught as a conflict instead of silently overwritten.
  const editOps = useCallback(async (): Promise<Op[] | null> => {
    const ed = editingRef.current
    if (!ed) return []
    if (conflictRef.current) return null // waiting for the user's choice
    const id = await realId(ed.id)
    if (!id) return []
    const now = editingRef.current ?? ed
    const changed = now.textChanged || now.orig.type !== now.type || JSON.stringify(now.orig.props) !== JSON.stringify(now.props)
    if (!changed) return []
    const base = editingRef.current?.base ?? now.base
    return [{ op: 'update', block_id: id, content: now.draft, type: now.type === now.orig.type ? undefined : now.type, props: now.props, base_rev: base }]
  }, [realId])

  // An editor that loses focus finishes only its own block: when Enter has
  // moved the edit to a new block, the old editor's blur must not send the
  // new block's half-typed text.
  const finishEdit = useCallback((from?: string) => {
    const ed = editingRef.current
    if (!ed || conflictRef.current) return
    if (from !== undefined && from !== ed.id) return
    void editOps().then(async (ops) => {
      if (ops === null) return
      if (ops.length && !(await runOk(ops))) return
      if (editingRef.current?.id === ed.id || editingRef.current?.id === (await realId(ed.id))) {
        editingRef.current = null
        setEditing(null)
      }
    })
  }, [editOps, runOk, realId])

  // Resolves the open conflict. "mine" resends the rejected ops with force
  // (a deleted block is inserted again); "theirs" drops the draft;
  // "continue" keeps the draft and rebases the edit on the current revision.
  const resolveConflict = useCallback((choice: 'mine' | 'theirs' | 'continue') => {
    const c = conflictRef.current
    if (!c) return
    const ed = editingRef.current
    const editingHit = ed ? c.conflicts.find((x) => x.block_id === ed.id) : undefined
    conflictRef.current = null
    setConflict(null)
    if (choice === 'continue') {
      if (ed && editingHit?.current) {
        const cur = editingHit.current
        const next = { ...ed, base: cur.rev, orig: { content: cur.content, type: cur.type, props: cur.props } }
        editingRef.current = next
        setEditing(next)
      }
      return
    }
    if (choice === 'theirs') {
      if (editingHit) {
        editingRef.current = null
        setEditing(null)
      }
      return
    }
    const gone = new Set(c.conflicts.filter((x) => !x.current).map((x) => x.block_id))
    const ops: Op[] = c.ops.map((op) => (op.block_id && gone.has(op.block_id) && op.op === 'update'
      ? { op: 'insert', type: op.type ?? 'paragraph', content: op.content, props: op.props }
      : op))
    void run(ops, true).then((res) => {
      if (res && editingHit && editingRef.current?.id === editingHit.block_id) {
        editingRef.current = null
        setEditing(null)
      }
    })
  }, [run])

  const ordered = useMemo(() => (state ? topLevel(state) : []), [state])
  const numbers = useMemo(() => listNumbers(ordered), [ordered])

  // Enter in the middle of a block: update it with the text before the
  // caret and insert a block with the rest after it, in one batch.
  const split = useCallback((before: string, after: string) => {
    const ed = editingRef.current
    if (!ed) return
    const continuing = CONTINUING.has(ed.type)
    if (continuing && before === '' && after === '') {
      // An empty list item ends the list instead of continuing it.
      const next = { ...ed, type: 'paragraph' as BlockType, props: {}, textChanged: true }
      editingRef.current = next
      finishEdit()
      return
    }
    const type = continuing ? ed.type : 'paragraph'
    const props = continuing ? { indent: ed.props.indent } : {}
    const prev = { ...ed, draft: before, textChanged: ed.textChanged || before !== ed.draft }
    editingRef.current = prev
    void (async () => {
      const id = await realId(ed.id)
      if (!id) return
      const update: Op[] = prev.textChanged || prev.orig.type !== prev.type
        ? [{ op: 'update', block_id: id, content: before, type: prev.type === prev.orig.type ? undefined : prev.type, props: prev.props, base_rev: prev.base }]
        : []
      await insertShown({ op: 'insert', type, content: after, props, after: id }, update)
    })()
  }, [finishEdit, insertShown, realId])

  // Backspace at the start: join this block's text onto the one above.
  const join = useCallback((text: string) => {
    const ed = editingRef.current
    if (!ed) return
    const idx = ordered.findIndex((b) => b.id === ed.id)
    const prev = idx > 0 ? ordered[idx - 1] : undefined
    void (async () => {
      const id = await realId(ed.id)
      if (!id) return
      editingRef.current = null
      setEditing(null)
      const ops: Op[] = [{ op: 'delete', block_id: id, base_rev: ed.base }]
      const joinable = prev && !['table', 'divider', 'paper', 'code', 'image', 'html', 'svg', 'mermaid'].includes(prev.type)
      if (joinable && text) ops.unshift({ op: 'update', block_id: prev.id, content: prev.content + text, base_rev: prev.rev })
      if (await runOk(ops)) {
        const fresh = prev ? stateRef.current?.blocks.get(prev.id) : undefined
        if (fresh && joinable) startEdit(fresh)
      }
    })()
  }, [ordered, runOk, realId, startEdit])

  const pasteParagraphs = useCallback((paragraphs: string[]) => {
    const ed = editingRef.current
    if (!ed) return
    void (async () => {
      const id = await realId(ed.id)
      if (!id) return
      const ops: Op[] = []
      let after = id
      paragraphs.forEach((p, i) => {
        ops.push({ op: 'insert', ref: `p${i}`, type: 'paragraph', content: p.replace(/[*_`[\\~]/g, (m) => `\\${m}`), after })
        after = `ref:p${i}`
      })
      const edit = await editOps()
      if (edit === null) return
      editingRef.current = null
      setEditing(null)
      await runOk([...edit, ...ops])
    })()
  }, [realId, editOps, runOk])

  const pasteImage = useCallback((file: File) => {
    const ed = editingRef.current
    void (async () => {
      try {
        const att = await api.upload(ws, paperId, file)
        const after = ed ? await realId(ed.id) : undefined
        await runOk([{ op: 'insert', type: 'image', content: '', props: { attachment_id: att.id }, after }])
      } catch (e) {
        setNotice(t('uploadFailed', { message: e instanceof Error ? e.message : String(e) }))
      }
    })()
  }, [ws, paperId, realId, runOk, t])

  const move = useCallback((b: Block, dir: -1 | 1) => {
    const idx = ordered.findIndex((x) => x.id === b.id)
    if (dir === -1 && idx > 0) {
      const anchor = idx >= 2 ? ordered[idx - 2]?.id : ''
      void run([{ op: 'move', block_id: b.id, after: anchor ?? '' }])
    }
    if (dir === 1 && idx >= 0 && idx < ordered.length - 1) {
      void run([{ op: 'move', block_id: b.id, after: ordered[idx + 1]?.id }])
    }
  }, [ordered, run])

  // Deleting a paper block deletes the child (creator) or archives it (D18).
  const remove = useCallback((b: Block) => {
    if (b.type === 'paper') {
      const ref = stateRef.current?.children.get(b.props.paper_id ?? '')
      const mine = ref?.created_by.user_id === me.user.id
      if (!window.confirm(mine ? t('confirmTrashChild', { title: ref?.title || t('untitled') }) : t('confirmArchiveChild'))) return
    }
    void run([{ op: 'delete', block_id: b.id, base_rev: b.rev }])
  }, [run, me.user.id, t])

  const setDraft = useCallback((text: string, changed: boolean) => {
    const ed = editingRef.current
    if (!ed) return
    let next: EditingState = { ...ed, draft: text, textChanged: changed }
    if (ed.type === 'paragraph') {
      const plain = plainText(text)
      const conv = detectShortcut(plain)
      if (conv) {
        const prefix = plain.length - conv.content.length
        next = { ...next, draft: stripPrefix(text, prefix, plain), type: conv.type, props: conv.props, textChanged: true }
      }
    }
    editingRef.current = next
    setEditing(next)
    if (next.type === 'divider') finishEdit()
  }, [finishEdit])

  const setDraftProps = useCallback((props: BlockProps) => {
    const ed = editingRef.current
    if (!ed) return
    const next = { ...ed, props }
    editingRef.current = next
    setEditing(next)
  }, [])

  const paper = state?.paper
  const canEdit = !readOnlyVersion && !!paper && !paper.deleted_at
  const owner = paper?.created_by.user_id === me.user.id

  const addBlock = (kind = 'paragraph') => {
    const last = ordered.filter((b) => !b.id.startsWith('tmp:')).pop()
    switch (kind) {
      case 'table':
        void run([{ op: 'insert', type: 'table', props: { header_row: true }, rows: 2, cols: 2, after: last?.id }])
        return
      case 'paper': {
        const title = window.prompt(t('childPaperTitle'))
        if (title === null) return
        void run([{ op: 'insert', type: 'paper', title, after: last?.id }])
        return
      }
      case 'image': {
        const input = document.createElement('input')
        input.type = 'file'
        input.accept = 'image/png,image/jpeg,image/gif,image/webp'
        input.onchange = () => {
          const f = input.files?.[0]
          if (f) pasteImage(f)
        }
        input.click()
        return
      }
      default:
        void insertShown({ op: 'insert', type: 'paragraph', content: '', after: last?.id })
    }
  }

  const saveTitle = () => {
    if (!state || title.trim() === state.paper.title) return
    void run([{ op: 'set_title', title: title.trim() }])
  }

  const saveVersion = async () => {
    const note = window.prompt(t('versionNotePrompt'), '')
    if (note === null) return
    finishEdit()
    try {
      const v = await api.saveVersion(ws, paperId, note)
      setNotice(v ? t('versionSaved', { n: v.number }) : t('versionUnchanged'))
      refreshStatus()
      onChanged()
    } catch (e) {
      setNotice(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }

  // Restoring loses unsaved changes, so offer to save them first.
  const restore = async (n: number) => {
    const st = await api.status(ws, paperId)
    if (st.changed && st.version_number !== n) {
      if (window.confirm(t('saveBeforeRestore'))) {
        const note = window.prompt(t('versionNotePrompt'), '')
        if (note === null) return
        await api.saveVersion(ws, paperId, note)
      } else if (!window.confirm(t('discardAndRestore', { n }))) {
        return
      }
    } else if (!window.confirm(t('confirmRestore', { n }))) {
      return
    }
    try {
      const res = await api.restoreVersion(ws, paperId, n)
      showResult(res)
      onChanged()
      await load()
      navigate(`/ws/${ws}/p/${paperId}`)
    } catch (e) {
      setNotice(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }

  const restoreFromTrash = async () => {
    try {
      await api.restorePaper(ws, paperId)
      setNotice(t('restoredFromTrash'))
      onChanged()
      await load()
    } catch (e) {
      setNotice(errorMessage(e, t))
    }
  }

  const print = async () => {
    // Print needs the whole paper, not only the ranges read so far.
    const all = await loadAllBlocks(ws, paperId)
    const s = stateRef.current
    if (s) commit(applyChildren(applyBlocks(s, all.blocks), all.papers))
    window.setTimeout(() => window.print(), 50)
  }

  if (fatal) return <div className="desk-empty">{fatal}</div>
  if (!state || !view || !paper) return null

  const ctx: BlockContext = {
    ws, state, canEdit, editing, conflict, recent, numbers, version: shown?.number,
    actions: { startEdit, setDraft, setDraftProps, finishEdit, split, join, pasteParagraphs, pasteImage, run: runOk, move, remove, resolveConflict },
  }
  // A conflict shows inside the block being edited; any other conflict (a
  // deleted block, a toolbar action) shows above the sheet.
  const inlineConflict = conflict && editing && conflict.conflicts.some((c) => c.block_id === editing.id && c.current)
  const base = `/ws/${ws}/p/${paperId}`

  return (
    <>
      <nav className="crumbs" aria-label={t('location')}>
        <Link to={`/ws/${ws}`}>{view.workspace.emoji ? `${view.workspace.emoji} ` : ''}{view.workspace.name}</Link>
        {(view.path ?? []).slice(1).map((p) => (
          <span key={p.id}> / <Link to={`/ws/${ws}/p/${p.id}`}>{p.title || t('untitled')}</Link></span>
        ))}
        {!view.is_home && <span> / {paper.title || t('untitled')}</span>}
      </nav>

      <div className="paper-bar">
        {!readOnlyVersion && <span className={`status ${conn === 'open' ? 'open' : ''}`}>{conn === 'open' ? t('connectionOpen') : t('connectionLost')}</span>}
        {readOnlyVersion ? (
          <span className="version-flag">{t('showingVersion', { n: shown?.number ?? '' })} · {shown && actorLabel(shown.created_by, t('viaApi'))}</span>
        ) : paper.version_number === 0 ? (
          <span className="badge badge-quiet">{t('statusDraft')}</span>
        ) : (
          <span className="badge">{status?.changed ? t('changedSince', { n: paper.version_number }) : t('atVersion', { n: paper.version_number })}</span>
        )}
        {paper.archived_at && <span className="badge">{t('statusArchived')}</span>}
        {paper.visibility === 'private' && <span className="badge badge-quiet">{t('private')}</span>}
        <span className="spacer" />
        {readOnlyVersion ? (
          <>
            <Link className="btn btn-quiet" to={base}>{t('openCurrent')}</Link>
            {shown && <button type="button" className="btn btn-quiet" onClick={() => void restore(shown.number)}>{t('restoreThisVersion')}</button>}
          </>
        ) : (
          <>
            {canEdit && <button type="button" className="btn btn-primary" onClick={() => void saveVersion()}>{t('saveVersion')}</button>}
            <button type="button" className="btn btn-quiet" onClick={() => setPanel(panel === 'versions' ? null : 'versions')}>{t('versions')}</button>
            {paper.version_number > 0 && <Link className="btn btn-quiet" to={`${base}/latest`}>{t('latestVersion')}</Link>}
          </>
        )}
        <button type="button" className="btn btn-quiet" onClick={() => void print()}>{t('print')}</button>
        {!isDemo && <a className="btn btn-quiet" href={api.markdownURL(ws, paperId)} download={`${paper.title || 'paper'}.md`}>{t('exportMarkdown')}</a>}
        {!readOnlyVersion && (
          <PaperMenu ws={ws} paper={paper} isHome={view.is_home} owner={owner} onChanged={() => { onChanged(); void load() }} onNotice={setNotice} />
        )}
      </div>

      {paper.deleted_at && (
        <div className="notice" role="status">
          <span>{t('inTrash')}</span>
          {owner && <button type="button" className="btn" onClick={() => void restoreFromTrash()}>{t('restoreFromTrash')}</button>}
        </div>
      )}
      {isDemo && canEdit && <div className="notice demo-hint">{t('demoHint')}</div>}
      {notice && (
        <div className="notice" role="status">
          <span>{notice}</span>
          <button type="button" className="btn btn-quiet" onClick={() => setNotice(undefined)}>{t('close')}</button>
        </div>
      )}

      {conflict && !inlineConflict && <div className="conflict-banner"><ConflictBox conflict={conflict} ctx={ctx} /></div>}

      <article className="sheet">
        <input
          className="sheet-title"
          value={title}
          placeholder={t('untitled')}
          aria-label={t('paperTitle')}
          readOnly={!canEdit}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={saveTitle}
          onKeyDown={(e) => {
            if (isEnter(e)) (e.target as HTMLInputElement).blur()
          }}
        />
        {view.is_home && view.workspace.description && <p className="ws-rules">{view.workspace.description}</p>}
        {ordered.length === 0 && <p className="sheet-empty">{t('emptyPaper')}</p>}
        {ordered.map((b) => <BlockView key={viewKeys.current.get(b.id) ?? b.id} block={b} ctx={ctx} />)}
        {canEdit && (
          <div className="add-block">
            <button type="button" className="btn btn-quiet" onClick={() => addBlock()}>+ {t('addBlock')}</button>
            {INSERT_CHOICES.filter((c) => c.key !== 'paper' || paper.visibility === 'public').map((c) => (
              <button key={c.key} type="button" className="btn btn-quiet" onClick={() => addBlock(c.key)}>+ {t(c.label)}</button>
            ))}
          </div>
        )}
      </article>

      {panel === 'versions' && (
        <VersionsPanel ws={ws} paper={paperId} latest={paper.version_number} onClose={() => setPanel(null)} onRestore={canEdit ? (n) => void restore(n) : undefined} />
      )}
    </>
  )
}
