import { useCallback, useEffect, useState } from 'react'
import { Link, Navigate, Route, Routes, useLocation, useMatch, useNavigate, useParams } from 'react-router'
import { ApiError, api } from './api/client'
import type { Me, Workspace, WorkspaceGroup } from './api/types'
import { Sidebar } from './components/Sidebar'
import { useTranslation } from './i18n'
import { LoginPage } from './pages/LoginPage'
import { PaperPage } from './pages/PaperPage'
import { SearchPage } from './pages/SearchPage'
import { TokensPage } from './pages/TokensPage'
import { TrashPage } from './pages/TrashPage'

const LAST_WS = 'papers.lastWorkspace'

function remember(ws: string) {
  try {
    window.localStorage.setItem(LAST_WS, ws)
  } catch {
    // storage may be unavailable; the list is the fallback
  }
}

function lastWorkspace(): string | null {
  try {
    return window.localStorage.getItem(LAST_WS)
  } catch {
    return null
  }
}

/** /: the home of the workspace opened last, or the list the first time. */
function Root({ workspaces, loaded }: { workspaces: Workspace[]; loaded: boolean }) {
  const { t } = useTranslation()
  if (!loaded) return null
  const last = lastWorkspace()
  const target = workspaces.find((w) => w.id === last) ?? (workspaces.length === 1 ? workspaces[0] : undefined)
  if (target) return <Navigate to={`/ws/${target.id}`} replace />
  if (workspaces.length === 0) return <p className="desk-empty">{t('noWorkspaces')}</p>
  return (
    <div className="desk-empty">
      <p>{t('pickWorkspace')}</p>
      <ul className="hits">
        {workspaces.map((w) => (
          <li key={w.id}><Link to={`/ws/${w.id}`}>{w.emoji ? `${w.emoji} ` : ''}{w.name}</Link>{w.description && <p className="hit-text">{w.description}</p>}</li>
        ))}
      </ul>
    </div>
  )
}

function PaperRoute({ workspaces, me, mode, onChanged }: { workspaces: Workspace[]; me: Me; mode: 'current' | 'latest' | 'version' | 'home'; onChanged: () => void }) {
  const { t } = useTranslation()
  const { ws = '', paper, n } = useParams()
  const workspace = workspaces.find((w) => w.id === ws)
  useEffect(() => {
    if (workspace) remember(workspace.id)
  }, [workspace])
  if (!workspace) return <p className="desk-empty">{t('workspaceNotFound')}</p>
  const id = mode === 'home' ? workspace.home_paper_id : paper ?? ''
  const version = mode === 'latest' ? 'latest' : mode === 'version' ? Number(n) : undefined
  return <PaperPage key={`${id}:${version ?? ''}`} ws={ws} paperId={id} version={version} me={me} onChanged={onChanged} />
}

function Shell({ me }: { me: Me }) {
  const navigate = useNavigate()
  const match = useMatch('/ws/:ws/*')
  const current = match?.params.ws
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [groups, setGroups] = useState<WorkspaceGroup[]>([])
  const [loaded, setLoaded] = useState(false)
  const [version, setVersion] = useState(0)

  useEffect(() => {
    void api.listWorkspaces().then((r) => {
      setWorkspaces(r.workspaces)
      setGroups(r.groups)
      setLoaded(true)
    })
  }, [])
  const onChanged = useCallback(() => setVersion((v) => v + 1), [])

  return (
    <div className="shell">
      <Sidebar me={me} workspaces={workspaces} groups={groups} current={current} version={version}
        onSearch={(q) => current && navigate(`/ws/${current}/search?q=${encodeURIComponent(q)}`)} />
      <main className="desk">
        <Routes>
          <Route path="/" element={<Root workspaces={workspaces} loaded={loaded} />} />
          <Route path="/ws/:ws" element={loaded ? <PaperRoute workspaces={workspaces} me={me} mode="home" onChanged={onChanged} /> : null} />
          <Route path="/ws/:ws/p/:paper" element={loaded ? <PaperRoute workspaces={workspaces} me={me} mode="current" onChanged={onChanged} /> : null} />
          <Route path="/ws/:ws/p/:paper/latest" element={loaded ? <PaperRoute workspaces={workspaces} me={me} mode="latest" onChanged={onChanged} /> : null} />
          <Route path="/ws/:ws/p/:paper/v/:n" element={loaded ? <PaperRoute workspaces={workspaces} me={me} mode="version" onChanged={onChanged} /> : null} />
          <Route path="/ws/:ws/search" element={<SearchRoute />} />
          <Route path="/ws/:ws/trash" element={<TrashRoute onChanged={onChanged} />} />
          <Route path="/settings/tokens" element={<TokensPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}

function SearchRoute() {
  const { ws = '' } = useParams()
  const q = new URLSearchParams(useLocation().search).get('q') ?? ''
  return <SearchPage ws={ws} query={q} />
}

function TrashRoute({ onChanged }: { onChanged: () => void }) {
  const { ws = '' } = useParams()
  return <TrashPage ws={ws} onChanged={onChanged} />
}

export function App() {
  const location = useLocation()
  const [me, setMe] = useState<Me | null | undefined>(undefined)

  useEffect(() => {
    api.me().then(setMe).catch((e: unknown) => {
      if (e instanceof ApiError && e.status === 401) setMe(null)
      else setMe(null)
    })
  }, [])

  if (location.pathname === '/login') return <LoginPage />
  if (me === undefined) return null
  if (me === null) return <Navigate to={`/login?return_to=${encodeURIComponent(location.pathname)}`} replace />
  return <Shell me={me} />
}
