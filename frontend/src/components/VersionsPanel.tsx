import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import { actorLabel } from '../api/paperState'
import type { Diff, VersionSummary } from '../api/types'
import { useTranslation } from '../i18n'
import { plainText } from '../markup/markup'

function excerpt(text: string | undefined): string {
  const s = plainText(text ?? '').replace(/\s+/g, ' ').trim()
  return s.length > 80 ? `${s.slice(0, 80)}…` : s
}

/** The differences between two states, block by block. */
export function DiffView({ diff }: { diff: Diff }) {
  const { t } = useTranslation()
  const entries = diff.entries ?? []
  return (
    <div className="diff">
      {diff.title_before !== diff.title_after && (
        <div className="diff-entry changed"><span className="diff-kind">{t('diffTitle')}</span> {diff.title_before || t('untitled')} → {diff.title_after || t('untitled')}</div>
      )}
      {entries.length === 0 && diff.title_before === diff.title_after && <p className="muted">{t('diffNone')}</p>}
      {entries.map((e) => (
        <div key={e.block_id} className={`diff-entry ${e.kind}`}>
          <span className="diff-kind">{t(`diff_${e.kind}`)}</span>
          {e.kind === 'changed' ? (
            <>
              <del>{excerpt(e.before?.content) || e.before?.type}</del>
              <ins>{excerpt(e.after?.content) || e.after?.type}</ins>
            </>
          ) : (
            <span>{excerpt((e.after ?? e.before)?.content) || (e.after ?? e.before)?.type}</span>
          )}
        </div>
      ))}
    </div>
  )
}

/**
 * The paper's saved versions, newest first: who saved, when and why. A
 * version opens read-only; it can be compared with the current state and
 * the current state can be set back to it.
 */
export function VersionsPanel({ ws, paper, latest, onClose, onRestore }: {
  ws: string
  paper: string
  /** The latest version number, to reload when it changes. */
  latest: number
  onClose: () => void
  onRestore?: (n: number) => void
}) {
  const { t } = useTranslation()
  const [versions, setVersions] = useState<VersionSummary[]>()
  const [diff, setDiff] = useState<{ n: number; diff: Diff }>()

  useEffect(() => {
    void api.versions(ws, paper).then(setVersions)
  }, [ws, paper, latest])

  const compare = async (n: number) => {
    if (diff?.n === n) {
      setDiff(undefined)
      return
    }
    setDiff({ n, diff: await api.diff(ws, paper, String(n), 'current') })
  }

  return (
    <aside className="panel" aria-label={t('versions')}>
      <div className="panel-head">
        <h2>{t('versions')}</h2>
        <button type="button" className="btn btn-quiet" onClick={onClose}>{t('close')}</button>
      </div>
      <p className="muted">{t('versionsLead')}</p>
      {versions && versions.length === 0 && <p className="muted">{t('noVersions')}</p>}
      <ol className="versions">
        {versions?.map((v) => (
          <li key={v.number}>
            <div className="version-head">
              <Link to={`/ws/${ws}/p/${paper}/v/${v.number}`}><strong>v{v.number}</strong></Link>
              <span className="muted">{actorLabel(v.created_by, t('viaApi'))} · {new Date(v.created_at).toLocaleString()}</span>
            </div>
            {v.note && <p className="version-note">{v.note}</p>}
            <div className="version-actions">
              <button type="button" className="btn btn-quiet" onClick={() => void compare(v.number)}>{diff?.n === v.number ? t('hideDiff') : t('compareWithCurrent')}</button>
              {onRestore && <button type="button" className="btn btn-quiet" onClick={() => onRestore(v.number)}>{t('restoreThisVersion')}</button>}
            </div>
            {diff?.n === v.number && <DiffView diff={diff.diff} />}
          </li>
        ))}
      </ol>
    </aside>
  )
}
