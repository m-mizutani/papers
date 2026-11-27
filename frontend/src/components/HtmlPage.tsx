import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api/client'
import type { DatabaseLink } from '../api/types'
import { useTranslation } from '../i18n'
import { useFrameDatabase } from './frameDatabase'
import { FRAME_SANDBOX, hash, withHead } from './RichFrame'

// The body of an HTML paper: one document written through the API, shown
// whole in a sandboxed frame where its scripts run. The frame has no
// same-origin access, so the document cannot read cookies, call papers'
// API or touch this page. Documents never change, so a new document (a
// new attachment) is a new fetch.

/** The srcdoc of an HTML paper's frame; db puts papers.db before the document. */
export function buildPageDoc(html: string, external: boolean, db = false): string {
  return withHead(html, external, db)
}

type Load = { state: 'loading' } | { state: 'ready'; html: string } | { state: 'failed' }

export function HtmlPage({ ws, paperId, attachmentId, title, external, databases, connect = false }: {
  ws: string
  paperId: string
  /** The document to show: the html_page block's attachment, of the current state or a version. */
  attachmentId?: string
  title: string
  /** Whether the workspace lets the document load from other hosts. */
  external: boolean
  /** The databases the document is connected to (the html_page block's props.databases). */
  databases?: DatabaseLink[]
  /** Whether to run the document's database calls; false answers them with unavailable. */
  connect?: boolean
}) {
  const { t } = useTranslation()
  const [load, setLoad] = useState<Load>({ state: 'loading' })
  const [attempt, setAttempt] = useState(0)
  const ref = useRef<HTMLIFrameElement>(null)
  const db = !!databases && databases.length > 0
  const srcDoc = useMemo(() => (load.state === 'ready' ? buildPageDoc(load.html, external, db) : ''), [load, external, db])
  useFrameDatabase(ref, ws, db ? databases : undefined, connect, srcDoc)

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
        : <iframe key={db ? `${hash(srcDoc)}:${connect}:${JSON.stringify(databases)}` : undefined} ref={ref} title={t('htmlFrameTitle', { title })} sandbox={FRAME_SANDBOX} referrerPolicy="no-referrer" srcDoc={srcDoc} />}
    </div>
  )
}
