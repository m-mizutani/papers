import { Link } from 'react-router'
import type { VersionSummary } from '../api/types'
import { ago, useTranslation } from '../i18n'
import { ActorMention } from './UserMention'

export interface ChangeCounts {
  added: number
  changed: number
  removed: number
  title: boolean
}

export function countText(c: ChangeCounts, t: ReturnType<typeof useTranslation>['t']): string {
  return [
    c.added ? `${c.added} ${t('added')}` : null,
    c.changed ? `${c.changed} ${t('changed')}` : null,
    c.removed ? `${c.removed} ${t('removed')}` : null,
    c.title ? t('titleChanged') : null,
  ].filter(Boolean).join(' · ')
}

export const totalOf = (c: ChangeCounts | null | undefined) => (c ? c.added + c.changed + c.removed + (c.title ? 1 : 0) : 0)

/** The paper's versions, newest first, with the current state on top (spec: R7). */
export function HistoryDrawer({ base, versions, viewing, counts, showChanges, onShowChanges, onClose }: {
  /** /ws/{ws}/p/{id} */
  base: string
  versions: VersionSummary[]
  /** The version being viewed; undefined for the current state. */
  viewing?: number
  /** Changes since the latest version (null when there is none yet). */
  counts: ChangeCounts | null
  showChanges: boolean
  onShowChanges: (on: boolean) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const changed = totalOf(counts) > 0
  return (
    <aside className="drawer" aria-label={t('history')}>
      <div className="dr-h">
        <span>{t('history')}</span>
        <button type="button" className="iconbtn" aria-label={t('close')} onClick={onClose}>✕</button>
      </div>
      <div className="dr-b">
        <Link className={`ver${viewing === undefined ? ' on' : ''}`} to={base}>
          <span className="vn"><span className="live-dot" /></span>
          <span className="vi">
            <span className="vt" style={{ display: 'block' }}>{t('current')}</span>
            <span className="vs" style={{ display: 'block' }}>{t('liveNow')}</span>
            {counts && changed && <span className="vnote" style={{ display: 'block' }}>{countText(counts, t)}</span>}
          </span>
        </Link>
        {changed && viewing === undefined && (
          <label className="chk" style={{ padding: 'var(--sp-1) var(--sp-5) var(--sp-4) var(--hist-indent)', fontSize: 'var(--text-sm)', color: 'var(--fg-2)' }}>
            <input type="checkbox" checked={showChanges} onChange={(e) => onShowChanges(e.target.checked)} />
            {t('showChanges')}
          </label>
        )}
        {versions.length === 0 && <p className="muted" style={{ padding: 'var(--sp-5)', margin: 0 }}>{t('noVersions')}</p>}
        {versions.map((v) => (
          <Link key={v.number} className={`ver${viewing === v.number ? ' on' : ''}`} to={`${base}/v/${v.number}`}>
            <span className="vn">v{v.number}</span>
            <span className="vi">
              <span className="vt" style={{ display: 'block' }}><ActorMention actor={v.created_by} /></span>
              <span className="vs" style={{ display: 'block' }}>{ago(v.created_at)}</span>
              {v.note && <span className="vnote" style={{ display: 'block' }}>{v.note}</span>}
            </span>
          </Link>
        ))}
      </div>
    </aside>
  )
}
