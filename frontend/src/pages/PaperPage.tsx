import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { ApiError, api } from '../api/client'
import { LiveConnection } from '../api/live'
import { applyBlocks, applyPaper, initState, listNumbers, topLevel, type PaperState } from '../api/paperState'
import type { Block, Op, Paper, PaperEvent } from '../api/types'
import { BlockView, ConflictBox, type BlockContext, type ConflictState, type EditingState } from '../components/BlockView'
import { CONTINUING } from '../components/blockTypes'
import { HistoryPanel } from '../components/HistoryPanel'
import { SharePanel } from '../components/SharePanel'
import { detectShortcut } from '../components/shortcuts'
import { useTranslation } from '../i18n'
import { isDemo } from '../utils/demo'
import { isEnter } from '../utils/keyboard'

const RECENT_MS = 4000

export function PaperPage({ onChanged }: { onChanged: () => void }) {
  const { t } = useTranslation()
  const { ws = '', paper: paperId = '' } = useParams()
  const navigate = useNavigate()

  const [state, setState] = useState<PaperState>()
  const [role, setRole] = useState<string>('')
  const [status, setStatus] = useState<'connecting' | 'open' | 'closed'>('connecting')
  const [notice, setNotice] = useState<string>()
  const [fatal, setFatal] = useState<string>()
  const [editing, setEditing] = useState<EditingState | null>(null)
  const [recent, setRecent] = useState<Set<string>>(new Set())
  const [conflict, setConflict] = useState<ConflictState | null>(null)
  const [panel, setPanel] = useState<'history' | 'share' | null>(null)
  const [title, setTitle] = useState('')

  const stateRef = useRef<PaperState | undefined>(undefined)
  stateRef.current = state
  const editingRef = useRef<EditingState | null>(null)
  editingRef.current = editing
  const conflictRef = useRef<ConflictState | null>(null)
  conflictRef.current = conflict

  const load = useCallback(async () => {
    try {
      const v = await api.getPaper(ws, paperId)
      setState(initState(v.paper, v.blocks))
      setRole(v.role)
      setTitle(v.paper.title)
    } catch (e) {
      setFatal(e instanceof Error ? e.message : String(e))
    }
  }, [ws, paperId])

  const markRecent = useCallback((ids: string[]) => {
    if (ids.length === 0) return
    setRecent((prev) => new Set([...prev, ...ids]))
    window.setTimeout(() => setRecent((prev) => {
      const next = new Set(prev)
      ids.forEach((id) => next.delete(id))
      return next
    }), RECENT_MS)
  }, [])

  // Applies resulting block states (from our own commit or the change
  // feed). Changes made by others are highlighted for a moment. If someone
  // deletes the block being edited, the draft is kept as a conflict so it
  // can be restored instead of vanishing with the block.
  const receive = useCallback((put: Block[], deleted: string[], paper: Paper | undefined, own: boolean) => {
    const cur = stateRef.current
    if (!cur) return
    const ed = editingRef.current
    if (!own && ed && deleted.includes(ed.id)) {
      const c: ConflictState = {
        ops: [{ op: 'update', block_id: ed.id, content: ed.draft, type: ed.type, props: ed.props, base_version: ed.base }],
        message: '', conflicts: [{ block_id: ed.id, base_version: ed.base, current: null }], draft: ed.draft,
      }
      conflictRef.current = c
      setConflict(c)
      editingRef.current = null
      setEditing(null)
    }
    let next = applyBlocks(cur, put, deleted)
    if (paper) next = applyPaper(next, paper)
    if (next === cur) return
    stateRef.current = next
    setState(next)
    if (paper && !editingRef.current) setTitle(paper.title)
    if (!own) markRecent(put.filter((b) => next.blocks.get(b.id) === b).map((b) => b.id))
  }, [markRecent])

  // Initial load and live connection.
  useEffect(() => {
    setState(undefined)
    setFatal(undefined)
    setEditing(null)
    void load()
    setConflict(null)
    const conn = new LiveConnection(api.liveURL(ws, paperId), {
      // Reload after (re)connecting: events may have been missed.
      onHello: () => void load(),
      onStatus: setStatus,
      onEvent: (ev: PaperEvent) => {
        const cur = stateRef.current
        if (!cur) return
        switch (ev.type) {
          case 'block_put':
            if (ev.block) receive([ev.block], [], undefined, false)
            break
          case 'block_deleted':
            if (ev.block_id) receive([], [ev.block_id], undefined, false)
            break
          case 'paper_updated':
            if (ev.paper) receive([], [], ev.paper, false)
            break
        }
      },
      onClosing: (reason) => {
        if (reason === 'access revoked') setFatal(t('accessRevoked'))
        if (reason === 'paper deleted') setFatal(t('paperDeleted'))
      },
    })
    return () => conn.close()
  }, [ws, paperId, load, receive, t])

  // Commits ops. A conflict (someone changed a block since the version an
  // op is based on) is not an error: it opens the conflict choice, with the
  // current state of those blocks applied so the user sees what changed.
  const run = useCallback(async (ops: Op[], message = '', force = false): Promise<boolean> => {
    try {
      const res = await api.commit(ws, paperId, ops, { message, force })
      if (res.commit) {
        receive(res.blocks, res.deleted, res.paper, true)
        if (res.commit.changes.some((c) => (c.overwrote_version ?? 0) > 0)) setNotice(t('overwroteNotice'))
        onChanged()
      }
      return true
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') {
        const current = e.conflicts.flatMap((c) => (c.current ? [c.current] : []))
        const gone = e.conflicts.filter((c) => !c.current).map((c) => c.block_id)
        const ed = editingRef.current
        const c: ConflictState = { ops, message, conflicts: e.conflicts, draft: ed?.draft }
        conflictRef.current = c
        setConflict(c)
        if (ed && gone.includes(ed.id)) {
          editingRef.current = null
          setEditing(null)
        }
        receive(current, gone, undefined, false)
      } else {
        setNotice(t('saveFailed', { message: e instanceof Error ? e.message : String(e) }))
      }
      return false
    }
  }, [ws, paperId, receive, onChanged, t])

  const startEdit = useCallback((b: Block) => {
    const next: EditingState = { id: b.id, draft: b.content, type: b.type, props: b.props, base: b.version, orig: { content: b.content, type: b.type, props: b.props } }
    editingRef.current = next
    setEditing(next)
  }, [])

  // Saves the edit in progress. Returns false if the save failed (the
  // editor then stays open with the draft).
  // Saves the edit in progress, based on the version the edit started
  // from (not the latest one received since), so that someone else's change
  // in between is caught as a conflict instead of silently overwritten.
  const saveEditing = useCallback(async (extra: Op[] = []): Promise<boolean> => {
    const ed = editingRef.current
    const cur = stateRef.current
    if (!ed || !cur) return true
    if (conflictRef.current) return false // waiting for the user's choice
    const b = cur.blocks.get(ed.id)
    const ops: Op[] = []
    // Compare with the block as it was when the edit started: an untouched
    // draft sends nothing, even if someone else changed the block meanwhile.
    const changed = ed.orig.content !== ed.draft || ed.orig.type !== ed.type || JSON.stringify(ed.orig.props) !== JSON.stringify(ed.props)
    if (b && changed) {
      ops.push({ op: 'update', block_id: b.id, content: ed.draft, type: ed.type === b.type ? undefined : ed.type, props: ed.props, base_version: ed.base })
    }
    ops.push(...extra)
    if (ops.length === 0) return true
    return run(ops, '')
  }, [run])

  const finishEdit = useCallback(() => {
    const ed = editingRef.current
    if (!ed || conflictRef.current) return
    void saveEditing().then((ok) => {
      if (!ok) return
      if (editingRef.current?.id === ed.id) {
        editingRef.current = null
        setEditing(null)
      }
    })
  }, [saveEditing])

  // Resolves the open conflict. "mine" resends the rejected ops with force
  // (restoring deleted blocks first); "theirs" drops the draft; "continue"
  // keeps the draft and rebases the edit on the current version.
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
        const next = { ...ed, base: cur.version, orig: { content: cur.content, type: cur.type, props: cur.props } }
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
    const restores: Op[] = c.conflicts.filter((x) => !x.current).map((x) => ({ op: 'restore', block_id: x.block_id, version: x.base_version }))
    void run([...restores, ...c.ops], c.message, true).then((ok) => {
      if (ok && editingHit && editingRef.current?.id === editingHit.block_id) {
        editingRef.current = null
        setEditing(null)
      }
    })
  }, [run])

  const ordered = useMemo(() => (state ? topLevel(state) : []), [state])
  const numbers = useMemo(() => listNumbers(ordered), [ordered])

  const splitAfter = useCallback(() => {
    const ed = editingRef.current
    if (!ed) return
    const id = crypto.randomUUID()
    const continuing = CONTINUING.has(ed.type)
    // An empty list item ends the list instead of continuing it.
    if (continuing && ed.draft === '') {
      editingRef.current = { ...ed, type: 'paragraph', props: {} }
      finishEdit()
      return
    }
    const type = continuing ? ed.type : 'paragraph'
    const props = continuing ? { indent: ed.props.indent } : {}
    void saveEditing([{ op: 'insert', block_id: id, type, content: '', props, after: ed.id }]).then((ok) => {
      if (!ok) return
      const nb = stateRef.current?.blocks.get(id)
      if (nb) startEdit(nb)
    })
  }, [saveEditing, startEdit, finishEdit])

  const removeEmpty = useCallback(() => {
    const ed = editingRef.current
    if (!ed) return
    const idx = ordered.findIndex((b) => b.id === ed.id)
    const prev = idx > 0 ? ordered[idx - 1] : undefined
    editingRef.current = null
    setEditing(null)
    void run([{ op: 'delete', block_id: ed.id, base_version: ed.base }]).then((ok) => {
      if (ok && prev && prev.type !== 'table' && prev.type !== 'divider') {
        const fresh = stateRef.current?.blocks.get(prev.id)
        if (fresh) startEdit(fresh)
      }
    })
  }, [ordered, run, startEdit])

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

  const setDraft = useCallback((text: string) => {
    const ed = editingRef.current
    if (!ed) return
    let next: EditingState = { ...ed, draft: text }
    if (ed.type === 'paragraph') {
      const conv = detectShortcut(text)
      if (conv) next = { ...ed, draft: conv.content, type: conv.type, props: conv.props }
    }
    editingRef.current = next
    setEditing(next)
    if (next.type === 'divider') finishEdit()
  }, [finishEdit])

  const setDraftProps = useCallback((props: EditingState['props']) => {
    const ed = editingRef.current
    if (!ed) return
    const next = { ...ed, props }
    editingRef.current = next
    setEditing(next)
  }, [])

  const canEdit = role === 'editor' || role === 'owner'

  const addBlock = () => {
    const id = crypto.randomUUID()
    const last = ordered[ordered.length - 1]
    void run([{ op: 'insert', block_id: id, type: 'paragraph', content: '', after: last?.id }]).then((ok) => {
      const nb = ok ? stateRef.current?.blocks.get(id) : undefined
      if (nb) startEdit(nb)
    })
  }

  const saveTitle = () => {
    if (!state || title.trim() === state.paper.title) return
    void run([{ op: 'set_title', title: title.trim() }])
  }

  const deletePaper = async () => {
    if (!state || !window.confirm(t('confirmDeletePaper', { title: state.paper.title || t('untitled') }))) return
    try {
      await api.deletePaper(ws, paperId)
      onChanged()
      navigate(`/w/${ws}`)
    } catch (e) {
      setNotice(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }

  if (fatal) return <div className="desk-empty">{fatal}</div>
  if (!state) return null

  const ctx: BlockContext = {
    state, canEdit, editing, conflict, recent, numbers,
    actions: { startEdit, setDraft, setDraftProps, finishEdit, splitAfter, removeEmpty, run, move, resolveConflict },
  }
  // A conflict shows inside the block being edited; any other conflict (a
  // deleted block, a toolbar action) shows above the sheet.
  const inlineConflict = conflict && editing && conflict.conflicts.some((c) => c.block_id === editing.id && c.current)

  return (
    <>
      <div className="paper-bar">
        <span className={`status ${status === 'open' ? 'open' : ''}`}>{status === 'open' ? t('connectionOpen') : t('connectionLost')}</span>
        <span>{t('updated', { when: new Date(state.paper.updated_at).toLocaleString() })}</span>
        {!canEdit && <span>{t('readOnly')}</span>}
        <span className="spacer" />
        <button type="button" className="btn btn-quiet" onClick={() => setPanel(panel === 'history' ? null : 'history')}>{t('history')}</button>
        {role === 'owner' && <button type="button" className="btn btn-quiet" onClick={() => setPanel(panel === 'share' ? null : 'share')}>{t('share')}</button>}
        {!isDemo && <a className="btn btn-quiet" href={api.markdownURL(ws, paperId)} download={`${state.paper.title || 'paper'}.md`}>{t('exportMarkdown')}</a>}
        {role === 'owner' && !isDemo && <button type="button" className="btn btn-quiet btn-danger" onClick={() => void deletePaper()}>{t('deletePaper')}</button>}
      </div>

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
          aria-label={t('untitled')}
          readOnly={!canEdit}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={saveTitle}
          onKeyDown={(e) => {
            if (isEnter(e)) (e.target as HTMLInputElement).blur()
          }}
        />
        {ordered.length === 0 && <p className="sheet-empty">{t('emptyPaper')}</p>}
        {ordered.map((b) => <BlockView key={b.id} block={b} ctx={ctx} />)}
        {canEdit && (
          <div className="add-block">
            <button type="button" className="btn btn-quiet" onClick={addBlock}>+ {t('addBlock')}</button>
          </div>
        )}
      </article>

      {panel === 'history' && (
        <HistoryPanel
          ws={ws}
          paper={paperId}
          state={state}
          onClose={() => setPanel(null)}
          onRestore={canEdit ? (block, version) => {
            const cur = state.blocks.get(block)
            void run([{ op: 'restore', block_id: block, version, base_version: cur?.version }])
          } : undefined}
        />
      )}
      {panel === 'share' && <SharePanel ws={ws} paper={state.paper} onClose={() => setPanel(null)} onSaved={() => void load()} />}
    </>
  )
}
