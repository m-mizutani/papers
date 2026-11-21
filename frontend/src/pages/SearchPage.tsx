import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import type { SearchHit } from '../api/types'
import { useTranslation } from '../i18n'

export function SearchPage({ ws, query }: { ws: string; query: string }) {
  const { t } = useTranslation()
  const [hits, setHits] = useState<SearchHit[]>()
  const [error, setError] = useState<string>()
  useEffect(() => {
    setHits(undefined)
    setError(undefined)
    void api.search(ws, query).then(setHits).catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }, [ws, query])
  return (
    <article className="sheet">
      <h1 className="sheet-title-static">{t('searchResults', { query })}</h1>
      {error && <p className="notice">{error}</p>}
      {hits && hits.length === 0 && <p className="muted">{t('noResults', { query })}</p>}
      <ul className="hits">
        {hits?.map((h) => (
          <li key={`${h.paper.id}:${h.block_id ?? ''}`}>
            <Link to={`/ws/${ws}/p/${h.paper.id}`}><strong>{h.paper.title || t('untitled')}</strong></Link>
            {h.block_id && <p className="hit-text">{h.text}</p>}
          </li>
        ))}
      </ul>
    </article>
  )
}
