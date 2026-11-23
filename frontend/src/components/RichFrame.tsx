import { useEffect, useMemo, useRef, useState } from 'react'

// Renders AI-authored HTML or SVG in a sandboxed iframe. The frame has
// scripts but no same-origin access, so it cannot read cookies, call the
// API or touch the page. A tiny script inside reports its height so the
// frame fits its content. Values the author marked with data-field are
// filled from the block's props.fields and, where people may edit, made
// editable in place: the frame then reports only {field, text}, which the
// page checks before writing (spec: R5, R20).

const SIZE_SCRIPT = `<script>(function(){function s(){parent.postMessage({papersFrameHeight:Math.ceil(document.documentElement.scrollHeight)},'*')}new ResizeObserver(s).observe(document.documentElement);addEventListener('load',s);s()})()</script>`

const BASE_STYLE = `<style>html,body{margin:0;padding:0;background:transparent}body{font-family:system-ui,sans-serif;color:#1a2230}@media (prefers-color-scheme:dark){body{color:#e6e9ee}}svg{max-width:100%;height:auto;display:block;margin:0 auto}</style>`

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

export function buildSrcDoc(kind: 'html' | 'svg', content: string, fields: Record<string, string> = {}, editable = false): string {
  const extra = SIZE_SCRIPT + fieldScript(fields, editable)
  if (kind === 'svg') {
    return `<!doctype html><html><head><meta charset="utf-8">${BASE_STYLE}</head><body>${content}${extra}</body></html>`
  }
  const isDocument = /<html[\s>]/i.test(content)
  if (isDocument) {
    return /<\/body>/i.test(content) ? content.replace(/<\/body>/i, `${extra}</body>`) : content + extra
  }
  return `<!doctype html><html><head><meta charset="utf-8">${BASE_STYLE}</head><body>${content}${extra}</body></html>`
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

export function RichFrame({ kind, content, height, title, fields, onField }: {
  kind: 'html' | 'svg'
  content: string
  height?: number
  title: string
  fields?: Record<string, string>
  /** Set where people may edit the values. */
  onField?: (name: string, value: string) => void
}) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [auto, setAuto] = useState(160)
  const editable = !!onField
  const srcDoc = useMemo(() => buildSrcDoc(kind, content, fields, editable), [kind, content, fields, editable])
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
      ref={ref}
      title={title}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      srcDoc={srcDoc}
      style={{ height: `${height && height > 0 ? height : auto}px` }}
    />
  )
}
