import { useEffect, useMemo, useRef, useState } from 'react'
import type { DatabaseLink } from '../api/types'
import { DB_SCRIPT, useFrameDatabase } from './frameDatabase'

// Renders AI-authored HTML or SVG in a sandboxed iframe. The frame has
// scripts but no same-origin access, so it cannot read cookies, call the
// API or touch the page. A tiny script inside reports its height so the
// frame fits its content. Values the author marked with data-field are
// filled from the block's props.fields and, where people may edit, made
// editable in place: the frame then reports only {field, text}, which the
// page checks before writing (spec: R5, R20).

const SIZE_SCRIPT = `<script>(function(){function s(){parent.postMessage({papersFrameHeight:Math.ceil(document.documentElement.scrollHeight)},'*')}new ResizeObserver(s).observe(document.documentElement);addEventListener('load',s);s()})()</script>`

const BASE_STYLE = `<style>html,body{margin:0;padding:0;background:transparent}body{font-family:system-ui,sans-serif;color:#1a2230}@media (prefers-color-scheme:dark){body{color:#e6e9ee}}svg{max-width:100%;height:auto;display:block;margin:0 auto}</style>`

/** The policy for a workspace whose HTML may not load anything it does not embed (model.SandboxCSP). */
export const SANDBOX_CSP = "default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; worker-src blob:"

/** The sandbox flags of an HTML paper's frame: scripts only; no popups, never papers' origin. */
export const FRAME_SANDBOX = 'allow-scripts'

const DOCTYPE = /^\s*<!doctype[^>]*>/i

/**
 * Puts the Content-Security-Policy that keeps a document from loading
 * anything from other hosts before everything the document has, right
 * after the doctype, so nothing in it runs or loads first. A policy can only
 * be tightened later, so the document cannot lift it.
 */
export function withSandboxCSP(doc: string): string {
  return withHead(doc, false, false)
}

/**
 * Puts, right after the doctype and before everything the document has, the
 * policy that keeps it from loading from other hosts (unless external) and
 * the script that defines papers.db (when db), so nothing of the document
 * runs or loads first. A policy can only be tightened later, and papers
 * cannot be redefined.
 */
export function withHead(doc: string, external: boolean, db: boolean): string {
  if (external && !db) return doc
  const csp = external ? '' : `<meta http-equiv="Content-Security-Policy" content="${SANDBOX_CSP}">`
  return `<!doctype html>${csp}${db ? DB_SCRIPT : ''}${doc.replace(DOCTYPE, '')}`
}

const FIELD_NAME = /^[a-z][a-z0-9_]{0,31}$/
export const MAX_FIELD_BYTES = 200

function fieldScript(fields: Record<string, string>, editable: boolean): string {
  if (Object.keys(fields).length === 0) return ''
  const json = JSON.stringify(fields).replace(/</g, '\\u003c')
  const edit = editable
    ? "el.contentEditable='plaintext-only';el.spellcheck=false;var start=F[k];el.addEventListener('focus',function(){start=el.textContent});el.addEventListener('keydown',function(e){if(e.key==='Enter'&&!e.isComposing&&e.keyCode!==229){e.preventDefault();el.blur()}else if(e.key==='Escape'){el.textContent=start;el.blur()}});el.addEventListener('blur',function(){var v=(el.textContent||'').trim();if(v!==start)parent.postMessage({papersField:1,k:k,v:v},'*')});"
    : ''
  return `<style>[data-field][contenteditable]{border-radius:4px;cursor:text;outline:none}[data-field][contenteditable]:hover{box-shadow:0 0 0 1px rgba(44,87,214,.5)}[data-field][contenteditable]:focus{box-shadow:0 0 0 2px #2c57d6}</style><script>(function(){var F=${json};document.querySelectorAll('[data-field]').forEach(function(el){var k=el.getAttribute('data-field');if(!Object.prototype.hasOwnProperty.call(F,k))return;el.textContent=F[k];${edit}})})()</script>`
}

export function buildSrcDoc(kind: 'html' | 'svg', content: string, fields: Record<string, string> = {}, editable = false, external = true, db = false): string {
  return withHead(frameDoc(kind, content, SIZE_SCRIPT + fieldScript(fields, editable)), external, db)
}

function frameDoc(kind: 'html' | 'svg', content: string, extra: string): string {
  if (kind === 'svg') {
    return `<!doctype html><html><head><meta charset="utf-8">${BASE_STYLE}</head><body>${content}${extra}</body></html>`
  }
  const isDocument = /<html[\s>]/i.test(content)
  if (isDocument) {
    return /<\/body>/i.test(content) ? content.replace(/<\/body>/i, `${extra}</body>`) : content + extra
  }
  return `<!doctype html><html><head><meta charset="utf-8">${BASE_STYLE}</head><body>${content}${extra}</body></html>`
}

/** A short fingerprint of a document, to give each document its own frame. */
export function hash(s: string): string {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 0x01000193)
  return (h >>> 0).toString(36)
}

/** The title an AI gave its drawing (<title>), shown above the frame. */
export function frameTitle(content: string): string | undefined {
  const m = /<title>([^<]*)<\/title>/i.exec(content)
  return m?.[1]?.trim() || undefined
}

/** A field change reported by a frame, if it is one the block has and it fits. */
export function acceptField(data: unknown, fields: Record<string, string> | undefined): { name: string; value: string } | null {
  if (!data || typeof data !== 'object') return null
  const d = data as { papersField?: unknown; k?: unknown; v?: unknown }
  if (d.papersField !== 1 || typeof d.k !== 'string' || typeof d.v !== 'string') return null
  if (!fields || !Object.prototype.hasOwnProperty.call(fields, d.k) || !FIELD_NAME.test(d.k)) return null
  if (/[\r\n]/.test(d.v) || new TextEncoder().encode(d.v).length > MAX_FIELD_BYTES) return null
  return { name: d.k, value: d.v }
}

export function RichFrame({ kind, content, height, title, fields, onField, external = true, ws = '', databases, connect = false }: {
  kind: 'html' | 'svg'
  content: string
  height?: number
  title: string
  fields?: Record<string, string>
  /** Whether the workspace lets the HTML load from other hosts. */
  external?: boolean
  /** Set where people may edit the values. */
  onField?: (name: string, value: string) => void
  /** The workspace and the databases the HTML is connected to (html only). */
  ws?: string
  databases?: DatabaseLink[]
  /** Whether to run the HTML's database calls; false answers them with unavailable. */
  connect?: boolean
}) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [auto, setAuto] = useState(160)
  const editable = !!onField
  const db = kind === 'html' && !!databases && databases.length > 0
  const srcDoc = useMemo(() => buildSrcDoc(kind, content, fields, editable, external, db), [kind, content, fields, editable, external, db])
  useFrameDatabase(ref, ws, db ? databases : undefined, connect, srcDoc)
  const fieldsRef = useRef(fields)
  fieldsRef.current = fields
  const onFieldRef = useRef(onField)
  onFieldRef.current = onField

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      // Only this block's own frame may report.
      if (!ref.current || e.source !== ref.current.contentWindow) return
      const h = (e.data as { papersFrameHeight?: unknown })?.papersFrameHeight
      if (typeof h === 'number' && h > 0) {
        setAuto(Math.min(h + 2, 4000))
        return
      }
      const f = acceptField(e.data, fieldsRef.current)
      if (f && onFieldRef.current && f.value !== fieldsRef.current?.[f.name]) onFieldRef.current(f.name, f.value)
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
  }, [])

  return (
    <iframe
      // A new document (or connection) is a new frame, so its first load is its own.
      key={db ? `${srcDoc.length}:${hash(srcDoc)}:${connect}:${JSON.stringify(databases)}` : undefined}
      ref={ref}
      title={title}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      srcDoc={srcDoc}
      style={{ height: `${height && height > 0 ? height : auto}px` }}
    />
  )
}
