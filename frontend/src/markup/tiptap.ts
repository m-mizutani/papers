import { merge, parse, render, type MNode } from './markup'

// Conversion between the stored markup and the editor's document (one
// paragraph of text runs with marks). The editor edits one block at a time
// (D11); the markup string stays the stored form.

export interface PMMark {
  type: 'bold' | 'italic' | 'strike' | 'code' | 'link' | 'color' | 'bg'
  attrs?: { href?: string; color?: string }
}

export interface PMText {
  type: 'text'
  text: string
  marks?: PMMark[]
}

export interface PMBreak {
  type: 'hardBreak'
}

/** A mention of a user: one inline atom the caret cannot enter. */
export interface PMMention {
  type: 'mention'
  attrs: { userId: string; name: string }
  marks?: PMMark[]
}

type PMInline = PMText | PMBreak | PMMention

export interface PMDoc {
  type: 'doc'
  content: { type: 'paragraph'; content?: PMInline[] }[]
}

function pushText(out: PMInline[], text: string, marks: PMMark[]) {
  text.split('\n').forEach((part, i) => {
    if (i > 0) out.push({ type: 'hardBreak' })
    if (part) out.push(marks.length ? { type: 'text', text: part, marks: [...marks] } : { type: 'text', text: part })
  })
}

function walk(nodes: MNode[], marks: PMMark[], out: PMInline[]) {
  for (const n of nodes) {
    switch (n.kind) {
      case 'text':
        pushText(out, n.text ?? '', marks)
        break
      case 'mention': {
        const m: PMMention = { type: 'mention', attrs: { userId: n.user_id ?? '', name: n.name ?? '' } }
        if (marks.length) m.marks = [...marks]
        out.push(m)
        break
      }
      case 'code':
        pushText(out, n.text ?? '', [...marks, { type: 'code' }])
        break
      case 'bold':
      case 'italic':
      case 'strike':
        walk(n.children ?? [], [...marks, { type: n.kind }], out)
        break
      case 'link':
        walk(n.children ?? [], [...marks, { type: 'link', attrs: { href: n.url ?? '' } }], out)
        break
      case 'span': {
        const m = [...marks]
        if (n.color) m.push({ type: 'color', attrs: { color: n.color } })
        if (n.bg) m.push({ type: 'bg', attrs: { color: n.bg } })
        walk(n.children ?? [], m, out)
        break
      }
    }
  }
}

/** Markup to an editor document. Invalid markup is shown as plain text. */
export function toDoc(src: string): PMDoc {
  let nodes: MNode[]
  try {
    nodes = parse(src)
  } catch {
    nodes = [{ kind: 'text', text: src }]
  }
  const content: PMInline[] = []
  walk(nodes, [], content)
  return { type: 'doc', content: [{ type: 'paragraph', content: content.length ? content : undefined }] }
}

// Marks nest outermost first in this order, so equal formatting renders the
// same way however it was applied.
function wrap(leaf: MNode, marks: PMMark[]): MNode {
  let n = leaf
  const has = (t: PMMark['type']) => marks.find((m) => m.type === t)
  if (has('strike')) n = { kind: 'strike', children: [n] }
  if (has('italic')) n = { kind: 'italic', children: [n] }
  if (has('bold')) n = { kind: 'bold', children: [n] }
  const color = has('color')?.attrs?.color
  const bg = has('bg')?.attrs?.color
  if (color || bg) n = { kind: 'span', color, bg, children: [n] }
  const link = has('link')?.attrs?.href
  if (link) n = { kind: 'link', url: link, children: [n] }
  return n
}

/** An editor document back to canonical markup. */
export function fromDoc(doc: PMDoc): string {
  const nodes: MNode[] = []
  doc.content.forEach((p, i) => {
    if (i > 0) nodes.push({ kind: 'text', text: '\n' })
    for (const c of p.content ?? []) {
      if (c.type === 'hardBreak') {
        nodes.push({ kind: 'text', text: '\n' })
        continue
      }
      const marks = c.marks ?? []
      if (c.type === 'mention') {
        nodes.push(wrap({ kind: 'mention', user_id: c.attrs.userId, name: c.attrs.name }, marks.filter((m) => m.type !== 'code')))
        continue
      }
      const code = marks.some((m) => m.type === 'code')
      nodes.push(wrap(code ? { kind: 'code', text: c.text } : { kind: 'text', text: c.text }, marks))
    }
  })
  return render(merge(nodes))
}
