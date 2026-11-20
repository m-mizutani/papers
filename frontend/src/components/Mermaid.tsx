import { useEffect, useId, useState } from 'react'
import { useTranslation } from '../i18n'

let mermaidPromise: Promise<typeof import('mermaid').default> | undefined

function loadMermaid() {
  mermaidPromise ??= import('mermaid').then((m) => {
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
