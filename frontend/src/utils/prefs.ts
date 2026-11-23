// Per-browser preferences: theme and language. Stored in localStorage; the
// app works the same when storage is unavailable.

export type Theme = 'system' | 'light' | 'dark'
export type LangPref = 'ja' | 'en'

const THEME = 'papers.theme'
const LANG = 'papers.lang'

function read(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) window.localStorage.removeItem(key)
    else window.localStorage.setItem(key, value)
  } catch {
    // storage may be unavailable; the choice then lasts for this page only
  }
}

export function storedTheme(): Theme {
  const v = read(THEME)
  return v === 'light' || v === 'dark' ? v : 'system'
}

export function applyTheme(theme: Theme) {
  if (theme === 'system') delete document.documentElement.dataset.theme
  else document.documentElement.dataset.theme = theme
}

export function setTheme(theme: Theme) {
  write(THEME, theme === 'system' ? null : theme)
  applyTheme(theme)
}

export function storedLang(): LangPref | null {
  const v = read(LANG)
  return v === 'ja' || v === 'en' ? v : null
}

/** Changing the language reloads the page so every text is redrawn. */
export function setLang(lang: LangPref) {
  write(LANG, lang)
  window.location.reload()
}

export function readPref(key: string): string | null {
  return read(`papers.${key}`)
}

export function writePref(key: string, value: string | null) {
  write(`papers.${key}`, value)
}
