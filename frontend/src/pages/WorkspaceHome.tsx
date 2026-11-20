import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { api } from '../api/client'
import { actorLabel } from '../api/paperState'
import type { Paper, Workspace } from '../api/types'
import { MembersPanel } from '../components/MembersPanel'
import { useTranslation } from '../i18n'

type Hit = { paper: Paper; snippet: string }

export function WorkspaceHome({ workspace, papers, onChanged }: { workspace: Workspace; papers: Paper[]; onChanged: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [query, setQuery] = useState('')
  const [hits, setHits] = useState<Hit[] | null>(null)
  const [members, setMembers] = useState(false)
  const canCreate = workspace.role === 'editor' || workspace.role === 'owner'

  useEffect(() => {
    const q = query.trim()
    if (!q) {
      setHits(null)
      return
    }
    const id = window.setTimeout(() => {
      void api.search(workspace.id, q).then(setHits)
    }, 250)
    return () => window.clearTimeout(id)
  }, [query, workspace.id])

  const create = async () => {
    const v = await api.createPaper(workspace.id, '')
    onChanged()
    navigate(`/w/${workspace.id}/p/${v.paper.id}`)
  }

  const rows: Hit[] = hits ?? papers.map((p) => ({ paper: p, snippet: '' }))

  return (
    <div className="home">
      <div className="home-head">
        <h1>{workspace.emoji ? `${workspace.emoji} ` : ''}{workspace.name}</h1>
        {workspace.role === 'owner' && <button type="button" className="btn btn-quiet" onClick={() => setMembers(true)}>{t('manageMembers')}</button>}
        {canCreate && <button type="button" className="btn btn-primary" onClick={() => void create()}>{t('newPaper')}</button>}
      </div>
      <input className="field search" type="search" value={query} placeholder={t('searchPlaceholder')} aria-label={t('searchPlaceholder')} onChange={(e) => setQuery(e.target.value)} />
      {rows.length === 0 && <p className="help">{hits ? t('noResults', { query }) : t('noPapers')}</p>}
      {rows.map(({ paper, snippet }) => (
        <Link key={paper.id} to={`/w/${workspace.id}/p/${paper.id}`} className={`paper-row${paper.updated_by.kind === 'agent' ? ' by-agent' : ''}`}>
          <span className="paper-row-title">{paper.title || t('untitled')}</span>
          <span className="paper-row-meta">
            {actorLabel(paper.updated_by)}
            <br />
            {t('updated', { when: new Date(paper.updated_at).toLocaleString() })}
          </span>
          {snippet && <span className="paper-row-snippet">{snippet}</span>}
        </Link>
      ))}
      {members && <MembersPanel workspace={workspace} onClose={() => setMembers(false)} onSaved={onChanged} />}
    </div>
  )
}
