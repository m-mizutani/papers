import { useState } from 'react'
import { Link } from 'react-router'
import type { Me, Workspace, WorkspaceGroup } from '../api/types'
import { groupWorkspaces } from '../components/Sidebar'
import { ago, useTranslation } from '../i18n'
import { Menu } from '../ui/Menu'
import { actorColor, initials } from '../utils/actors'

/** Workspaces as cards by display group, each with where the user left off (spec: R21). */
export function WorkspacesPage({ me, workspaces, groups, onSignOut }: { me: Me; workspaces: Workspace[]; groups: WorkspaceGroup[]; onSignOut: () => void }) {
  const { t } = useTranslation()
  const [menu, setMenu] = useState<HTMLElement | null>(null)
  const sections = groupWorkspaces(workspaces, groups, t('otherWorkspaces'))
  return (
    <main className="gate">
      <header className="gate-top">
        <div className="wordmark sm"><span className="mark" aria-hidden />papers</div>
        <span style={{ flex: 1 }} />
        <button type="button" className="face solo" style={{ background: actorColor({ kind: 'human', user_id: me.user.id, name: me.user.name }), width: 'var(--control-h)', height: 'var(--control-h)' }}
          aria-label={me.user.name} onClick={(e) => setMenu(e.currentTarget)}>{initials(me.user.name)}</button>
      </header>
      <div className="gate-body">
        <h1 className="page-h">{t('workspaces')}</h1>
        {workspaces.length === 0 && (
          <div className="list"><div className="empty">
            <p style={{ margin: '0 0 var(--sp-3)', color: 'var(--fg)', fontWeight: 600 }}>{t('noWorkspacesTitle')}</p>
            <p style={{ margin: '0 0 var(--sp-6)' }}>{t('noWorkspacesBody')}</p>
            <button type="button" className="btn" onClick={onSignOut}>{t('signOut')}</button>
          </div></div>
        )}
        {sections.map((g) => (
          <section key={g.name || '-'} className="wgroup">
            {g.name && <h2 className="wgh">{g.name}</h2>}
            <div className="wgrid">
              {g.items.map((w) => (
                <div key={w.id} className="wcard">
                  <Link className="wmain" to={`/ws/${w.id}`}>
                    <span className="wemoji" aria-hidden>{w.emoji ?? '▦'}</span>
                    <span className="wtext"><span className="wname">{w.name}</span>{w.description && <span className="wdesc">{w.description.split('\n')[0]}</span>}</span>
                  </Link>
                  {w.last_opened
                    ? (
                      <Link className="wlast" to={`/ws/${w.id}/p/${w.last_opened.paper_id}`}>
                        <span className="muted">{t('continue')}</span><span className="wlt">{w.last_opened.title || t('untitled')}</span><span className="muted">{ago(w.last_opened.opened_at)}</span>
                      </Link>
                    )
                    : <span className="wlast none">{t('notOpened')}</span>}
                </div>
              ))}
            </div>
          </section>
        ))}
      </div>
      {menu && <Menu anchor={menu} onClose={() => setMenu(null)} items={[{ note: me.user.email || me.user.name }, { label: t('signOut'), icon: '⎋', onSelect: onSignOut }]} />}
    </main>
  )
}
