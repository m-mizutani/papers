import { useEffect, useMemo, useState } from 'react'
import { api } from '../api/client'
import type { Paper, PaperListItem, Workspace } from '../api/types'
import { useTranslation } from '../i18n'
import { Modal } from '../ui/Modal'
import { DEFAULT_ICONS, paperIcon } from '../utils/icon'
import { NO_AUTOFILL } from '../utils/autofill'

/**
 * Picks a new parent for a paper (spec: R12). Only filed papers can be
 * parents: unfiled and private papers have no children. Another workspace
 * moves the paper with everything under it as a background job.
 */
export function MoveDialog({ paper, ws, workspaces, exclude, homeIcon, onMove, onClose }: {
  paper: Paper
  ws: string
  workspaces: Workspace[]
  /** The paper itself and everything under it. */
  exclude: Set<string>
  /** The icon set on this workspace's home, if any; other workspaces show the default. */
  homeIcon?: string
  onMove: (parent: string, workspace: string) => Promise<void>
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [target, setTarget] = useState(ws)
  const [q, setQ] = useState('')
  const [rows, setRows] = useState<PaperListItem[]>([])
  const [next, setNext] = useState('')
  const [found, setFound] = useState<{ id: string; title: string; icon: string }[]>([])
  const [home, setHome] = useState<string>('')
  const [sel, setSel] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    const w = workspaces.find((x) => x.id === target)
    setHome(w?.home_paper_id ?? '')
    setSel(null)
    setRows([])
    setNext('')
    void api.listPapers(target, { place: 'filed', creator: 'all', limit: 100 })
      .then((r) => { if (live) { setRows(r.papers); setNext(r.next_cursor) } })
      .catch(() => { if (live) setRows([]) })
    return () => { live = false }
  }, [target, workspaces])

  const more = () => {
    const cursor = next
    setNext('')
    void api.listPapers(target, { place: 'filed', creator: 'all', limit: 100, cursor })
      .then((r) => { setRows((prev) => [...prev, ...r.papers]); setNext(r.next_cursor) })
      .catch(() => setNext(cursor))
  }

  // Papers not loaded yet are found by searching their titles.
  useEffect(() => {
    const query = q.trim()
    if (!query) {
      setFound([])
      return
    }
    let live = true
    const timer = window.setTimeout(() => {
      void api.search(target, query).then((hits) => {
        if (!live) return
        const seen = new Set<string>()
        setFound(hits.map((h) => h.paper).filter((p) => p.visibility === 'public' && !!p.parent_id && !seen.has(p.id) && !!seen.add(p.id))
          .map((p) => ({ id: p.id, title: p.title, icon: paperIcon(p) })))
      }).catch(() => { if (live) setFound([]) })
    }, 150)
    return () => { live = false; window.clearTimeout(timer) }
  }, [q, target])

  const options = useMemo(() => {
    const w = workspaces.find((x) => x.id === target)
    const ok = (id: string) => !(target === ws && exclude.has(id)) && id !== home
    const match = (title: string) => !q || (title || t('untitled')).toLowerCase().includes(q.toLowerCase())
    const list = rows.filter((r) => r.visibility === 'public' && ok(r.id) && match(r.title))
    const listed = new Set(list.map((r) => r.id))
    const extra = found.filter((r) => ok(r.id) && !listed.has(r.id) && match(r.title))
    const homeRow = w && home && (!q || w.name.toLowerCase().includes(q.toLowerCase()))
      ? [{ id: home, icon: (target === ws && homeIcon) || DEFAULT_ICONS.home, title: w.name, sub: '' }]
      : []
    return [
      ...homeRow,
      ...list.map((r) => ({ id: r.id, icon: paperIcon(r), title: r.title || t('untitled'), sub: r.parent?.title ?? '' })),
      ...extra.map((r) => ({ id: r.id, icon: r.icon, title: r.title || t('untitled'), sub: '' })),
    ]
  }, [rows, found, q, target, ws, exclude, home, homeIcon, workspaces, t])

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
        <input {...NO_AUTOFILL} id="mvq" className="inp" value={q} placeholder={t('filter')} onChange={(e) => setQ(e.target.value)} />
        <div className="picker" role="listbox" aria-label={t('moveTo')}>
          {options.map((o) => (
            <button key={o.id} type="button" role="option" aria-selected={sel === o.id} className={`prow${sel === o.id ? ' sel' : ''}`} onClick={() => setSel(o.id)}>
              <span><span className="pic-i" aria-hidden>{o.icon}</span> {o.title}</span>
              {o.sub && <span className="pp">{o.sub}</span>}
            </button>
          ))}
          {next && <button type="button" className="prow more" onClick={more}>{t('loadMore')}</button>}
        </div>
      </div>
      {target !== ws && <p className="banner warn mv-warn">{t('crossWorkspace')}</p>}
    </Modal>
  )
}
