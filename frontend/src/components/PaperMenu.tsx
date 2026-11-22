import { useNavigate } from 'react-router'
import { ApiError, api } from '../api/client'
import type { Paper } from '../api/types'
import { useTranslation } from '../i18n'
import { errorMessage } from '../utils/errors'

/** What can be done with the paper itself: pin, move, who can use it, archive, delete. */
export function PaperMenu({ ws, paper, isHome, owner, onChanged, onNotice }: {
  ws: string
  paper: Paper
  isHome: boolean
  owner: boolean
  onChanged: () => void
  onNotice: (msg: string) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  // Runs a change. Work on everything under the paper goes on in the
  // background; its results arrive through the change feed.
  const attempt = async (fn: () => Promise<unknown>, done?: string, after?: () => void) => {
    try {
      await fn()
      if (done) onNotice(done)
      after?.()
      onChanged()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'ambiguous') {
        onNotice(t('ambiguousPath', { candidates: e.candidates.map((c) => `${c.title} (${c.id})`).join(', ') }))
        return
      }
      onNotice(errorMessage(e, t))
    }
  }

  const move = () => {
    const parent = window.prompt(t('movePrompt'), '')
    if (parent === null) return
    void attempt(() => api.move(ws, paper.id, { parent }), t('moved'))
  }

  const moveAcross = () => {
    const target = window.prompt(t('moveWorkspacePrompt'), '')
    if (!target) return
    void attempt(() => api.move(ws, paper.id, { parent: '', workspace: target }), t('moveStarted'))
  }

  const visibility = () => {
    if (paper.visibility === 'public') {
      if (!window.confirm(t('confirmPrivate'))) return
      void attempt(() => api.setVisibility(ws, paper.id, 'private'), t('nowPrivate'))
      return
    }
    const parent = window.prompt(t('publicParentPrompt'), '')
    if (parent === null) return
    void attempt(() => api.setVisibility(ws, paper.id, 'public', parent), t('nowPublic'))
  }

  const archive = () => {
    const archived = !paper.archived_at
    const subtree = archived ? window.confirm(t('archiveSubtree')) : false
    void attempt(() => api.archive(ws, paper.id, archived, subtree), archived ? t('archived') : t('unarchived'))
  }

  const trash = () => {
    if (!window.confirm(t('confirmDeletePaper', { title: paper.title || t('untitled') }))) return
    void attempt(() => api.trashPaper(ws, paper.id), t('trashStarted'), () => navigate(`/ws/${ws}`))
  }

  return (
    <details className="menu">
      <summary className="btn btn-quiet" aria-label={t('more')}>⋯</summary>
      <div className="menu-items" onClick={(e) => (e.currentTarget.parentElement as HTMLDetailsElement).removeAttribute('open')}>
        <button type="button" onClick={() => void attempt(() => api.pin(ws, paper.id), t('pinned'))}>{t('pin')}</button>
        {!isHome && paper.visibility === 'public' && <button type="button" onClick={move}>{t('move')}</button>}
        {!isHome && paper.visibility === 'public' && <button type="button" onClick={moveAcross}>{t('moveWorkspace')}</button>}
        {!isHome && owner && <button type="button" onClick={visibility}>{paper.visibility === 'public' ? t('makePrivate') : t('makePublic')}</button>}
        {!isHome && <button type="button" onClick={archive}>{paper.archived_at ? t('unarchive') : t('archive')}</button>}
        {!isHome && owner && <button type="button" className="danger" onClick={trash}>{t('deletePaper')}</button>}
      </div>
    </details>
  )
}
