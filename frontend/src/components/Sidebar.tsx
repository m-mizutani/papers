import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { api } from '../api/client'
import { rememberKind } from '../api/kinds'
import type { Me, PaperRef, Sidebar as SidebarData, Workspace, WorkspaceGroup } from '../api/types'
import { accountNote, canSignOut } from './account'
import { useTranslation } from '../i18n'
import { Menu, type MenuItem } from '../ui/Menu'
import { actorColor, initials } from '../utils/actors'
import { MOD_LABEL } from '../utils/keyboard'

/** A tree entry whose children load when it is opened. */
function TreeItem({ ws, paper, depth, current, expand, version, label, icon, onUnfavorite, onNewInside }: {
  ws: string
  paper: PaperRef
  depth: number
  current?: string
  /** Papers to open: the ancestors of the open paper. */
  expand: Set<string>
  version: number
  label?: string
  icon?: string
  onUnfavorite?: () => void
  onNewInside: (parent: string) => void
}) {
  const { t } = useTranslation()
  // A database's items do not show in the tree.
  const db = paper.kind === 'database'
  const [open, setOpen] = useState(expand.has(paper.id) && !db)
  const [kids, setKids] = useState<PaperRef[]>()
  useEffect(() => {
    if (expand.has(paper.id) && !db) setOpen(true)
  }, [expand, paper.id, db])
  useEffect(() => {
    if (!open || db) return
    let live = true
    void api.children(ws, paper.id).then((k) => { if (live) setKids(k.filter((x) => x.status !== 'archived')) }).catch(() => { if (live) setKids([]) })
    return () => { live = false }
  }, [open, ws, paper.id, version, db])
  const canHaveChildren = !db && paper.visibility === 'public' && (!!paper.parent_id || label !== undefined)
  const leaf = kids !== undefined && kids.length === 0
  const to = label !== undefined ? `/ws/${ws}` : `/ws/${ws}/p/${paper.id}`
  return (
    <div>
      <div className={`tree-item${current === paper.id ? ' active' : ''}`} style={{ paddingLeft: `calc(var(--tree-indent) * ${depth})` }}>
        <button type="button" className={`tw${leaf || !canHaveChildren ? ' leaf' : ''}`} aria-label={open ? t('collapse') : t('expand')} aria-expanded={open} onClick={() => setOpen(!open)}>{open ? '▾' : '▸'}</button>
        {(icon || db) && <span className={`ticon${db ? ' db' : ''}`} aria-hidden>{db ? '⊞' : icon}</span>}
        <Link className="tl" to={to} onClick={() => rememberKind(paper.id, paper.kind)}>{label ?? (paper.title || t('untitled'))}</Link>
        <span className="ta">
          {canHaveChildren && <button type="button" title={t('newInside', { title: label ?? (paper.title || t('untitled')) })} aria-label={t('newInside', { title: label ?? (paper.title || t('untitled')) })} onClick={() => onNewInside(paper.id)}>＋</button>}
          {onUnfavorite && <button type="button" title={t('removeFavorite')} aria-label={t('removeFavorite')} onClick={onUnfavorite}>✕</button>}
        </span>
      </div>
      {open && kids?.map((k) => <TreeItem key={k.id} ws={ws} paper={k} depth={depth + 1} current={current} expand={expand} version={version} onNewInside={onNewInside} />)}
    </div>
  )
}

export function groupWorkspaces(workspaces: Workspace[], groups: WorkspaceGroup[], other: string): { name: string; items: Workspace[] }[] {
  const byId = new Map(workspaces.map((w) => [w.id, w]))
  const grouped = new Set(groups.flatMap((g) => g.workspaces))
  const out = groups.map((g) => ({ name: g.name, items: g.workspaces.flatMap((id) => byId.get(id) ?? []) })).filter((g) => g.items.length)
  const rest = workspaces.filter((w) => !grouped.has(w.id))
  if (rest.length) out.push({ name: out.length ? other : '', items: rest })
  return out
}

export function Sidebar({ me, workspaces, ws, data, current, expand, version, active, onSearch, onNew, onNewInside, onSignOut, onChanged }: {
  me: Me
  workspaces: Workspace[]
  ws: string
  data?: SidebarData
  current?: string
  expand: Set<string>
  version: number
  active?: 'papers' | 'trash' | 'settings'
  onSearch: () => void
  onNew: () => void
  onNewInside: (parent: string) => void
  onSignOut: () => void
  onChanged: () => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [menu, setMenu] = useState<{ anchor: HTMLElement; items: MenuItem[] } | null>(null)
  const w = workspaces.find((x) => x.id === ws)

  const wsMenu = (anchor: HTMLElement) => setMenu({
    anchor,
    items: [
      { head: t('workspaces') },
      ...workspaces.map((x) => ({ label: x.name, icon: x.emoji ?? '▦', desc: x.id === ws ? '✓' : undefined, onSelect: () => navigate(`/ws/${x.id}`) })),
      '-',
      { label: t('allWorkspaces'), icon: '▦', onSelect: () => navigate('/workspaces') },
    ],
  })
  const meMenu = (anchor: HTMLElement) => setMenu({
    anchor,
    items: [
      { note: accountNote(me) },
      { label: t('allWorkspaces'), icon: '▦', onSelect: () => navigate('/workspaces') },
      ...(canSignOut(me) ? [{ label: t('signOut'), icon: '⎋', onSelect: onSignOut }] : []),
    ],
  })
  const unfavorite = (id: string) => void api.unfavorite(ws, id).then(onChanged).catch(() => undefined)
  const count = data?.unfiled_count ?? 0

  return (
    <aside className="sb" aria-label={t('workspace')}>
      <div className="sb-top">
        <button type="button" className="ws-btn" aria-haspopup="menu" onClick={(e) => wsMenu(e.currentTarget)}>
          <span className="ws-emoji">{w?.emoji ?? '▦'}</span>
          <span className="ws-name">{w?.name ?? ws}</span>
          <span className="muted" aria-hidden>⌄</span>
        </button>
        <Link className={`sb-row${active === 'papers' ? ' active' : ''}`} to={`/ws/${ws}/papers`}>
          <span className="ic" aria-hidden>☰</span><span className="lbl">{t('papers')}</span>
          {count > 0 && <span className="badge" title={t('unfiledCount', { n: count >= 100 ? '99+' : count })}>{count >= 100 ? '99+' : count}</span>}
        </Link>
        <button type="button" className="sb-row" onClick={onSearch}>
          <span className="ic" aria-hidden>⌕</span><span className="lbl">{t('search')}</span><span className="kbd">{MOD_LABEL}K</span>
        </button>
        <button type="button" className="sb-row" onClick={onNew}>
          <span className="ic" aria-hidden>＋</span><span className="lbl">{t('newPaper')}</span>
        </button>
      </div>
      <div className="sb-scroll">
        {data?.home && (
          <div className="sb-sec first">
            <TreeItem ws={ws} paper={data.home} depth={0} current={current} expand={expand} version={version} label={t('home')} icon="⌂" onNewInside={onNewInside} />
          </div>
        )}
        {!!data?.favorites.length && (
          <div className="sb-sec">
            <div className="sb-h">{t('favorites')}</div>
            {data.favorites.map((f) => <TreeItem key={f.id} ws={ws} paper={f} depth={0} current={current} expand={expand} version={version} onUnfavorite={() => unfavorite(f.id)} onNewInside={onNewInside} />)}
          </div>
        )}
        {!!data?.recent.length && (
          <div className="sb-sec">
            <div className="sb-h">{t('recent')}</div>
            {data.recent.slice(0, 5).map((r) => (
              <div key={r.id} className={`tree-item${current === r.id ? ' active' : ''}`}>
                <span className="tw leaf" />
                <Link className="tl" to={`/ws/${ws}/p/${r.id}`}>{r.visibility === 'private' ? '🔒 ' : ''}{r.title || t('untitled')}</Link>
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="sb-foot">
        <Link className={`sb-row${active === 'trash' ? ' active' : ''}`} to={`/ws/${ws}/trash`}><span className="ic" aria-hidden>🗑</span><span className="lbl">{t('trash')}</span></Link>
        <Link className={`sb-row${active === 'settings' ? ' active' : ''}`} to="/settings"><span className="ic" aria-hidden>⚙</span><span className="lbl">{t('settings')}</span></Link>
        <button type="button" className="sb-row" onClick={(e) => meMenu(e.currentTarget)}>
          <span className="face solo" style={{ background: actorColor({ kind: 'human', user_id: me.user.id, name: me.user.name }) }}>{initials(me.user.name)}</span>
          <span className="lbl">{me.user.name}</span>
        </button>
      </div>
      {menu && <Menu anchor={menu.anchor} items={menu.items} onClose={() => setMenu(null)} />}
    </aside>
  )
}
