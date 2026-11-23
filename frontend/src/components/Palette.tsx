import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router'
import { api } from '../api/client'
import type { PaperRef, SearchHit } from '../api/types'
import { useTranslation } from '../i18n'
import { isEnter } from '../utils/keyboard'

export interface Command {
  label: string
  icon: string
  run: () => void
}

function highlight(text: string, q: string): ReactNode {
  const i = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1
  if (i < 0) return text.slice(0, 120)
  const start = Math.max(0, i - 30)
  return <>{start > 0 ? '…' : ''}{text.slice(start, i)}<mark>{text.slice(i, i + q.length)}</mark>{text.slice(i + q.length, i + q.length + 80)}</>
}

type Row = { kind: 'paper'; ref: PaperRef } | { kind: 'hit'; hit: SearchHit } | { kind: 'cmd'; cmd: Command }

/** Search and commands in one box (Mod+K). Tab switches to "under this paper" (spec: R14). */
export function Palette({ ws, wsName, under, recent, commands, onClose }: {
  ws: string
  wsName: string
  /** The open paper, when it can scope a search. */
  under?: { id: string; title: string }
  recent: PaperRef[]
  commands: Command[]
  onClose: () => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [q, setQ] = useState('')
  const [scope, setScope] = useState<'all' | 'under'>('all')
  const [hits, setHits] = useState<SearchHit[]>([])
  const [active, setActive] = useState(0)
  const listRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const query = q.trim()
    if (!query) {
      setHits([])
      return
    }
    let live = true
    const timer = window.setTimeout(() => {
      void api.search(ws, query, scope === 'under' ? under?.id ?? '' : '').then((h) => { if (live) setHits(h) }).catch(() => { if (live) setHits([]) })
    }, 120)
    return () => { live = false; window.clearTimeout(timer) }
  }, [q, scope, ws, under])

  const rows: Row[] = useMemo(() => {
    const query = q.trim().toLowerCase()
    const cmds = commands.filter((c) => !query || c.label.toLowerCase().includes(query)).map((c) => ({ kind: 'cmd' as const, cmd: c }))
    if (!query) return [...recent.slice(0, 5).map((r) => ({ kind: 'paper' as const, ref: r })), ...cmds]
    return [...hits.map((h) => ({ kind: 'hit' as const, hit: h })), ...cmds]
  }, [q, hits, recent, commands])

  useEffect(() => setActive(0), [q, scope])
  useEffect(() => {
    listRef.current?.querySelector('.pr.hi')?.scrollIntoView({ block: 'nearest' })
  }, [active])

  const pick = (r: Row | undefined) => {
    if (!r) return
    onClose()
    if (r.kind === 'cmd') r.cmd.run()
    else if (r.kind === 'paper') navigate(`/ws/${ws}/p/${r.ref.id}`)
    else navigate(`/ws/${ws}/p/${r.hit.paper.id}${r.hit.block_id ? `#${r.hit.block_id}` : ''}`)
  }

  const recentCount = !q.trim() ? Math.min(recent.length, 5) : hits.length
  return createPortal(
    <div className="scrim" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div className="pal" role="dialog" aria-label={t('search')}>
        <div className="pal-in">
          <span className="muted" aria-hidden style={{ fontSize: 18 }}>⌕</span>
          <input autoFocus value={q} placeholder={t('palettePlaceholder')} aria-label={t('search')} autoComplete="off"
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowDown') { e.preventDefault(); setActive((a) => Math.min(rows.length - 1, a + 1)) }
              else if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(0, a - 1)) }
              else if (isEnter(e)) { e.preventDefault(); pick(rows[active]) }
              else if (e.key === 'Escape') { e.preventDefault(); onClose() }
              else if (e.key === 'Tab' && under) { e.preventDefault(); setScope(scope === 'all' ? 'under' : 'all') }
            }} />
          <span className="kbd">Esc</span>
        </div>
        <div className="scope">
          <button type="button" className={scope === 'all' ? 'on' : ''} onClick={() => setScope('all')}>{t('scopeAll', { ws: wsName })}</button>
          {under && <button type="button" className={scope === 'under' ? 'on' : ''} onClick={() => setScope('under')}>{t('scopeUnder', { title: under.title || t('untitled') })}</button>}
        </div>
        <div className="pal-list" ref={listRef} role="listbox">
          {recentCount > 0 && <div className="mh">{q.trim() ? `${t('results')} · ${hits.length}` : t('recent')}</div>}
          {q.trim() && hits.length === 0 && <div className="empty">{t('noMatch')}</div>}
          {rows.map((r, i) => {
            const head = r.kind === 'cmd' && (i === 0 || rows[i - 1]?.kind !== 'cmd') ? <div className="mh">{t('commands')}</div> : null
            const cls = `pr${i === active ? ' hi' : ''}`
            const body = r.kind === 'cmd'
              ? <span className="pt"><span aria-hidden>{r.cmd.icon}</span><span>{r.cmd.label}</span></span>
              : r.kind === 'paper'
                ? <span className="pt">{r.ref.visibility === 'private' ? '🔒 ' : ''}{r.ref.title || t('untitled')}</span>
                : (
                  <>
                    <span className="pt">{highlight(r.hit.paper.title || t('untitled'), q.trim())}</span>
                    {r.hit.block_id && <span className="ps">{highlight(r.hit.text, q.trim())}</span>}
                  </>
                )
            return (
              <div key={`${r.kind}-${i}`}>
                {head}
                <button type="button" role="option" aria-selected={i === active} className={cls} onMouseMove={() => setActive(i)} onClick={() => pick(r)}>{body}</button>
              </div>
            )
          })}
        </div>
        <div className="pal-foot"><span>↑↓ {t('navigate')}</span><span>Enter {t('openKey')}</span>{under && <span>Tab {t('scopeUnder', { title: '' }).replace(/[「」“”]/g, '').trim()}</span>}</div>
      </div>
    </div>,
    document.body,
  )
}
