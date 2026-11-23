import type { Actor } from '../api/types'
import { t } from '../i18n'

// How writers are shown: a person by name, an AI as "Claude (for Aiko)",
// a person writing with a personal token "via API" (D7).

export function actorName(a: Actor | undefined): string {
  if (!a) return ''
  if (a.kind === 'agent') return `${a.agent_name || 'AI'} (${t('forUser', { name: a.name })})`
  if (a.kind === 'system') return 'papers'
  if (a.token_id) return `${a.name} (${t('viaApi')})`
  return a.name
}

/** The short form for tags on blocks: the agent's name, or a person's first name. */
export function actorShort(a: Actor | undefined): string {
  if (!a) return t('someone')
  if (a.kind === 'agent') return a.agent_name || 'AI'
  return a.name.split(/\s+/)[0] ?? a.name
}

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/)
  const s = parts.length > 1 ? `${parts[0]?.[0] ?? ''}${parts[1]?.[0] ?? ''}` : name.slice(0, 2)
  return s.toUpperCase()
}

// People's colours: readable with white text in both themes.
const PEOPLE = ['#2c57d6', '#c2417a', '#1f8a55', '#b55a12', '#0f7c8c', '#8a5a3c', '#5b5fc7']

export function actorColor(a: Actor | undefined): string {
  if (!a) return 'var(--fg-3)'
  if (a.kind === 'agent') return 'var(--ai)'
  let h = 0
  for (const c of a.user_id) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return PEOPLE[h % PEOPLE.length] ?? 'var(--accent)'
}
