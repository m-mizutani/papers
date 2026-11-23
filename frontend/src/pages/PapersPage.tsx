import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { api } from '../api/client'
import type { PaperListItem, PaperListQuery } from '../api/types'
import { ago, useTranslation } from '../i18n'
import { actorName } from '../utils/actors'
import { isEnter } from '../utils/keyboard'
import { readPref, writePref } from '../utils/prefs'

type Filters = Required<Pick<PaperListQuery, 'place' | 'creator' | 'visibility' | 'archived' | 'sort'>>
const DEFAULTS: Filters = { place: 'unfiled', creator: 'me', visibility: 'all', archived: false, sort: 'updated' }

function stored(): Filters {
  try {
    return { ...DEFAULTS, ...(JSON.parse(readPref('papersFilters') ?? '{}') as Partial<Filters>) }
  } catch {
    return DEFAULTS
  }
}

/** Every paper, filtered; by default my unfiled ones (spec: R11). */
export function PapersPage({ ws, onNew, onNewPrivate, layoutTop }: { ws: string; onNew: () => void; onNewPrivate: () => void; layoutTop: React.ReactNode }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [f, setF] = useState<Filters>(stored)
  const [rows, setRows] = useState<PaperListItem[]>()
  const [next, setNext] = useState('')

  // Answers for filters already left (or a "more" asked before they changed) are dropped.
  const seq = useRef(0)
  useEffect(() => { seq.current += 1 }, [ws, f])
  const load = useCallback(async (cursor = '') => {
    const mine = seq.current
    const r = await api.listPapers(ws, { ...f, cursor: cursor || undefined, limit: 50 })
    if (mine !== seq.current) return
    setRows((prev) => (cursor ? [...(prev ?? []), ...r.papers] : r.papers))
    setNext(r.next_cursor)
  }, [ws, f])
  useEffect(() => {
    setRows(undefined)
    void load().catch(() => setRows([]))
  }, [load])
  const set = (patch: Partial<Filters>) => {
    const n = { ...f, ...patch }
    setF(n)
    writePref('papersFilters', JSON.stringify(n))
  }
  const seg = <K extends keyof Filters>(label: string, key: K, opts: [Filters[K], string][]) => (
    <div className="fg">
      <span>{label}</span>
      <div className="seg" role="group" aria-label={label}>
        {opts.map(([v, l]) => <button key={String(v)} type="button" className={f[key] === v ? 'on' : ''} aria-pressed={f[key] === v} onClick={() => set({ [key]: v } as Partial<Filters>)}>{l}</button>)}
      </div>
    </div>
  )
  const where = (p: PaperListItem) => (p.visibility === 'private' ? `🔒 ${t('private')}` : p.parent ? p.parent.title || t('untitled') : t('unfiled'))
  return (
    <section className="main">
      {layoutTop}
      <div className="scroll"><div className="col wide">
        <div className="all-h">
          <h1 className="page-h" style={{ margin: 0 }}>{t('papers')}</h1>
          <div style={{ display: 'flex', gap: 'var(--sp-4)', flexWrap: 'wrap' }}>
            <button type="button" className="btn" onClick={onNewPrivate}>🔒 {t('newPrivate')}</button>
            <button type="button" className="btn primary" onClick={onNew}>＋ {t('newPaper')}</button>
          </div>
        </div>
        <div className="filters">
          {seg(t('place'), 'place', [['unfiled', t('unfiled')], ['filed', t('filed')], ['all', t('all')]])}
          {seg(t('author'), 'creator', [['me', t('me')], ['all', t('everyone')]])}
          {seg(t('visibilityLabel'), 'visibility', [['all', t('all')], ['public', t('publicLabel')], ['private', t('private')]])}
          <label className="fg"><input type="checkbox" checked={f.archived} onChange={(e) => set({ archived: e.target.checked })} />{t('includeArchived')}</label>
          <span style={{ flex: 1 }} />
          <select className="inp" style={{ height: 'var(--control-h)', width: 'auto', padding: '0 var(--sp-4)' }} aria-label={t('sort')} value={f.sort} onChange={(e) => set({ sort: e.target.value as Filters['sort'] })}>
            <option value="updated">{t('sortUpdated')}</option>
            <option value="created">{t('sortCreated')}</option>
          </select>
        </div>
        {rows && <div className="muted" style={{ fontSize: 'var(--text-sm)', margin: '0 0 var(--sp-3)' }}>{t('nItems', { n: `${rows.length}${next ? '+' : ''}` })}</div>}
        {rows && rows.length === 0 && <div className="list"><div className="empty">{t('noneMatch')}</div></div>}
        {rows && rows.length > 0 && (
          <div className="dbw">
            <table className="dbt">
              <thead><tr><th>{t('colTitle')}</th><th>{t('author')}</th><th>{t('place')}</th><th>{f.sort === 'created' ? t('sortCreated') : t('colEdited')}</th></tr></thead>
              <tbody>
                {rows.map((p) => {
                  const unfiled = p.visibility === 'public' && !p.parent
                  return (
                    <tr key={p.id} tabIndex={0} onClick={(e) => { if (!(e.target as HTMLElement).closest('a,button')) navigate(`/ws/${ws}/p/${p.id}`) }}
                      onKeyDown={(e) => { if (isEnter(e)) navigate(`/ws/${ws}/p/${p.id}`) }}>
                      <td className="tt"><Link to={`/ws/${ws}/p/${p.id}`} style={{ textDecoration: 'none' }}>{p.visibility === 'private' ? '🔒 ' : ''}{p.title || t('untitled')}</Link>{p.status === 'archived' && <span className="muted" style={{ fontWeight: 400, fontSize: 12 }}> · {t('archived')}</span>}</td>
                      <td className="nw">{actorName(p.created_by)}</td>
                      <td className={`loc${unfiled ? ' is-loose' : ''}`}>{where(p)}</td>
                      <td className="nw">{ago(f.sort === 'created' ? p.created_at : p.updated_at)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        {next && <div style={{ marginTop: 'var(--sp-6)' }}><button type="button" className="btn" onClick={() => void load(next)}>{t('loadMore')}</button></div>}
      </div></div>
    </section>
  )
}
