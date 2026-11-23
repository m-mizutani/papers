import { useEffect, useMemo, useState } from 'react'
import { api } from '../api/client'
import type { Paper, PaperListItem, Workspace } from '../api/types'
import { useTranslation } from '../i18n'
import { Modal } from '../ui/Modal'

/**
 * Picks a new parent for a paper (spec: R12). Only filed papers can be
 * parents: unfiled and private papers have no children. Another workspace
 * moves the paper with everything under it as a background job.
 */
export function MoveDialog({ paper, ws, workspaces, exclude, onMove, onClose }: {
  paper: Paper
  ws: string
  workspaces: Workspace[]
  /** The paper itself and everything under it. */
  exclude: Set<string>
  onMove: (parent: string, workspace: string) => Promise<void>
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [target, setTarget] = useState(ws)
  const [q, setQ] = useState('')
  const [rows, setRows] = useState<PaperListItem[]>([])
  const [home, setHome] = useState<string>('')
  const [sel, setSel] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    const w = workspaces.find((x) => x.id === target)
    setHome(w?.home_paper_id ?? '')
    setSel(null)
    void api.listPapers(target, { place: 'filed', creator: 'all', limit: 100 }).then((r) => { if (live) setRows(r.papers) }).catch(() => { if (live) setRows([]) })
    return () => { live = false }
  }, [target, workspaces])

  const options = useMemo(() => {
    const w = workspaces.find((x) => x.id === target)
    const list = rows.filter((r) => r.visibility === 'public' && !(target === ws && exclude.has(r.id)) && r.id !== home)
      .filter((r) => !q || (r.title || t('untitled')).toLowerCase().includes(q.toLowerCase()))
    const homeRow = w && (!q || w.name.toLowerCase().includes(q.toLowerCase())) ? [{ id: home, title: `⌂ ${w.name}`, sub: '' }] : []
    return [...homeRow, ...list.map((r) => ({ id: r.id, title: r.title || t('untitled'), sub: r.parent?.title ?? '' }))]
  }, [rows, q, target, ws, exclude, home, workspaces, t])

  return (
    <Modal wide title={t('moveTitle', { title: paper.title || t('untitled') })} onClose={onClose}
      actions={[
        { label: t('cancel') },
        { label: t('move'), kind: 'primary', disabled: !sel, onClick: async () => { if (sel) await onMove(sel, target) } },
      ]}>
      {workspaces.length > 1 && (
        <div className="field">
          <label htmlFor="mvws">{t('workspace')}</label>
          <select id="mvws" className="inp" value={target} onChange={(e) => setTarget(e.target.value)}>
            {workspaces.map((w) => <option key={w.id} value={w.id}>{`${w.emoji ?? ''} ${w.name}`.trim()}</option>)}
          </select>
        </div>
      )}
      <div className="field">
        <label htmlFor="mvq">{t('moveTo')}</label>
        <input id="mvq" className="inp" value={q} placeholder={t('filter')} onChange={(e) => setQ(e.target.value)} />
        <div className="picker" role="listbox" aria-label={t('moveTo')}>
          {options.map((o) => (
            <button key={o.id} type="button" role="option" aria-selected={sel === o.id} className={`prow${sel === o.id ? ' sel' : ''}`} onClick={() => setSel(o.id)}>
              <span>{o.title}</span>
              {o.sub && <span className="pp">{o.sub}</span>}
            </button>
          ))}
        </div>
      </div>
      {target !== ws && <p className="banner warn" style={{ margin: '8px 0 0' }}>{t('crossWorkspace')}</p>}
    </Modal>
  )
}
