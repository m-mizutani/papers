import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { actorLabel } from '../api/paperState'
import type { Commit } from '../api/types'
import { useTranslation } from '../i18n'

const PAGE = 200

export function HistoryPanel({ ws, paper, revision, onClose }: { ws: string; paper: string; revision: number; onClose: () => void }) {
  const { t } = useTranslation()
  const [commits, setCommits] = useState<Commit[]>([])
  const [error, setError] = useState<string>()

  useEffect(() => {
    api.commits(ws, paper, Math.max(0, revision - PAGE))
      .then((cs) => setCommits([...cs].reverse()))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [ws, paper, revision])

  return (
    <aside className="panel" aria-label={t('history')}>
      <div className="panel-head">
        <h2>{t('history')}</h2>
        <button type="button" className="btn btn-quiet" onClick={onClose}>{t('close')}</button>
      </div>
      {error && <p className="login-error">{error}</p>}
      {commits.length === 0 && !error && <p className="help">{t('noHistory')}</p>}
      {commits.map((c) => {
        const agent = c.actor.kind === 'agent'
        const overwrote = c.changes.some((ch) => (ch.overwrote_version ?? 0) > 0)
        return (
          <div key={c.id} className={`commit${agent ? ' by-agent' : ''}`}>
            <div>
              <span className="commit-who">{actorLabel(c.actor)}</span>
              {agent && <span className="agent-tag"> {t('aiFor', { name: c.actor.name })}</span>}
            </div>
            {c.message && <div>{c.message}</div>}
            <div className="commit-meta">
              {t('revision', { n: c.seq })}, {t('changes', { n: c.changes.length })}, {new Date(c.created_at).toLocaleString()}
            </div>
            {overwrote && <div className="commit-warn">{t('overwroteNotice')}</div>}
          </div>
        )
      })}
    </aside>
  )
}
