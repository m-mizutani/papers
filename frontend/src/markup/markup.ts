// The twin of pkg/domain/markup: inline Markdown (bold, italic, strike,
// code, links), colour spans [text]{.red} / [text]{.bg-yellow} and
// mentions of users [@name]{user=ID}. Both
// implementations run testdata/markup/cases.json, so the editor writes the
// same canonical strings the server stores.

export type Kind = 'text' | 'bold' | 'italic' | 'strike' | 'code' | 'link' | 'span' | 'mention'

export interface MNode {
  kind: Kind
  text?: string
  children?: MNode[]
  url?: string
  color?: string
  bg?: string
  /** mention: the user's ID and the name written with it (without "@"). */
  user_id?: string
  name?: string
}

/** Whether id looks like a papers ID (22 characters of base62); the markup does not check that the user exists. */
export const validUserId = (id: string) => /^[0-9A-Za-z]{22}$/.test(id)

function mentionNode(label: string, attrs: string): MNode | undefined {
  const fields = attrs.split(/\s+/).filter(Boolean)
  if (fields.length !== 1 || !fields[0]?.startsWith('user=')) return undefined
  const id = fields[0].slice('user='.length)
  if (!validUserId(id)) return undefined
  let name = ''
  for (let i = 0; i < label.length; i++) {
    if (label[i] === '\\' && i + 1 < label.length && isPunct(label[i + 1])) i++
    name += label[i]
  }
  if (name.startsWith('@')) name = name.slice(1)
  if (name.trim() === '' || /[\n\r]/.test(name)) return undefined
  return { kind: 'mention', user_id: id, name }
}

export const PALETTE = ['gray', 'brown', 'orange', 'yellow', 'green', 'blue', 'purple', 'pink', 'red'] as const
export type PaletteColor = (typeof PALETTE)[number]

export class MarkupError extends Error {}

const isPunct = (c: string | undefined) => c !== undefined && c.length === 1 && /[!-/:-@[-`{-~]/.test(c)

function runeBefore(s: string, i: number): string {
  if (i <= 0) return ' '
  const lo = s.charCodeAt(i - 1)
  if (lo >= 0xdc00 && lo <= 0xdfff && i >= 2) return s.slice(i - 2, i)
  return s[i - 1] ?? ' '
}

function runeAt(s: string, i: number): string {
  if (i >= s.length) return ' '
  const cp = s.codePointAt(i)
  return cp === undefined ? ' ' : String.fromCodePoint(cp)
}

const isAlnum = (r: string) => /^[\p{L}\p{Nd}]$/u.test(r)
const isSpace = (r: string) => /^\s$/u.test(r)

function codeEnd(s: string, i: number): [number, number] {
  let n = 0
  while (i + n < s.length && s[i + n] === '`') n++
  let j = i + n
  while (j < s.length) {
    if (s[j] !== '`') {
      j++
      continue
    }
    let m = 0
    while (j + m < s.length && s[j + m] === '`') m++
    if (m === n) return [n, j + m]
    j += m
  }
  return [n, -1]
}

function canOpen(s: string, i: number, delim: string): boolean {
  const after = runeAt(s, i + delim.length)
  if (isSpace(after) || i + delim.length >= s.length) return false
  if (delim[0] === '_' && isAlnum(runeBefore(s, i))) return false
  if (delim.length === 1 && s.startsWith(delim + delim, i)) return false
  return true
}

function canClose(s: string, j: number, delim: string): boolean {
  if (isSpace(runeBefore(s, j))) return false
  if (delim[0] === '_' && isAlnum(runeAt(s, j + delim.length))) return false
  if (delim.length === 1 && s.startsWith(delim + delim, j)) return false
  return true
}

function findClose(s: string, from: number, delim: string): number {
  for (let j = from; j < s.length;) {
    if (s[j] === '\\' && j + 1 < s.length && isPunct(s[j + 1])) {
      j += 2
      continue
    }
    if (s[j] === '`') {
      const [, end] = codeEnd(s, j)
      if (end > 0) {
        j = end
        continue
      }
    } else if (s.startsWith(delim, j) && canClose(s, j, delim)) {
      return j
    }
    j++
  }
  return -1
}

function findBracket(s: string, i: number): number {
  let depth = 0
  for (let j = i; j < s.length; j++) {
    switch (s[j]) {
      case '\\':
        if (j + 1 < s.length && isPunct(s[j + 1])) j++
        break
      case '`': {
        const [, end] = codeEnd(s, j)
        if (end > 0) j = end - 1
        break
      }
      case '[':
        depth++
        break
      case ']':
        depth--
        if (depth === 0) return j
    }
  }
  return -1
}

class Parser {
  err?: MarkupError

  parse(s: string): MNode[] {
    const out: MNode[] = []
    let text = ''
    const flush = () => {
      if (text) {
        out.push({ kind: 'text', text })
        text = ''
      }
    }
    for (let i = 0; i < s.length;) {
      const c = s[i] as string
      if (c === '\\' && i + 1 < s.length && isPunct(s[i + 1])) {
        text += s[i + 1]
        i += 2
        continue
      }
      if (c === '`') {
        const [n, end] = codeEnd(s, i)
        if (end < 0) {
          text += s.slice(i, i + n)
          i += n
          continue
        }
        let inner = s.slice(i + n, end - n)
        if (inner.length >= 2 && inner[0] === ' ' && inner[inner.length - 1] === ' ' && inner.trim() !== '') inner = inner.slice(1, -1)
        flush()
        out.push({ kind: 'code', text: inner })
        i = end
        continue
      }
      if (s.startsWith('**', i) || s.startsWith('~~', i) || s.startsWith('__', i)) {
        const delim = s.slice(i, i + 2)
        if (canOpen(s, i, delim)) {
          const j = findClose(s, i + 2, delim)
          if (j > i + 2) {
            flush()
            out.push({ kind: delim === '~~' ? 'strike' : 'bold', children: this.parse(s.slice(i + 2, j)) })
            i = j + 2
            continue
          }
        }
        text += delim
        i += 2
        continue
      }
      if (c === '*' || c === '_') {
        if (canOpen(s, i, c)) {
          const j = findClose(s, i + 1, c)
          if (j > i + 1) {
            flush()
            out.push({ kind: 'italic', children: this.parse(s.slice(i + 1, j)) })
            i = j + 1
            continue
          }
        }
      } else if (c === '[') {
        const j = findBracket(s, i)
        if (j > i) {
          const label = s.slice(i + 1, j)
          if (s[j + 1] === '(') {
            const k = s.indexOf(')', j + 2)
            if (k >= 0) {
              let url = s.slice(j + 2, k).trim()
              const angled = url.startsWith('<') && url.endsWith('>')
              if (angled) url = url.slice(1, -1)
              if (url !== '' && (angled || !/[ \t\n]/.test(url)) && !/[<>\n]/.test(url)) {
                flush()
                out.push({ kind: 'link', url, children: this.parse(label) })
                i = k + 1
                continue
              }
            }
          }
          if (s[j + 1] === '{') {
            const k = s.indexOf('}', j + 2)
            if (k >= 0) {
              const m = mentionNode(label, s.slice(j + 2, k))
              if (m) {
                flush()
                out.push(m)
                i = k + 1
                continue
              }
              const n = this.span(s.slice(j + 2, k))
              if (n) {
                flush()
                n.children = this.parse(label)
                out.push(n)
                i = k + 1
                continue
              }
            }
          }
        }
      }
      text += c
      i++
    }
    flush()
    return out
  }

  span(attrs: string): MNode | undefined {
    const fields = attrs.split(/\s+/).filter(Boolean)
    if (fields.length === 0) return undefined
    const n: MNode = { kind: 'span' }
    for (const f of fields) {
      if (!f.startsWith('.')) return undefined
      let name = f.slice(1)
      let bg = false
      if (name.startsWith('bg-')) {
        name = name.slice(3)
        bg = true
      }
      if (!(PALETTE as readonly string[]).includes(name)) {
        this.err ??= new MarkupError(`unknown colour ${f}; use one of ${PALETTE.join(', ')}`)
        return undefined
      }
      if (bg) n.bg = name
      else n.color = name
    }
    return n
  }
}

function sameFormat(a: MNode, b: MNode): boolean {
  if (a.kind === 'link') return a.url === b.url
  if (a.kind === 'span') return (a.color ?? '') === (b.color ?? '') && (a.bg ?? '') === (b.bg ?? '')
  return true
}

function appendNodes(out: MNode[], nodes: MNode[]): MNode[] {
  for (const n of nodes) {
    const last = out[out.length - 1]
    if (last && last.kind === n.kind && n.kind !== 'mention' && sameFormat(last, n)) {
      if (n.kind === 'text') {
        last.text = (last.text ?? '') + (n.text ?? '')
        continue
      }
      if (n.kind !== 'code') {
        last.children = appendNodes([], [...(last.children ?? []), ...(n.children ?? [])])
        continue
      }
    }
    out.push(n)
  }
  return out
}

export function merge(nodes: MNode[]): MNode[] {
  let out: MNode[] = []
  for (const n of nodes) {
    if (n.kind === 'mention') {
      out.push(n)
      continue
    }
    if (n.kind !== 'text' && n.kind !== 'code') {
      n.children = merge(n.children ?? [])
      if (n.children.length === 0) continue
      if (n.kind === 'span' && !n.color && !n.bg) {
        out = appendNodes(out, n.children)
        continue
      }
      const flat: MNode[] = []
      for (const c of n.children) {
        if (c.kind === n.kind && n.kind !== 'link' && n.kind !== 'span') flat.push(...(c.children ?? []))
        else flat.push(c)
      }
      n.children = appendNodes([], flat)
    }
    if (n.kind === 'text' && !n.text) continue
    out = appendNodes(out, [n])
  }
  return out
}

export function parse(src: string): MNode[] {
  const p = new Parser()
  const nodes = p.parse(src)
  if (p.err) throw p.err
  return merge(nodes)
}

function escapeText(s: string): string {
  let out = ''
  for (let i = 0; i < s.length; i++) {
    const c = s[i] as string
    switch (c) {
      case '\\':
        if (i + 1 >= s.length || isPunct(s[i + 1])) out += '\\'
        break
      case '*':
      case '`':
      case '[':
        out += '\\'
        break
      case '~':
        if (s[i + 1] === '~' || (i > 0 && s[i - 1] === '~')) out += '\\'
        break
      case '_':
        if (!(isAlnum(runeBefore(s, i)) && isAlnum(runeAt(s, i + 1)))) out += '\\'
        break
    }
    out += c
  }
  return out
}

export function render(nodes: MNode[]): string {
  let out = ''
  for (const n of nodes) {
    switch (n.kind) {
      case 'text':
        out += escapeText(n.text ?? '')
        break
      case 'code': {
        const t = n.text ?? ''
        let fence = '`'
        while (t.includes(fence)) fence += '`'
        const pad = t.startsWith('`') || t.endsWith('`') || (t.startsWith(' ') && t.endsWith(' ') && t.trim() !== '')
        out += fence + (pad ? ` ${t} ` : t) + fence
        break
      }
      case 'bold':
        out += `**${render(n.children ?? [])}**`
        break
      case 'italic':
        out += `*${render(n.children ?? [])}*`
        break
      case 'strike':
        out += `~~${render(n.children ?? [])}~~`
        break
      case 'link':
        out += `[${render(n.children ?? [])}](${(n.url ?? '').replaceAll(' ', '%20').replaceAll(')', '%29')})`
        break
      case 'span': {
        const cls = [n.color ? `.${n.color}` : '', n.bg ? `.bg-${n.bg}` : ''].filter(Boolean).join(' ')
        out += `[${render(n.children ?? [])}]{${cls}}`
        break
      }
      case 'mention':
        out += `[@${(n.name ?? '').replace(/[\\[\]`]/g, (c) => `\\${c}`)}]{user=${n.user_id ?? ''}}`
        break
    }
  }
  return out
}

export function normalize(src: string): string {
  return render(parse(src))
}

export function plainText(src: string): string {
  let nodes: MNode[]
  try {
    nodes = parse(src)
  } catch {
    return src
  }
  const walk = (ns: MNode[]): string =>
    ns.map((n) => (n.kind === 'text' || n.kind === 'code' ? (n.text ?? '') : n.kind === 'mention' ? `@${n.name ?? ''}` : walk(n.children ?? []))).join('')
  return walk(nodes)
}
