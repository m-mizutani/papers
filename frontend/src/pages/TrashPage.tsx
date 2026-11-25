import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import type { TrashItem } from '../api/types'
import { tParts, useTranslation } from '../i18n'
import { useToast } from '../ui/Toast'
import { ActorMention } from '../components/UserMention'
import { errorMessage } from '../utils/errors'

const DAY = 24 * 60 * 60 * 1000

/** My papers in the trash; they are deleted for good after 30 days. */
export function TrashPage({ ws, onChanged, layoutTop }: { ws: string; onChanged: () => void; layoutTop: React.ReactNode }) {
  const { t } = useTranslation()
  const toast = useToast()
  const [papers, setPapers] = useState<TrashItem[]>()
  const load = () => void api.trash(ws).then(setPapers).catch(() => setPapers([]))
  useEffect(load, [ws])
  const restore = async (id: string) => {
    try {
      await api.restorePaper(ws, id)
      toast(t('restoredDone'))
      onChanged()
      load()
    } catch (e) {
      toast(errorMessage(e, t))
    }
  }
  return (
    <section className="main">
      {layoutTop}
      <div className="scroll"><div className="col wide">
        <h1 className="page-h" style={{ marginBottom: 'var(--sp-7)' }}>{t('trash')}</h1>
        {papers && papers.length === 0 && <div className="list"><div className="empty">{t('trashEmpty')}</div></div>}
        {papers && papers.length > 0 && (
          <div className="list">
            {papers.map((p) => (
              <div key={p.id} className="lr">
                <div className="lm">
                  <div className="lt"><Link to={`/ws/${ws}/p/${p.id}`} style={{ textDecoration: 'none' }}>{p.title || t('untitled')}</Link></div>
                  <div className="ls">
                    {t('daysLeft', { n: Math.max(1, Math.ceil((Date.parse(p.purge_at) - Date.now()) / DAY)) })}
                    {p.below > 0 && ` · ${t('trashBelow', { n: p.below })}`}
                    {' · '}{tParts('createdBy', { name: <ActorMention actor={p.created_by} /> })}
                  </div>
                </div>
                <button type="button" className="btn sm" onClick={() => void restore(p.id)}>{t('restoreFromTrash')}</button>
              </div>
            ))}
          </div>
        )}
      </div></div>
    </section>
  )
}
