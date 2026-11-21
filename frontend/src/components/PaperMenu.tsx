import { useNavigate } from 'react-router'
import { ApiError, api } from '../api/client'
import type { BatchResult, Paper } from '../api/types'
import { useTranslation } from '../i18n'
import { errorMessage, followJobs } from '../utils/jobs'

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

  // Runs a change; work on everything under the paper may go on as a
  // background job, which is followed until it finishes.
  const attempt = async (fn: () => Promise<BatchResult | void>, done?: string, after?: (res: BatchResult) => void) => {
    try {
      const res = await fn()
      if (res) {
        const jobs = await followJobs(ws, res, onNotice, t)
        after?.({ ...res, jobs, forward: res.forward ?? jobs.find((j) => j.result?.forward)?.result?.forward })
      }
      if (done) onNotice(done)
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
    void attempt(() => api.move(ws, paper.id, { parent: '', workspace: target }), t('moveDone'), (res) => {
      if (res.forward) navigate(`/ws/${res.forward.to_workspace_id}/p/${res.forward.to_paper_id}`)
    })
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
    void attempt(() => api.trashPaper(ws, paper.id), t('trashDone'), () => navigate(`/ws/${ws}`))
  }

  return (
    <details className="menu">
      <summary className="btn btn-quiet" aria-label={t('more')}>⋯</summary>
      <div className="menu-items" onClick={(e) => (e.currentTarget.parentElement as HTMLDetailsElement).removeAttribute('open')}>
        <button type="button" onClick={() => void attempt(async () => { await api.pin(ws, paper.id) }, t('pinned'))}>{t('pin')}</button>
        {!isHome && paper.visibility === 'public' && <button type="button" onClick={move}>{t('move')}</button>}
        {!isHome && paper.visibility === 'public' && <button type="button" onClick={moveAcross}>{t('moveWorkspace')}</button>}
        {!isHome && owner && <button type="button" onClick={visibility}>{paper.visibility === 'public' ? t('makePrivate') : t('makePublic')}</button>}
        {!isHome && <button type="button" onClick={archive}>{paper.archived_at ? t('unarchive') : t('archive')}</button>}
        {!isHome && owner && <button type="button" className="danger" onClick={trash}>{t('deletePaper')}</button>}
      </div>
    </details>
  )
}
