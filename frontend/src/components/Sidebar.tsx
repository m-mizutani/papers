import { useState } from 'react'
import { NavLink, useNavigate } from 'react-router'
import { api } from '../api/client'
import type { Me, Paper, Workspace } from '../api/types'
import { useTranslation } from '../i18n'

export function Sidebar({ me, workspaces, papers, current, onWorkspacesChanged }: {
  me: Me
  workspaces: Workspace[]
  papers: Paper[]
  current?: string
  onWorkspacesChanged: () => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')

  const create = async () => {
    if (!name.trim()) return
    const ws = await api.createWorkspace(name.trim(), '')
    setName('')
    setCreating(false)
    onWorkspacesChanged()
    navigate(`/w/${ws.id}`)
  }

  const signOut = async () => {
    await api.logout()
    window.location.href = '/login'
  }

  return (
    <nav className="sidebar" aria-label={t('workspaces')}>
      <div className="brand"><span className="brand-mark" aria-hidden />{t('appName')}</div>

      <section>
        <h2>{t('workspaces')}</h2>
        <ul className="nav-list">
          {workspaces.map((w) => (
            <li key={w.id}>
              <NavLink className="nav-item" to={`/w/${w.id}`} end aria-current={w.id === current ? 'page' : undefined}>
                <span aria-hidden>{w.emoji || '▤'}</span>
                {w.name}
              </NavLink>
            </li>
          ))}
        </ul>
        {creating ? (
          <form className="inline-form" onSubmit={(e) => { e.preventDefault(); void create() }}>
            <input className="field" autoFocus value={name} placeholder={t('workspaceName')} aria-label={t('workspaceName')} onChange={(e) => setName(e.target.value)} />
            <button type="submit" className="btn">{t('create')}</button>
          </form>
        ) : (
          <button type="button" className="nav-item" onClick={() => setCreating(true)}>+ {t('newWorkspace')}</button>
        )}
      </section>

      {current && (
        <section>
          <h2>{t('papers')}</h2>
          <ul className="nav-list">
            {papers.slice(0, 50).map((p) => (
              <li key={p.id}>
                <NavLink className="nav-item" to={`/w/${current}/p/${p.id}`}>
                  {p.title || t('untitled')}
                </NavLink>
              </li>
            ))}
          </ul>
        </section>
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
