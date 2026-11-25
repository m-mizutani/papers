import type { PaperListQuery } from '../api/types'

// The papers list's filters. The page's URL holds them (so a link can open
// one person's papers); the stored choice is used only when the URL has none.

export type Filters = Required<Pick<PaperListQuery, 'place' | 'creator' | 'visibility' | 'archived' | 'sort'>>

export const DEFAULTS: Filters = { place: 'unfiled', creator: 'me', visibility: 'all', archived: false, sort: 'updated' }

const KEYS = ['place', 'creator', 'visibility', 'archived', 'sort'] as const

const oneOf = <T extends string>(v: unknown, allowed: readonly T[], fallback: T): T =>
  typeof v === 'string' && (allowed as readonly string[]).includes(v) ? (v as T) : fallback

/** Fills in defaults for anything missing or out of range; my own ID is "me". */
export function clean(raw: Record<string, unknown>, myId: string): Filters {
  let creator = typeof raw.creator === 'string' && raw.creator !== '' ? raw.creator : DEFAULTS.creator
  if (creator === myId) creator = 'me'
  return {
    place: oneOf(raw.place, ['unfiled', 'filed', 'all'], DEFAULTS.place),
    creator,
    visibility: oneOf(raw.visibility, ['all', 'public', 'private'], DEFAULTS.visibility),
    archived: raw.archived === true || raw.archived === 'true',
    sort: oneOf(raw.sort, ['updated', 'created'], DEFAULTS.sort),
  }
}

/** The filters a page shows: the URL's when it has any, otherwise the stored ones. */
export function readFilters(params: URLSearchParams, stored: string | null, myId: string): Filters {
  if (KEYS.some((k) => params.has(k))) {
    const raw: Record<string, unknown> = {}
    for (const k of KEYS) if (params.has(k)) raw[k] = params.get(k)
    return clean(raw, myId)
  }
  try {
    return clean(JSON.parse(stored ?? '{}') as Record<string, unknown>, myId)
  } catch {
    return DEFAULTS
  }
}

/** The URL query for the filters: only what differs from the defaults. */
export function toQuery(f: Filters): URLSearchParams {
  const q = new URLSearchParams()
  for (const k of KEYS) if (f[k] !== DEFAULTS[k]) q.set(k, String(f[k]))
  return q
}

/** Whether a filter differs from its default (its button is then highlighted). */
export function isSet(f: Filters, k: (typeof KEYS)[number]): boolean {
  return f[k] !== DEFAULTS[k]
}
