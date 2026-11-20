import { useCallback, useEffect, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useMatch } from 'react-router'
import { ApiError, api } from './api/client'
import type { Me, Paper, Workspace } from './api/types'
import { Sidebar } from './components/Sidebar'
import { useTranslation } from './i18n'
import { LoginPage } from './pages/LoginPage'
import { PaperPage } from './pages/PaperPage'
import { TokensPage } from './pages/TokensPage'
import { WorkspaceHome } from './pages/WorkspaceHome'

function Shell({ me }: { me: Me }) {
  const { t } = useTranslation()
  const match = useMatch('/w/:ws/*')
  const current = match?.params.ws
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [papers, setPapers] = useState<Paper[]>([])
  const [loaded, setLoaded] = useState(false)

  const loadWorkspaces = useCallback(() => {
    void api.listWorkspaces().then((ws) => {
      setWorkspaces(ws)
      setLoaded(true)
    })
  }, [])
  const loadPapers = useCallback(() => {
    if (current) void api.listPapers(current).then(setPapers).catch(() => setPapers([]))
    else setPapers([])
  }, [current])

  useEffect(loadWorkspaces, [loadWorkspaces])
  useEffect(loadPapers, [loadPapers])

  const workspace = workspaces.find((w) => w.id === current)
  const onChanged = () => {
    loadPapers()
    loadWorkspaces()
  }

  return (
    <div className="shell">
      <Sidebar me={me} workspaces={workspaces} papers={papers} current={current} onWorkspacesChanged={loadWorkspaces} />
      <main className="desk">
        <Routes>
          <Route path="/" element={loaded && workspaces[0] ? <Navigate to={`/w/${workspaces[0].id}`} replace /> : <p className="desk-empty">{loaded ? t('noWorkspaces') : ''}</p>} />
          <Route path="/w/:ws" element={workspace ? <WorkspaceHome workspace={workspace} papers={papers} onChanged={onChanged} /> : <p className="desk-empty">{loaded ? t('pickWorkspace') : ''}</p>} />
          <Route path="/w/:ws/p/:paper" element={<PaperPage onChanged={loadPapers} />} />
          <Route path="/settings/tokens" element={<TokensPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
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
