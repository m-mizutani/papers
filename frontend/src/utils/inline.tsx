import type { ReactNode } from 'react'
import { UserMention } from '../components/UserMention'
import { parse, type MNode } from '../markup/markup'

// Renders the stored inline markup (D10) as React elements. It never
// injects HTML, and only http(s) and mailto links become anchors.

export function safeHref(url: string): string | undefined {
  try {
    const u = new URL(url, window.location.origin)
    return ['http:', 'https:', 'mailto:'].includes(u.protocol) ? u.href : undefined
  } catch {
    return undefined
  }
}

function lines(text: string, key: string): ReactNode[] {
  return text.split('\n').flatMap((part, i) => (i === 0 ? [part] : [<br key={`${key}-br${i}`} />, part]))
}

function nodes(ns: MNode[], key: string): ReactNode[] {
  return ns.map((n, i) => {
    const k = `${key}-${i}`
    const kids = () => nodes(n.children ?? [], k)
    switch (n.kind) {
      case 'text':
        return <span key={k}>{lines(n.text ?? '', k)}</span>
      case 'code':
        return <code key={k}>{n.text}</code>
      case 'bold':
        return <strong key={k}>{kids()}</strong>
      case 'italic':
        return <em key={k}>{kids()}</em>
      case 'strike':
        return <s key={k}>{kids()}</s>
      case 'link': {
        const href = safeHref(n.url ?? '')
        return href ? <a key={k} href={href} target="_blank" rel="noopener noreferrer">{kids()}</a> : <span key={k}>{kids()}</span>
      }
      case 'mention':
        return <UserMention key={k} id={n.user_id ?? ''} name={n.name ?? ''} />
      case 'span': {
        const cls = [n.color ? `mk-c-${n.color}` : '', n.bg ? `mk-bg-${n.bg}` : ''].filter(Boolean).join(' ')
        return <span key={k} className={cls}>{kids()}</span>
      }
    }
    return null
  })
}

/** Renders a block's markup; text that does not parse is shown as is. */
export function renderMarkup(text: string): ReactNode[] {
  try {
    return nodes(parse(text), 'm')
  } catch {
    return lines(text, 'raw')
  }
}
