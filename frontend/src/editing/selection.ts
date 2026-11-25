import type { Op } from '../api/types'

// Selecting several top-level blocks at once (spec: multi-block selection).
// The selection is the range between two blocks: the one it started from
// (anchor) and the one being moved (focus). The blocks in it are worked out
// from the current list each time, so blocks others add inside the range
// are selected too, and nothing about the selection is stored.

export interface Selection {
  anchor: string
  focus: string
}

interface Item {
  id: string
}

/** The selection still valid against the list: an end that is gone falls back to the other end. */
export function normalize(list: Item[], sel: Selection | null): Selection | null {
  if (!sel) return null
  const has = (id: string) => list.some((b) => b.id === id)
  const a = has(sel.anchor)
  const f = has(sel.focus)
  if (a && f) return sel
  if (a) return { anchor: sel.anchor, focus: sel.anchor }
  if (f) return { anchor: sel.focus, focus: sel.focus }
  return null
}

/** The IDs of the selected blocks, in list order. */
export function selectedIds(list: Item[], sel: Selection | null): string[] {
  const s = normalize(list, sel)
  if (!s) return []
  const a = list.findIndex((b) => b.id === s.anchor)
  const f = list.findIndex((b) => b.id === s.focus)
  return list.slice(Math.min(a, f), Math.max(a, f) + 1).map((b) => b.id)
}

export interface Span {
  id: string
  top: number
  bottom: number
}

/**
 * The blocks whose vertical extent overlaps the band between y0 (where the
 * press started) and y1 (where the pointer is). Only the vertical position
 * counts: blocks span the column. The anchor is the end nearer to y0.
 */
export function overlapping(spans: Span[], y0: number, y1: number): Selection | null {
  const lo = Math.min(y0, y1)
  const hi = Math.max(y0, y1)
  const hit = spans.filter((s) => s.bottom >= lo && s.top <= hi)
  const first = hit[0]
  const last = hit[hit.length - 1]
  if (!first || !last) return null
  return y0 <= y1 ? { anchor: first.id, focus: last.id } : { anchor: last.id, focus: first.id }
}

/** The block under a vertical position, or the nearest one above or below it. */
export function blockAt(spans: Span[], y: number): string | undefined {
  if (spans.length === 0) return undefined
  for (const s of spans) {
    if (y < s.bottom) return s.id
  }
  return spans[spans.length - 1]?.id
}

/**
 * The batch moving the selected blocks (list order) to just after `after`
 * ('' = the top), keeping their order: the first goes after `after`, each
 * next one after the one before it. Empty when nothing would change.
 */
export function moveOps(list: Item[], ids: string[], after: string): Op[] {
  const first = ids[0]
  if (!first || ids.includes(after)) return []
  const i = list.findIndex((b) => b.id === first)
  const before = i > 0 ? list[i - 1]?.id ?? '' : ''
  if (after === before && contiguous(list, ids)) return []
  return ids.map((id, k) => ({ op: 'move', block_id: id, after: k === 0 ? after : ids[k - 1] }))
}

function contiguous(list: Item[], ids: string[]): boolean {
  const i = list.findIndex((b) => b.id === ids[0])
  return ids.every((id, k) => list[i + k]?.id === id)
}

/**
 * Where a drop line next to block `target` (before or after it) puts the
 * selected blocks, as the block they go after; undefined when the line is
 * inside the selection or touches it, which changes nothing.
 */
export function dropAfter(list: Item[], ids: string[], target: { id: string; before: boolean }): string | undefined {
  const at = list.findIndex((b) => b.id === target.id)
  if (at < 0) return undefined
  // the gap the line marks, as the index of the block below it
  const gap = target.before ? at : at + 1
  const idx = ids.map((id) => list.findIndex((b) => b.id === id))
  const lo = Math.min(...idx)
  const hi = Math.max(...idx)
  if (gap >= lo && gap <= hi + 1) return undefined
  const rest = list.filter((b) => !ids.includes(b.id))
  const below = list[gap]
  const j = below ? rest.findIndex((b) => b.id === below.id) : rest.length
  return j > 0 ? rest[j - 1]?.id ?? '' : ''
}

/** Where ⌥↑ / ⌥↓ moves the selected blocks, as the block they go after; undefined at the top or bottom. */
export function stepAfter(list: Item[], ids: string[], dir: -1 | 1): string | undefined {
  const idx = ids.map((id) => list.findIndex((b) => b.id === id))
  const lo = Math.min(...idx)
  const hi = Math.max(...idx)
  if (dir === -1) return lo > 0 ? (lo >= 2 ? list[lo - 2]?.id ?? '' : '') : undefined
  return hi < list.length - 1 ? list[hi + 1]?.id : undefined
}
