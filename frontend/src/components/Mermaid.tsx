import { useEffect, useId, useState } from 'react'
import { useTranslation } from '../i18n'

import { isDemo } from '../utils/demo'

type MermaidModule = { default: typeof import('mermaid').default }

// The demo is a single static page, so it loads Mermaid from a CDN instead
// of shipping it.
const MERMAID_CDN = 'https://cdn.jsdelivr.net/npm/mermaid@12.3.0/dist/mermaid.esm.min.mjs'

let mermaidPromise: Promise<typeof import('mermaid').default> | undefined

function loadMermaid() {
  const mod: Promise<MermaidModule> = isDemo ? import(/* @vite-ignore */ MERMAID_CDN) : import('mermaid')
  mermaidPromise ??= mod.then((m) => {
    m.default.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: 'neutral',
      fontFamily: 'Instrument Sans, system-ui, sans-serif',
    })
    return m.default
  })
  return mermaidPromise
}

export function Mermaid({ source }: { source: string }) {
  const { t } = useTranslation()
  const id = 'm' + useId().replace(/[^a-zA-Z0-9]/g, '')
  const [svg, setSvg] = useState<string>()
  const [error, setError] = useState<string>()

  useEffect(() => {
    let cancelled = false
    loadMermaid()
      .then((m) => m.render(id, source))
      .then((r) => {
        if (!cancelled) {
          setSvg(r.svg)
          setError(undefined)
        }
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [id, source])

  if (error) {
    return (
      <div className="b-rich-error">
        {t('renderFailed')}
        <pre>{error}</pre>
      </div>
    )
  }
  // securityLevel "strict" makes Mermaid sanitize its SVG output.
  return <div className="b-mermaid" dangerouslySetInnerHTML={{ __html: svg ?? '' }} />
}
