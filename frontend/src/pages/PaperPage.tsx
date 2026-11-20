import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { ApiError, api } from '../api/client'
import { LiveConnection } from '../api/live'
import { actorLabel, applyCommit, applyLock, applyUnlock, initState, listNumbers, topLevel, type PaperState } from '../api/paperState'
import type { Block, BlockLock, Commit, Op, PaperEvent } from '../api/types'
import { BlockView, type BlockContext, type EditingState } from '../components/BlockView'
import { CONTINUING } from '../components/blockTypes'
import { HistoryPanel } from '../components/HistoryPanel'
import { SharePanel } from '../components/SharePanel'
import { detectShortcut } from '../components/shortcuts'
import { useTranslation } from '../i18n'
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
  const [now, setNow] = useState(() => Date.now())
  const [panel, setPanel] = useState<'history' | 'share' | null>(null)
  const [title, setTitle] = useState('')

  const stateRef = useRef<PaperState | undefined>(undefined)
  stateRef.current = state
  const editingRef = useRef<EditingState | null>(null)
  editingRef.current = editing
  const connRef = useRef<LiveConnection | undefined>(undefined)
  const myKeyRef = useRef('')

  const load = useCallback(async () => {
    try {
      const v = await api.getPaper(ws, paperId)
      setState(initState(v.paper, v.blocks, v.locks))
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

  const receiveCommit = useCallback((commit: Commit, own: boolean) => {
    const cur = stateRef.current
    if (!cur) return
    const { state: next, outcome } = applyCommit(cur, commit)
    if (outcome === 'gap') {
      void load()
      return
    }
    if (outcome !== 'applied') return
    stateRef.current = next
    setState(next)
    if (!editingRef.current) setTitle(next.paper.title)
    if (!own) markRecent(commit.changes.map((c) => c.block_id ?? '').filter(Boolean))
  }, [load, markRecent])

  // Initial load and live connection.
  useEffect(() => {
    setState(undefined)
    setFatal(undefined)
    setEditing(null)
    void load()
    const conn = new LiveConnection(api.liveURL(ws, paperId), {
      onHello: (id) => {
        myKeyRef.current = id
        // Reload after (re)connecting: events may have been missed.
        void load()
      },
      onStatus: setStatus,
      onEvent: (ev: PaperEvent) => {
        const cur = stateRef.current
        if (!cur) return
        switch (ev.type) {
          case 'commit':
            if (ev.commit) receiveCommit(ev.commit, false)
            break
          case 'lock':
            if (ev.lock) setState((s) => (s ? applyLock(s, ev.lock as BlockLock) : s))
            break
          case 'unlock':
            if (ev.block_id) setState((s) => (s ? applyUnlock(s, ev.block_id as string, ev.holder_key ?? '') : s))
            break
          case 'access_changed':
            void load()
            break
        }
      },
      onLockResult: (blockId, ok, lock) => {
        const ed = editingRef.current
        if (ok || !ed || ed.id !== blockId || ed.forced) return
        // Someone else got there first: stop editing, keep their lock visible.
        setEditing(null)
        if (lock) setState((s) => (s ? applyLock(s, lock) : s))
      },
      onLockLost: (blockId, lock) => {
        if (lock) setState((s) => (s ? applyLock(s, lock) : s))
        if (editingRef.current?.id === blockId) setNotice(t('lockLost'))
      },
      onClosing: (reason) => {
        if (reason === 'access revoked') setFatal(t('accessRevoked'))
        if (reason === 'paper deleted') setFatal(t('paperDeleted'))
      },
    })
    connRef.current = conn
    return () => conn.close()
  }, [ws, paperId, load, receiveCommit, t])

  // Re-evaluate lock expiry.
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 3000)
    return () => window.clearInterval(id)
  }, [])

  const run = useCallback(async (ops: Op[], message = '', force = false): Promise<boolean> => {
    try {
      const res = await api.commit(ws, paperId, ops, { message, force, connectionId: myKeyRef.current })
      if (res.commit) {
        receiveCommit(res.commit, true)
        if (res.commit.changes.some((c) => (c.overwrote_version ?? 0) > 0)) setNotice(t('overwroteNotice'))
        onChanged()
      }
      return true
    } catch (e) {
      if (e instanceof ApiError && e.code === 'locked') {
        const editingId = editingRef.current?.id
        const lock = e.lock ?? (editingId ? stateRef.current?.locks.get(editingId) : undefined)
        setNotice(t('lockedSaveFailed', { name: lock ? actorLabel(lock.holder) : '?' }))
      } else {
        setNotice(t('saveFailed', { message: e instanceof Error ? e.message : String(e) }))
      }
      return false
    }
  }, [ws, paperId, receiveCommit, onChanged, t])

  const startEdit = useCallback((b: Block, force = false) => {
    const prev = editingRef.current
    if (prev && prev.id !== b.id) connRef.current?.unlock(prev.id)
    const next = { id: b.id, draft: b.content, type: b.type, props: b.props, forced: force }
    editingRef.current = next
    setEditing(next)
    connRef.current?.lock(b.id)
  }, [])

  // Saves the edit in progress. Returns false if the save failed (the
  // editor then stays open with the draft).
  const saveEditing = useCallback(async (extra: Op[] = []): Promise<boolean> => {
    const ed = editingRef.current
    const cur = stateRef.current
    if (!ed || !cur) return true
    const b = cur.blocks.get(ed.id)
    const ops: Op[] = []
    if (b && (b.content !== ed.draft || b.type !== ed.type || JSON.stringify(b.props) !== JSON.stringify(ed.props))) {
      ops.push({ op: 'update', block_id: b.id, content: ed.draft, type: ed.type === b.type ? undefined : ed.type, props: ed.props, base_version: b.version })
    }
    ops.push(...extra)
    if (ops.length === 0) return true
    const ok = await run(ops, '', ed.forced)
    if (!ok) editingRef.current = { ...ed, forced: true }
    return ok
  }, [run])

  const finishEdit = useCallback(() => {
    const ed = editingRef.current
    if (!ed) return
    void saveEditing().then((ok) => {
      if (!ok) {
        setEditing(editingRef.current)
        return
      }
      if (editingRef.current?.id === ed.id) {
        editingRef.current = null
        setEditing(null)
      }
      connRef.current?.unlock(ed.id)
    })
  }, [saveEditing])

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
      connRef.current?.unlock(ed.id)
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
    void run([{ op: 'delete', block_id: ed.id }]).then((ok) => {
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
    state, myKey: myKeyRef.current, canEdit, now, editing, recent, numbers,
    actions: { startEdit, setDraft, setDraftProps, finishEdit, splitAfter, removeEmpty, run, move },
  }

  return (
    <>
      <div className="paper-bar">
        <span className={`status ${status === 'open' ? 'open' : ''}`}>{status === 'open' ? t('connectionOpen') : t('connectionLost')}</span>
        <span>{t('revision', { n: state.paper.revision })}</span>
        {!canEdit && <span>{t('readOnly')}</span>}
        <span className="spacer" />
        <button type="button" className="btn btn-quiet" onClick={() => setPanel(panel === 'history' ? null : 'history')}>{t('history')}</button>
        {role === 'owner' && <button type="button" className="btn btn-quiet" onClick={() => setPanel(panel === 'share' ? null : 'share')}>{t('share')}</button>}
        <a className="btn btn-quiet" href={api.markdownURL(ws, paperId)} download={`${state.paper.title || 'paper'}.md`}>{t('exportMarkdown')}</a>
        {role === 'owner' && <button type="button" className="btn btn-quiet btn-danger" onClick={() => void deletePaper()}>{t('deletePaper')}</button>}
      </div>

      {notice && (
        <div className="notice" role="status">
          <span>{notice}</span>
          <button type="button" className="btn btn-quiet" onClick={() => setNotice(undefined)}>{t('close')}</button>
        </div>
      )}

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

      {panel === 'history' && <HistoryPanel ws={ws} paper={paperId} revision={state.paper.revision} onClose={() => setPanel(null)} />}
      {panel === 'share' && <SharePanel ws={ws} paper={state.paper} onClose={() => setPanel(null)} onSaved={() => void load()} />}
    </>
  )
}
