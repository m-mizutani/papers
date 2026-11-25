import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { api } from '../api/client'
import type { Me, PaperListItem } from '../api/types'
import { PersonPicker, type Person } from '../components/PersonPicker'
import { ActorMention, useUserNames } from '../components/UserMention'
import { ago, useTranslation } from '../i18n'
import { Menu, type MenuItem } from '../ui/Menu'
import { paperIcon } from '../utils/icon'
import { isEnter } from '../utils/keyboard'
import { readPref, writePref } from '../utils/prefs'
import { isSet, readFilters, toQuery, type Filters } from './papersFilters'

type Open = { kind: 'creator' | 'visibility' | 'sort'; anchor: HTMLElement }

/** Every paper, filtered; by default my unfiled ones (spec: R11). The filters live in the URL. */
export function PapersPage({ ws, me, homeId, onNew, onNewPrivate, layoutTop }: { ws: string; me: Me; homeId?: string; onNew: () => void; onNewPrivate: () => void; layoutTop: React.ReactNode }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const myId = me.user.id
  const f = useMemo(() => readFilters(params, readPref('papersFilters'), myId), [params, myId])
  const [rows, setRows] = useState<PaperListItem[]>()
  const [next, setNext] = useState('')
  const [failed, setFailed] = useState<'first' | 'more'>()
  const [open, setOpen] = useState<Open>()
  // Names of people picked or seen, for the author button.
  const [names, setNames] = useState<Record<string, string>>({})

  // Answers for filters already left (or a "more" asked before they changed) are dropped.
  const seq = useRef(0)
  useEffect(() => { seq.current += 1 }, [ws, f])
  const load = useCallback(async (cursor = '') => {
    const mine = seq.current
    setFailed(undefined)
    try {
      const r = await api.listPapers(ws, { ...f, cursor: cursor || undefined, limit: 50 })
      if (mine !== seq.current) return
      setRows((prev) => (cursor ? [...(prev ?? []), ...r.papers] : r.papers))
      setNext(r.next_cursor)
    } catch {
      if (mine === seq.current) setFailed(cursor ? 'more' : 'first')
    }
  }, [ws, f])
  useEffect(() => {
    setRows(undefined)
    void load()
  }, [load])
  useEffect(() => {
    if (!rows) return
    const seen: Record<string, string> = {}
    for (const p of rows) if (p.created_by.kind !== 'system' && p.created_by.user_id) seen[p.created_by.user_id] = p.created_by.name
    setNames((prev) => ({ ...seen, ...prev }))
  }, [rows])

  const set = (patch: Partial<Filters>) => {
    const n = { ...f, ...patch }
    setParams(toQuery(n), { replace: true })
    writePref('papersFilters', JSON.stringify(n))
  }
  // The people who can use the workspace, found by a part of the name or email.
  const search = useCallback(async (q: string): Promise<Person[]> => (await api.users(ws, { q })).map((u) => ({ id: u.id, name: u.name })), [ws])
  // A person named only by the URL (a person's card links here) is looked up once.
  const other = f.creator !== 'me' && f.creator !== 'all' ? f.creator : ''
  const looked = useUserNames(ws, other && !names[other] ? [other] : []).names
  const creatorName = names[f.creator] ?? looked.get(f.creator)

  const creatorLabel = f.creator === 'me' ? t('meMention') : f.creator === 'all' ? t('everyone') : creatorName ? `@${creatorName}` : t('unknownUser')
  const visLabel = { all: t('all'), public: t('publicLabel'), private: t('private') }[f.visibility]
  const menuItems = (kind: 'visibility' | 'sort'): MenuItem[] => kind === 'visibility'
    ? (['all', 'public', 'private'] as const).map((v) => ({ label: { all: t('all'), public: t('publicLabel'), private: t('private') }[v], icon: f.visibility === v ? '✓' : '', onSelect: () => set({ visibility: v }) }))
    : (['updated', 'created'] as const).map((v) => ({ label: v === 'updated' ? t('sortUpdatedNewest') : t('sortCreatedNewest'), icon: f.sort === v ? '✓' : '', onSelect: () => set({ sort: v }) }))
  const toggle = (kind: Open['kind'], el: HTMLElement) => setOpen((o) => (o?.kind === kind ? undefined : { kind, anchor: el }))

  const place = (p: PaperListItem) => (p.visibility === 'private'
    ? <span className="tag">🔒 {t('private')}</span>
    : p.parent ? <span className="ploc"><span className="pic" aria-hidden>{paperIcon(p.parent, homeId)}</span>{p.parent.title || t('untitled')}</span> : <span className="tag">{t('unfiled')}</span>)
  const who = (p: PaperListItem) => (
    <span className="pwho"><ActorMention actor={p.created_by} /></span>
  )
  const go = (p: PaperListItem) => navigate(`/ws/${ws}/p/${p.id}`)
  const tabs: [Filters['place'], string][] = [['unfiled', t('unfiled')], ['filed', t('filed')], ['all', t('all')]]

  return (
    <section className="main">
      {layoutTop}
      <div className="scroll"><div className="col wide">
        <div className="all-h">
          <div className="plh">
            <h1 className="page-h" style={{ margin: 0 }}>{t('papers')}</h1>
            {rows && <span className="muted">{t('nItems', { n: `${rows.length}${next ? '+' : ''}` })}</span>}
          </div>
          <div className="plh-acts">
            <button type="button" className="btn pl-private" onClick={onNewPrivate} aria-label={t('newPrivate')} title={t('newPrivate')}>🔒<span className="pl-lbl"> {t('newPrivate')}</span></button>
            <button type="button" className="btn primary" onClick={onNew}>＋ {t('newPaper')}</button>
          </div>
        </div>
        <div className="ptabs" role="tablist" aria-label={t('place')}>
          {tabs.map(([v, l]) => <button key={v} type="button" role="tab" aria-selected={f.place === v} className={f.place === v ? 'on' : ''} onClick={() => set({ place: v })}>{l}</button>)}
        </div>
        <div className="pfilters">
          <button type="button" className={`pill${isSet(f, 'creator') ? ' set' : ''}`} aria-haspopup="dialog" aria-expanded={open?.kind === 'creator'} onClick={(e) => toggle('creator', e.currentTarget)}>
            {t('authorIs', { name: creatorLabel })}<span className="caret" aria-hidden>▾</span>
          </button>
          <button type="button" className={`pill${isSet(f, 'visibility') ? ' set' : ''}`} aria-haspopup="menu" aria-expanded={open?.kind === 'visibility'} onClick={(e) => toggle('visibility', e.currentTarget)}>
            {t('visibilityIs', { value: visLabel })}<span className="caret" aria-hidden>▾</span>
          </button>
          <button type="button" className={`pill${f.archived ? ' set' : ''}`} aria-pressed={f.archived} onClick={() => set({ archived: !f.archived })}>
            {f.archived ? '✓ ' : ''}{t('includeArchived')}
          </button>
          <span className="sp" />
          <button type="button" className="pill plain" aria-label={t('sort')} aria-haspopup="menu" aria-expanded={open?.kind === 'sort'} onClick={(e) => toggle('sort', e.currentTarget)}>
            ↓ {f.sort === 'created' ? t('sortCreated') : t('sortUpdated')}<span className="caret" aria-hidden>▾</span>
          </button>
        </div>
        {open?.kind === 'creator' && (
          <PersonPicker anchor={open.anchor} value={f.creator} me={{ id: myId, name: me.user.name }} search={search} onClose={() => setOpen(undefined)}
            onPick={(v, p) => {
              if (p && v !== 'me') setNames((prev) => ({ ...prev, [p.id]: p.name }))
              set({ creator: v })
            }} />
        )}
        {(open?.kind === 'visibility' || open?.kind === 'sort') && <Menu anchor={open.anchor} items={menuItems(open.kind)} onClose={() => setOpen(undefined)} label={open.kind === 'sort' ? t('sort') : t('visibilityLabel')} />}

        {failed === 'first' && (
          <div className="list"><div className="empty">{t('papersLoadFailed')}<div><button type="button" className="btn" style={{ marginTop: 'var(--sp-4)' }} onClick={() => void load()}>{t('retry')}</button></div></div></div>
        )}
        {!rows && !failed && (
          <table className="plist" aria-busy="true"><tbody>
            {[60, 45, 70].map((w) => <tr key={w}><td><span className="skel" style={{ width: `${w}%`, margin: 0 }} /></td><td className="pwhen" /></tr>)}
          </tbody></table>
        )}
        {rows && rows.length === 0 && <div className="list"><div className="empty">{t('noneMatch')}</div></div>}
        {rows && rows.length > 0 && (
          <div className="plistw">
            <table className="plist">
              <thead><tr><th>{t('colTitle')}</th><th className="pwide">{t('place')}</th><th className="pwide">{t('author')}</th><th className="pwhen">{f.sort === 'created' ? t('colCreated') : t('colEdited')}</th></tr></thead>
              <tbody>
                {rows.map((p) => (
                  <tr key={p.id} tabIndex={0} onClick={(e) => { if (!(e.target as HTMLElement).closest('a,button')) go(p) }}
                    onKeyDown={(e) => { if (isEnter(e)) go(p) }}>
                    <td>
                      <div className="ptt">
                        <span className="pic" aria-hidden>{paperIcon(p)}</span>
                        <Link to={`/ws/${ws}/p/${p.id}`}>{p.title || t('untitled')}</Link>
                        {p.status === 'archived' && <span className="tag">{t('archived')}</span>}
                      </div>
                      <div className="psub">{place(p)}{who(p)}</div>
                    </td>
                    <td className="pwide">{place(p)}</td>
                    <td className="pwide">{who(p)}</td>
                    <td className="pwhen">{ago(f.sort === 'created' ? p.created_at : p.updated_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {failed === 'more' && <div className="pmore muted">{t('papersLoadFailed')} <button type="button" className="btn" onClick={() => void load(next)}>{t('retry')}</button></div>}
        {next && !failed && <div className="pmore"><button type="button" className="btn" onClick={() => void load(next)}>{t('loadMore')}</button></div>}
      </div></div>
    </section>
  )
}
