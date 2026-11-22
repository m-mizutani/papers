import { useEffect, useMemo, useRef, useState } from 'react'

// Renders AI-authored HTML or SVG in a sandboxed iframe. The frame has
// scripts but no same-origin access, so it cannot read cookies, call the
// API or touch the page. A tiny script inside reports its height so the
// frame fits its content.

const SIZE_SCRIPT = `<script>(function(){function s(){parent.postMessage({papersFrameHeight:Math.ceil(document.documentElement.scrollHeight)},'*')}new ResizeObserver(s).observe(document.documentElement);addEventListener('load',s);s()})()</script>`

const BASE_STYLE = `<style>html,body{margin:0;padding:0;background:transparent}body{font-family:system-ui,sans-serif;color:#1a2230}svg{max-width:100%;height:auto;display:block;margin:0 auto}</style>`

export function buildSrcDoc(kind: 'html' | 'svg', content: string): string {
  if (kind === 'svg') {
    return `<!doctype html><html><head><meta charset="utf-8">${BASE_STYLE}</head><body>${content}${SIZE_SCRIPT}</body></html>`
  }
  const isDocument = /<html[\s>]/i.test(content)
  if (isDocument) {
    // Inject the size reporter before </body> (or at the end).
    return /<\/body>/i.test(content) ? content.replace(/<\/body>/i, `${SIZE_SCRIPT}</body>`) : content + SIZE_SCRIPT
  }
  return `<!doctype html><html><head><meta charset="utf-8">${BASE_STYLE}</head><body>${content}${SIZE_SCRIPT}</body></html>`
}

export function RichFrame({ kind, content, height, title }: { kind: 'html' | 'svg'; content: string; height?: number; title: string }) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [auto, setAuto] = useState(160)
  const srcDoc = useMemo(() => buildSrcDoc(kind, content), [kind, content])

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (e.source !== ref.current?.contentWindow) return
      const h = (e.data as { papersFrameHeight?: unknown })?.papersFrameHeight
      if (typeof h === 'number' && h > 0) setAuto(Math.min(h + 2, 4000))
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
