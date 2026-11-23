import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, MemoryRouter } from 'react-router'
import { App } from './App'
import { detectLang } from './i18n'
import { isDemo } from './utils/demo'
import { applyTheme, storedTheme } from './utils/prefs'
import './styles/global.css'

document.documentElement.lang = detectLang()
applyTheme(storedTheme())

async function start() {
  if (isDemo) {
    const { installMock } = await import('./mock/install')
    installMock()
  }
  const root = document.getElementById('root')
  if (!root) return
  // The demo is hosted where only the page itself is served, so it keeps
  // its routes in memory instead of the address bar.
  const Router = isDemo ? MemoryRouter : BrowserRouter
  createRoot(root).render(
    <StrictMode>
      <Router>
        <App />
      </Router>
    </StrictMode>,
  )
}

void start()
