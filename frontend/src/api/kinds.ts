import type { PaperKind } from './types'

// Kinds of papers seen so far (in the sidebar, in child cards, on opening),
// so a database opens its table at once instead of the editor first.
const kinds = new Map<string, PaperKind | ''>()

export const kindOf = (id: string): PaperKind | '' | undefined => kinds.get(id)
export const rememberKind = (id: string, kind: PaperKind | undefined) => { kinds.set(id, kind ?? '') }
