import { storedLang } from '../utils/prefs'
import { en } from './en'
import { ja } from './ja'
import type { MsgKey } from './keys'

export type { MsgKey }

const dictionaries = { en, ja }
export type Lang = keyof typeof dictionaries

/** The chosen language (Settings), else the browser's. */
export function detectLang(): Lang {
  const chosen = typeof window === 'undefined' ? null : storedLang()
  if (chosen) return chosen
  return typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('ja') ? 'ja' : 'en'
}

const lang = detectLang()

export function t(key: MsgKey, params: Record<string, string | number> = {}): string {
  return dictionaries[lang][key].replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? `{${name}}`))
}

/** A message with elements (a mention) in its parameters, as a list of parts to render. */
export function tParts<T>(key: MsgKey, params: Record<string, T | string | number>): (T | string)[] {
  const out: (T | string)[] = []
  const re = /\{(\w+)\}/g
  const text = dictionaries[lang][key]
  let last = 0
  for (let m = re.exec(text); m; m = re.exec(text)) {
    if (m.index > last) out.push(text.slice(last, m.index))
    const v = params[m[1] ?? '']
    out.push(v === undefined ? m[0] : typeof v === 'number' ? String(v) : v)
    last = m.index + m[0].length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

/** "3 min ago" and so on, for times shown next to people and versions. */
export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return ''
  const d = now - Date.parse(iso)
  if (d < 45_000) return t('justNow')
  if (d < 3_600_000) return t('minutesAgo', { n: Math.round(d / 60_000) })
  if (d < 86_400_000) return t('hoursAgo', { n: Math.round(d / 3_600_000) })
  return t('daysAgo', { n: Math.round(d / 86_400_000) })
}

export function useTranslation() {
  return { t, lang }
}
