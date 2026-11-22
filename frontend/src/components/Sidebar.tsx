import { useEffect, useState, type ReactNode } from 'react'
import { NavLink, useNavigate } from 'react-router'
import { api } from '../api/client'
import type { Me, PaperRef, Sidebar as SidebarData, Workspace, WorkspaceGroup } from '../api/types'
import { useTranslation } from '../i18n'

/** A sidebar entry whose children load when it is opened. */
function TreeItem({ ws, paper, depth = 0, extra }: { ws: string; paper: PaperRef; depth?: number; extra?: ReactNode }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [kids, setKids] = useState<PaperRef[]>()
  const toggle = () => {
    if (!open && !kids) void api.children(ws, paper.id).then(setKids).catch(() => setKids([]))
    setOpen(!open)
  }
  return (
    <li>
      <div className="tree-row" style={{ paddingLeft: `${depth * 0.75}rem` }}>
        <button type="button" className="twisty" aria-label={open ? t('collapse') : t('expand')} aria-expanded={open} onClick={toggle}>{open ? '▾' : '▸'}</button>
        <NavLink className="nav-item" to={`/ws/${ws}/p/${paper.id}`} end>{paper.title || t('untitled')}</NavLink>
        {extra}
      </div>
      {open && kids && (
        <ul className="nav-list">
          {kids.length === 0 && <li className="tree-empty" style={{ paddingLeft: `${(depth + 2) * 0.75}rem` }}>{t('noChildren')}</li>}
          {kids.map((k) => <TreeItem key={k.id} ws={ws} paper={k} depth={depth + 1} />)}
        </ul>
      )}
    </li>
  )
}

function groupWorkspaces(workspaces: Workspace[], groups: WorkspaceGroup[]): { name: string; items: Workspace[] }[] {
  const byId = new Map(workspaces.map((w) => [w.id, w]))
  const grouped = new Set(groups.flatMap((g) => g.workspaces))
  const out = groups.map((g) => ({ name: g.name, items: g.workspaces.flatMap((id) => byId.get(id) ?? []) }))
  const rest = workspaces.filter((w) => !grouped.has(w.id))
  if (rest.length) out.push({ name: '', items: rest })
  return out
}

export function Sidebar({ me, workspaces, groups, current, version, onSearch }: {
  me: Me
  workspaces: Workspace[]
  groups: WorkspaceGroup[]
  current?: string
  /** Bumped by the page when the tree or pins may have changed. */
  version: number
  onSearch: (q: string) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [data, setData] = useState<SidebarData>()
  const [q, setQ] = useState('')

  useEffect(() => {
    if (!current) {
      setData(undefined)
      return
    }
    void api.sidebar(current).then(setData).catch(() => setData(undefined))
  }, [current, version])

  const signOut = async () => {
    await api.logout()
    window.location.href = '/login'
  }

  const newPaper = async (visibility: 'public' | 'private') => {
    if (!current) return
    const title = window.prompt(t('newPaperTitle'))
    if (title === null) return
    const p = await api.createPaper(current, { title, visibility })
    navigate(`/ws/${current}/p/${p.id}`)
  }

  const unpin = async (id: string) => {
    if (!current) return
    await api.unpin(current, id)
    setData(await api.sidebar(current))
  }

  return (
    <nav className="sidebar" aria-label={t('navigation')}>
      <div className="brand"><span className="brand-mark" aria-hidden />{t('appName')}</div>

      <section>
        <h2>{t('workspaces')}</h2>
        {groupWorkspaces(workspaces, groups).map((g) => (
          <div key={g.name || '-'}>
            {g.name && <h3 className="group-name">{g.name}</h3>}
            <ul className="nav-list">
              {g.items.map((w) => (
                <li key={w.id}>
                  <NavLink className="nav-item" to={`/ws/${w.id}`} aria-current={w.id === current ? 'page' : undefined}>
                    <span aria-hidden className="ws-mark" style={w.color ? { background: w.color } : undefined}>{w.emoji || '▤'}</span>
                    {w.name}
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </section>

      {current && data && (
        <>
          <form className="inline-form" role="search" onSubmit={(e) => { e.preventDefault(); if (q.trim()) onSearch(q.trim()) }}>
            <input className="field" type="search" value={q} placeholder={t('searchPlaceholder')} aria-label={t('searchPlaceholder')} onChange={(e) => setQ(e.target.value)} />
          </form>
          <section>
            <ul className="nav-list">
              {data.home && <li><NavLink className="nav-item" to={`/ws/${current}`} end>⌂ {t('home')}</NavLink></li>}
            </ul>
          </section>
          <section>
            <h2>{t('pins')}</h2>
            {data.pins.length === 0 && <p className="nav-empty">{t('noPins')}</p>}
            <ul className="nav-list tree">
              {data.pins.map((p) => (
                <TreeItem key={p.id} ws={current} paper={p} extra={
                  <button type="button" className="tool" title={t('unpin')} aria-label={t('unpin')} onClick={() => void unpin(p.id)}>×</button>
                } />
              ))}
            </ul>
          </section>
          <section>
            <h2>{t('recent')}</h2>
            <ul className="nav-list">
              {data.recent.map((p) => (
                <li key={p.id}><NavLink className="nav-item" to={`/ws/${current}/p/${p.id}`}>{p.title || t('untitled')}</NavLink></li>
              ))}
            </ul>
          </section>
          <section>
            <h2>{t('privatePapers')}</h2>
            <ul className="nav-list">
              {data.private.map((p) => (
                <li key={p.id}><NavLink className="nav-item" to={`/ws/${current}/p/${p.id}`}>🔒 {p.title || t('untitled')}</NavLink></li>
              ))}
            </ul>
            <button type="button" className="nav-item" onClick={() => void newPaper('private')}>+ {t('newPrivatePaper')}</button>
          </section>
          <section>
            <button type="button" className="nav-item" onClick={() => void newPaper('public')}>+ {t('newPaper')}</button>
            <NavLink className="nav-item" to={`/ws/${current}/trash`}>{t('trash')}</NavLink>
          </section>
        </>
      )}

      <div className="sidebar-foot">
        {me.no_authn && <span className="dev-banner">{me.demo ? t('demoMode') : t('devMode')}</span>}
        <NavLink className="nav-item" to="/settings/tokens">{t('tokens')}</NavLink>
        <div className="me">
          {me.user.avatar_url ? <img className="avatar" src={me.user.avatar_url} alt="" referrerPolicy="no-referrer" /> : <span className="avatar" aria-hidden />}
          <span>{me.user.name || me.user.email}</span>
        </div>
        {!me.no_authn && <button type="button" className="nav-item" onClick={() => void signOut()}>{t('signOut')}</button>}
      </div>
    </nav>
  )
}
