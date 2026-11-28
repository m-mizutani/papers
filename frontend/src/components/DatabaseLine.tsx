import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api/client'
import type { DatabaseLink, Paper } from '../api/types'
import { useTranslation } from '../i18n'
import { DEFAULT_ICONS, paperIcon } from '../utils/icon'

// The line that tells people which databases an HTML document is connected
// to, and whether it reads or writes each one: under an HTML paper's title,
// or under an html block. The document reads and writes as the viewer, so
// this is shown wherever such a document is.

type Found = Record<string, Paper | null>

export function DatabaseLine({ ws, links, of, connected, version = false }: {
  ws: string
  links: DatabaseLink[]
  /** Whether the document is a whole HTML paper or one block. */
  of: 'page' | 'block'
  /** Whether the document is connected: not while an earlier version is shown, or the paper cannot be changed (in the trash, being moved). */
  connected: boolean
  /** Whether an earlier version is shown. */
  version?: boolean
}) {
  const { t } = useTranslation()
  const [found, setFound] = useState<Found>({})
  const ids = links.map((l) => l.id).join(',')

  useEffect(() => {
    let alive = true
    const want = ids ? ids.split(',') : []
    void Promise.all(want.map((id) => api.getPaper(ws, id).then((v) => (v.paper.kind === 'database' ? v.paper : null), () => null))).then((papers) => {
      if (!alive) return
      const next: Found = {}
      want.forEach((id, i) => { next[id] = papers[i] ?? null })
      setFound(next)
    })
    return () => { alive = false }
  }, [ws, ids])

  if (!connected) {
    const why = version
      ? (of === 'page' ? 'dbNotConnectedVersion' : 'dbNotConnectedVersionBlock')
      : (of === 'page' ? 'dbNotConnectedPage' : 'dbNotConnectedBlock')
    return <div className="dbline ver" role="note">{t(why)}</div>
  }
  return (
    <div className="dbline" role="note">
      <span>{t(of === 'page' ? 'dbConnectedPage' : 'dbConnectedBlock')}</span>
      {links.map((l) => {
        const p = found[l.id]
        const access = <span className="dbacc">{t(l.write ? 'dbReadWrite' : 'dbReadOnly')}</span>
        if (p === null) {
          return <span key={l.id} className="dbchip gone">{DEFAULT_ICONS.database} {t('dbMissing')} {access}</span>
        }
        return (
          <Link key={l.id} className="dbchip" to={`/ws/${ws}/p/${l.id}`}>
            {p ? paperIcon(p) : DEFAULT_ICONS.database} {p ? p.title || t('untitled') : '…'} {access}
          </Link>
        )
      })}
    </div>
  )
}
