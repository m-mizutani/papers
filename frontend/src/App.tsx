import { useCallback, useEffect, useMemo, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useMatch, useNavigate, useParams } from 'react-router'
import { ApiError, UNAUTHORIZED, api } from './api/client'
import type { Me, PaperView, Sidebar as SidebarData, Workspace, WorkspaceGroup } from './api/types'
import { Palette, type Command } from './components/Palette'
import { Sidebar } from './components/Sidebar'
import { useTranslation } from './i18n'
import { LoginPage } from './pages/LoginPage'
import { PaperPage } from './pages/PaperPage'
import { PapersPage } from './pages/PapersPage'
import { SettingsPage, ShortcutList } from './pages/SettingsPage'
import { TrashPage } from './pages/TrashPage'
import { WorkspacesPage } from './pages/WorkspacesPage'
import { Modal } from './ui/Modal'
import { ToastProvider, useToast } from './ui/Toast'
import { MOD_LABEL, modKey } from './utils/keyboard'
import { readPref, setLang, writePref } from './utils/prefs'

/** What a page may change in the shell around it. */
export interface Layout {
  drawer: boolean
  setDrawer: (open: boolean) => void
  toggleSidebar: () => void
}

const lastWorkspace = () => readPref('lastWorkspace')

/**
 * After sign-in, /: the workspace opened last, or the only one, or the
 * chooser (spec: R21).
 */
function Root({ workspaces, loaded }: { workspaces: Workspace[]; loaded: boolean }) {
  if (!loaded) return null
  const last = lastWorkspace()
  const target = workspaces.find((w) => w.id === last) ?? (workspaces.length === 1 ? workspaces[0] : undefined)
  if (target) return <Navigate to={`/ws/${target.id}`} replace />
  return <Navigate to="/workspaces" replace />
}

function Top({ title, layout }: { title: string; layout: Layout }) {
  const { t } = useTranslation()
  return (
    <header className="top">
      <button type="button" className="iconbtn" aria-label={t('toggleSidebar')} title={`${t('toggleSidebar')} (${MOD_LABEL}\\)`} onClick={layout.toggleSidebar}>☰</button>
      <div className="crumbs"><span className="crumb cur">{title}</span></div>
    </header>
  )
}

function Shell({ me, workspaces, loaded, onSignOut }: { me: Me; workspaces: Workspace[]; loaded: boolean; onSignOut: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const toast = useToast()
  const wsMatch = useMatch('/ws/:ws/*')
  const paperMatch = useMatch('/ws/:ws/p/:paper/*')
  const ws = wsMatch?.params.ws ?? lastWorkspace() ?? workspaces[0]?.id ?? ''
  const workspace = workspaces.find((w) => w.id === ws)
  const [sidebar, setSidebar] = useState<SidebarData>()
  const [version, setVersion] = useState(0)
  const [view, setView] = useState<PaperView>()
  const [palette, setPalette] = useState(false)
  const [help, setHelp] = useState(false)
  const [drawer, setDrawer] = useState(() => readPref('drawer') === '1')
  const [sbHidden, setSbHidden] = useState(false)
  const [sbMobile, setSbMobile] = useState(false)
  const location = useLocation()

  useEffect(() => {
    if (wsMatch?.params.ws && workspace) writePref('lastWorkspace', wsMatch.params.ws)
  }, [wsMatch?.params.ws, workspace])
  useEffect(() => {
    if (!ws) return
    let live = true
    void api.sidebar(ws).then((s) => { if (live) setSidebar(s) }).catch(() => undefined)
    return () => { live = false }
  }, [ws, version])
  useEffect(() => setSbMobile(false), [location.pathname])

  const onChanged = useCallback(() => setVersion((v) => v + 1), [])
  const layout: Layout = useMemo(() => ({
    drawer,
    setDrawer: (open: boolean) => { setDrawer(open); writePref('drawer', open ? '1' : '0') },
    toggleSidebar: () => (window.innerWidth <= 860 ? setSbMobile((v) => !v) : setSbHidden((v) => !v)),
  }), [drawer])

  const create = useCallback(async (input: { parent?: string; visibility?: 'private' }) => {
    try {
      const p = await api.createPaper(ws, { title: '', parent: input.parent, visibility: input.visibility })
      onChanged()
      navigate(`/ws/${ws}/p/${p.id}`, { state: { focusTitle: true } })
    } catch (e) {
      toast(t('error', { message: e instanceof Error ? e.message : String(e) }))
    }
  }, [ws, navigate, onChanged, toast, t])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement
      if (modKey(e) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPalette(true)
      } else if (modKey(e) && e.key === '\\') {
        e.preventDefault()
        layout.toggleSidebar()
      } else if (e.key === '?' && !target.closest('input, textarea, [contenteditable="true"], .scrim')) {
        setHelp(true)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [layout])

  const paperId = paperMatch?.params.paper
  const expand = useMemo(() => new Set(view && view.paper.id === paperId ? (view.path ?? []).map((p) => p.id) : []), [view, paperId])
  const under = view && view.paper.id === paperId && view.paper.visibility === 'public' && !view.is_home && view.paper.parent_id ? { id: view.paper.id, title: view.paper.title } : undefined
  const commands: Command[] = [
    { label: t('newPaper'), icon: '＋', run: () => void create({}) },
    { label: t('newPrivate'), icon: '🔒', run: () => void create({ visibility: 'private' }) },
    { label: t('papers'), icon: '☰', run: () => navigate(`/ws/${ws}/papers`) },
    ...(paperId ? [{ label: t('history'), icon: '🕘', run: () => layout.setDrawer(true) }] : []),
    { label: t('trash'), icon: '🗑', run: () => navigate(`/ws/${ws}/trash`) },
    { label: t('settings'), icon: '⚙', run: () => navigate('/settings') },
    { label: t('allWorkspaces'), icon: '▦', run: () => navigate('/workspaces') },
    { label: t('switchLanguage'), icon: 'あ', run: () => setLang(document.documentElement.lang === 'ja' ? 'en' : 'ja') },
  ]
  const active = location.pathname.endsWith('/papers') ? 'papers' : location.pathname.endsWith('/trash') ? 'trash' : location.pathname.startsWith('/settings') ? 'settings' : undefined
  const showDrawer = drawer && !!paperId

  if (wsMatch?.params.ws && loaded && !workspace) {
    return <div className="empty">{t('workspaceNotFound')}</div>
  }
  return (
    <div className={['app', showDrawer ? 'drawer-open' : '', sbHidden ? 'nosb' : '', sbMobile ? 'sb-mobile' : ''].filter(Boolean).join(' ')}>
      {ws && (
        <Sidebar me={me} workspaces={workspaces} ws={ws} data={sidebar} current={paperId ?? (location.pathname === `/ws/${ws}` ? sidebar?.home?.id : undefined)} expand={expand} version={version} active={active}
          onSearch={() => setPalette(true)} onNew={() => void create({})} onNewInside={(parent) => void create({ parent })} onSignOut={onSignOut} onChanged={onChanged} />
      )}
      <div className="sb-scrim" onClick={() => setSbMobile(false)} />
      <Routes>
        <Route path="/ws/:ws" element={loaded && workspace ? <PaperRoute me={me} workspaces={workspaces} sidebar={sidebar} onChanged={onChanged} layout={layout} onView={setView} home /> : null} />
        <Route path="/ws/:ws/p/:paper" element={<PaperRoute me={me} workspaces={workspaces} sidebar={sidebar} onChanged={onChanged} layout={layout} onView={setView} />} />
        <Route path="/ws/:ws/p/:paper/latest" element={<PaperRoute me={me} workspaces={workspaces} sidebar={sidebar} onChanged={onChanged} layout={layout} onView={setView} version="latest" />} />
        <Route path="/ws/:ws/p/:paper/v/:n" element={<PaperRoute me={me} workspaces={workspaces} sidebar={sidebar} onChanged={onChanged} layout={layout} onView={setView} version="n" />} />
        <Route path="/ws/:ws/papers" element={<PapersPage ws={ws} onNew={() => void create({})} onNewPrivate={() => void create({ visibility: 'private' })} layoutTop={<Top title={t('papers')} layout={layout} />} />} />
        <Route path="/ws/:ws/trash" element={<TrashPage ws={ws} onChanged={onChanged} layoutTop={<Top title={t('trash')} layout={layout} />} />} />
        <Route path="/ws/:ws/search" element={<Navigate to={`/ws/${ws}`} replace />} />
        <Route path="/settings" element={<SettingsPage layoutTop={<Top title={t('settings')} layout={layout} />} />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      {palette && ws && (
        <Palette ws={ws} wsName={workspace?.name ?? ws} under={under} recent={sidebar?.recent ?? []} commands={commands} onClose={() => setPalette(false)} />
      )}
      {help && <Modal title={t('shortcuts')} onClose={() => setHelp(false)} actions={[{ label: t('done'), kind: 'primary' }]}><ShortcutList /></Modal>}
    </div>
  )
}

function PaperRoute({ me, workspaces, sidebar, onChanged, layout, onView, home, version }: {
  me: Me
  workspaces: Workspace[]
  sidebar?: SidebarData
  onChanged: () => void
  layout: Layout
  onView: (v: PaperView | undefined) => void
  home?: boolean
  version?: 'latest' | 'n'
}) {
  const { ws = '', paper, n } = useParams()
  const workspace = workspaces.find((w) => w.id === ws)
  const id = home ? workspace?.home_paper_id ?? '' : paper ?? ''
  if (!id) return null
  const v = version === 'latest' ? 'latest' : version === 'n' ? Number(n) : undefined
  return <PaperPage key={`${id}:${v ?? ''}`} ws={ws} paperId={id} version={v} me={me} workspaces={workspaces} sidebar={sidebar} onChanged={onChanged} layout={layout} onView={onView} />
}

function Authed({ me }: { me: Me }) {
  const navigate = useNavigate()
  const location = useLocation()
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [groups, setGroups] = useState<WorkspaceGroup[]>([])
  const [loaded, setLoaded] = useState(false)
  const onChooser = location.pathname === '/workspaces'
  useEffect(() => {
    void api.listWorkspaces().then((r) => {
      setWorkspaces(r.workspaces)
      setGroups(r.groups ?? [])
      setLoaded(true)
    }).catch(() => setLoaded(true))
  }, [onChooser])
  const signOut = useCallback(() => {
    void api.logout().catch(() => undefined).finally(() => navigate('/login'))
  }, [navigate])

  if (location.pathname === '/') return <Root workspaces={workspaces} loaded={loaded} />
  if (location.pathname === '/workspaces') return loaded ? <WorkspacesPage me={me} workspaces={workspaces} groups={groups} onSignOut={signOut} /> : null
  if (location.pathname === '/settings/tokens') return <Navigate to="/settings" replace />
  return <Shell me={me} workspaces={workspaces} loaded={loaded} onSignOut={signOut} />
}

export function App() {
  const location = useLocation()
  const navigate = useNavigate()
  const [me, setMe] = useState<Me | null | undefined>(undefined)

  useEffect(() => {
    api.me().then(setMe).catch((e: unknown) => {
      if (e instanceof ApiError && e.status === 401) setMe(null)
      else setMe(null)
    })
  }, [])

  // A call that finds the sign-in gone (it expired) goes back to sign-in.
  useEffect(() => {
    const onGone = () => {
      if (window.location.pathname === '/login') return
      navigate(`/login?return_to=${encodeURIComponent(window.location.pathname)}&error=expired`, { replace: true })
      setMe(null)
    }
    window.addEventListener(UNAUTHORIZED, onGone)
    return () => window.removeEventListener(UNAUTHORIZED, onGone)
  }, [navigate])

  if (location.pathname === '/login') return <ToastProvider><LoginPage /></ToastProvider>
  if (me === undefined) return null
  if (me === null) return <Navigate to={`/login?return_to=${encodeURIComponent(location.pathname)}`} replace />
  return <ToastProvider><Authed me={me} /></ToastProvider>
}
