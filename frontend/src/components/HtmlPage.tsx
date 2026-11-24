import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { useTranslation } from '../i18n'
import { FRAME_SANDBOX, withSandboxCSP } from './RichFrame'

// The body of an HTML paper: one document written through the API, shown
// whole in a sandboxed frame where its scripts run. The frame has no
// same-origin access, so the document cannot read cookies, call papers'
// API or touch this page. Documents never change, so a new document (a
// new attachment) is a new fetch.

/** The srcdoc of an HTML paper's frame. */
export function buildPageDoc(html: string, external: boolean): string {
  return external ? html : withSandboxCSP(html)
}

type Load = { state: 'loading' } | { state: 'ready'; html: string } | { state: 'failed' }

export function HtmlPage({ ws, paperId, attachmentId, title, external }: {
  ws: string
  paperId: string
  /** The document to show: the html_page block's attachment, of the current state or a version. */
  attachmentId?: string
  title: string
  /** Whether the workspace lets the document load from other hosts. */
  external: boolean
}) {
  const { t } = useTranslation()
  const [load, setLoad] = useState<Load>({ state: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (!attachmentId) {
      setLoad({ state: 'failed' })
      return
    }
    let alive = true
    setLoad({ state: 'loading' })
    api.fetchHTML(ws, paperId, attachmentId).then(
      (html) => { if (alive) setLoad({ state: 'ready', html }) },
      () => { if (alive) setLoad({ state: 'failed' }) },
    )
    return () => { alive = false }
  }, [ws, paperId, attachmentId, attempt])

  if (load.state === 'failed') {
    return (
      <div className="htmlpage-fail">
        <div className="banner warn" role="alert">
          <span className="sp">{t('htmlLoadFailed')}</span>
          {attachmentId && <button type="button" className="btn sm" onClick={() => setAttempt((n) => n + 1)}>{t('retry')}</button>}
        </div>
      </div>
    )
  }
  return (
    <div className="htmlpage" aria-busy={load.state === 'loading'}>
      {load.state === 'loading'
        ? <div className="htmlpage-skel"><div className="skel" style={{ width: '40%' }} /><div className="skel" /><div className="skel" style={{ width: '85%' }} /><div className="skel" style={{ width: '60%' }} /></div>
        : <iframe title={t('htmlFrameTitle', { title })} sandbox={FRAME_SANDBOX} referrerPolicy="no-referrer" srcDoc={buildPageDoc(load.html, external)} />}
    </div>
  )
}
