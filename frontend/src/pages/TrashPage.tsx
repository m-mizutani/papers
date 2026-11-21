import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import type { PaperRef } from '../api/types'
import { useTranslation } from '../i18n'

/** My papers in the trash; they are deleted for good after 30 days. */
export function TrashPage({ ws, onChanged }: { ws: string; onChanged: () => void }) {
  const { t } = useTranslation()
  const [papers, setPapers] = useState<PaperRef[]>()
  const load = () => void api.trash(ws).then(setPapers)
  useEffect(load, [ws])
  const restore = async (id: string) => {
    await api.restorePaper(ws, id)
    onChanged()
    load()
  }
  return (
    <article className="sheet">
      <h1 className="sheet-title-static">{t('trash')}</h1>
      <p className="muted">{t('trashLead')}</p>
      {papers && papers.length === 0 && <p className="muted">{t('trashEmpty')}</p>}
      <ul className="hits">
        {papers?.map((p) => (
          <li key={p.id}>
            <Link to={`/ws/${ws}/p/${p.id}`}>{p.title || t('untitled')}</Link>
            <button type="button" className="btn btn-quiet" onClick={() => void restore(p.id)}>{t('restoreFromTrash')}</button>
          </li>
        ))}
      </ul>
    </article>
  )
}
