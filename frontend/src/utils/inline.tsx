import type { ReactNode } from 'react'

// A small, safe inline Markdown renderer: **bold**, *italic*, `code`,
// ~~strike~~ and [links](url). It never injects HTML, and only http(s) and
// mailto links are rendered as anchors.

const TOKEN = /(`[^`]+`)|(\*\*[^*]+\*\*)|(~~[^~]+~~)|(\*[^*\s][^*]*\*)|(_[^_\s][^_]*_)|(\[[^\]]+\]\([^)\s]+\))/

export function safeHref(url: string): string | undefined {
  try {
    const u = new URL(url, window.location.origin)
    return ['http:', 'https:', 'mailto:'].includes(u.protocol) ? u.href : undefined
  } catch {
    return undefined
  }
}

export function renderInline(text: string, keyPrefix = 'i'): ReactNode[] {
  const out: ReactNode[] = []
  let rest = text
  let n = 0
  while (rest.length > 0) {
    const m = TOKEN.exec(rest)
    if (!m || m.index === undefined) {
      out.push(rest)
      break
    }
    if (m.index > 0) out.push(rest.slice(0, m.index))
    const tok = m[0]
    const key = `${keyPrefix}-${n++}`
    if (m[1]) out.push(<code key={key}>{tok.slice(1, -1)}</code>)
    else if (m[2]) out.push(<strong key={key}>{renderInline(tok.slice(2, -2), key)}</strong>)
    else if (m[3]) out.push(<s key={key}>{renderInline(tok.slice(2, -2), key)}</s>)
    else if (m[4] || m[5]) out.push(<em key={key}>{renderInline(tok.slice(1, -1), key)}</em>)
    else if (m[6]) {
      const close = tok.indexOf('](')
      const label = tok.slice(1, close)
      const href = safeHref(tok.slice(close + 2, -1))
      out.push(href ? <a key={key} href={href} target="_blank" rel="noopener noreferrer">{renderInline(label, key)}</a> : label)
    }
    rest = rest.slice(m.index + tok.length)
  }
  return out
}

/** Renders multi-line inline text, keeping line breaks. */
export function renderLines(text: string): ReactNode[] {
  const lines = text.split('\n')
  return lines.flatMap((line, i) => (i === 0 ? renderInline(line, `l${i}`) : [<br key={`br${i}`} />, ...renderInline(line, `l${i}`)]))
}
