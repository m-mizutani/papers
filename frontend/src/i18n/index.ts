import { en } from './en'
import { ja } from './ja'
import type { MsgKey } from './keys'

export type { MsgKey }

const dictionaries = { en, ja }
export type Lang = keyof typeof dictionaries

export function detectLang(): Lang {
  return typeof navigator !== 'undefined' && navigator.language.toLowerCase().startsWith('ja') ? 'ja' : 'en'
}

const lang = detectLang()

export function t(key: MsgKey, params: Record<string, string | number> = {}): string {
  return dictionaries[lang][key].replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? `{${name}}`))
}

export function useTranslation() {
  return { t, lang }
}
