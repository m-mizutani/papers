import { useEffect, useState } from 'react'
import { api } from '../api/client'
import { actorLabel, type PaperState } from '../api/paperState'
import type { Block, Commit } from '../api/types'
import { useTranslation } from '../i18n'

function excerpt(b: Block | undefined): string {
  if (!b) return ''
  const text = b.content.replace(/\s+/g, ' ').trim()
  return text.length > 60 ? `${text.slice(0, 60)}…` : text
}

/**
 * One block's version history, newest first. Any version other than the
 * current one can be restored (as a new version; history is kept).
 */
function BlockVersions({ ws, paper, block, current, onRestore }: {
  ws: string
  paper: string
  block: string
  current: Block | undefined
  onRestore?: (block: string, version: number) => void
}) {
  const { t } = useTranslation()
  const [versions, setVersions] = useState<Block[]>()
  useEffect(() => {
    void api.blockVersions(ws, paper, block).then(setVersions)
  }, [ws, paper, block, current?.version])
  if (!versions) return null
  return (
    <ol className="versions">
      {versions.map((v) => (
        <li key={v.version} className={v.updated_by.kind === 'agent' ? 'by-agent' : undefined}>
          <span className="commit-meta">v{v.version}, {actorLabel(v.updated_by)}, {new Date(v.updated_at).toLocaleString()}</span>
          <div className="version-text">{v.content || '—'}</div>
          {onRestore && v.version !== current?.version && v.type !== 'table' && !v.parent_id === !current?.parent_id && (
            <button type="button" className="btn btn-quiet" onClick={() => onRestore(block, v.version)}>{t('restoreVersion')}</button>
          )}
        </li>
      ))}
    </ol>
  )
}

export function HistoryPanel({ ws, paper, state, onClose, onRestore }: {
  ws: string
  paper: string
  state: PaperState
  onClose: () => void
  /** Restores a block version; omitted for readers. */
  onRestore?: (block: string, version: number) => void
}) {
  const { t } = useTranslation()
  const [commits, setCommits] = useState<Commit[]>([])
  const [more, setMore] = useState(false)
  const [error, setError] = useState<string>()
  const [open, setOpen] = useState<string>()

  const loadPage = (before = '') =>
    api.commits(ws, paper, before)
      .then((cs) => {
        setCommits((prev) => (before ? [...prev, ...cs] : cs))
        setMore(cs.length === 50)
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))

  useEffect(() => {
    void loadPage()
    // Reload when the paper changes while the panel is open.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ws, paper, state.blocks])

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
        const overwrites = c.changes.filter((ch) => (ch.overwrote_version ?? 0) > 0)
        return (
          <div key={c.id} className={`commit${agent ? ' by-agent' : ''}`}>
            <div>
              <span className="commit-who">{actorLabel(c.actor)}</span>
              {agent && <span className="agent-tag"> {t('aiFor', { name: c.actor.name })}</span>}
            </div>
            {c.message && <div>{c.message}</div>}
            <div className="commit-meta">
              {new Date(c.created_at).toLocaleString()}, {t('changes', { n: c.changes.length })}
            </div>
            <ul className="changes">
              {c.changes.slice(0, 8).map((ch, i) => {
                if (ch.kind === 'set_title') return <li key={i}>{t('changeTitle', { title: ch.title_after ?? '' })}</li>
                const key = `${c.id}:${ch.block_id}`
                const label = excerpt(state.blocks.get(ch.block_id ?? '')) || t('deletedBlock')
                return (
                  <li key={i}>
                    <button type="button" className="change-link" onClick={() => setOpen(open === key ? undefined : key)}>
                      {t(`change_${ch.kind}` as 'change_insert')}: {label}
                    </button>
                    {open === key && ch.block_id && (
                      <BlockVersions ws={ws} paper={paper} block={ch.block_id} current={state.blocks.get(ch.block_id)} onRestore={onRestore} />
                    )}
                  </li>
                )
              })}
            </ul>
            {overwrites.map((ch, i) => (
              <div key={i} className="commit-warn">
                {t('overwroteBy', { version: ch.overwrote_version ?? 0, name: ch.overwrote_by ? actorLabel(ch.overwrote_by) : '?' })}
              </div>
            ))}
          </div>
        )
      })}
      {more && <button type="button" className="btn" onClick={() => void loadPage(commits[commits.length - 1]?.id ?? '')}>{t('olderCommits')}</button>}
    </aside>
  )
}
