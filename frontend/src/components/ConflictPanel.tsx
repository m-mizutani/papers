import type { Actor, Block } from '../api/types'
import { useTranslation } from '../i18n'
import { actorName } from '../utils/actors'
import { renderMarkup } from '../utils/inline'

/** Someone changed or deleted the block being edited. */
export interface ConflictState {
  blockId: string
  /** Their block now; null when they deleted it. */
  theirs: Block | null
  /** Who deleted it, when known. */
  by?: Actor
  /** My text at the moment of the conflict. */
  mine: string
  /** The deleted block, shown in its old place until the choice is made. */
  ghost?: Block
}

export type ConflictChoice = 'keepBoth' | 'theirs' | 'overwrite' | 'addAsNew' | 'discard'

/** Theirs next to mine, under the block, with what to keep (spec: R8). */
export function ConflictPanel({ conflict, onChoose }: { conflict: ConflictState; onChoose: (c: ConflictChoice) => void }) {
  const { t } = useTranslation()
  // Keep the caret in the editor: a blur would try to save again.
  const keep = (e: { preventDefault: () => void; stopPropagation: () => void }) => {
    e.preventDefault()
    e.stopPropagation()
  }
  const text = (s: string, code: boolean) => (code ? s : renderMarkup(s))
  const code = conflict.theirs?.type === 'code'
  if (!conflict.theirs) {
    return (
      <div className="conf" role="alert" onMouseDown={keep} onClick={(e) => e.stopPropagation()}>
        <h4>⚠ {t('deletedTitle', { name: conflict.by ? actorName(conflict.by) : t('someone') })}</h4>
        <p>{t('deletedBody')}</p>
        <div className="side" style={{ marginBottom: 'var(--sp-5)' }}><div className="tx">{text(conflict.mine, false)}</div></div>
        <div className="acts">
          <button type="button" className="btn primary sm" onClick={() => onChoose('addAsNew')}>{t('addAsNew')}</button>
          <button type="button" className="btn sm" onClick={() => onChoose('discard')}>{t('discard')}</button>
        </div>
      </div>
    )
  }
  return (
    <div className="conf" role="alert" onMouseDown={keep} onClick={(e) => e.stopPropagation()}>
      <h4>⚠ {t('conflictTitle', { name: actorName(conflict.theirs.updated_by) })}</h4>
      <p>{t('conflictBody')}</p>
      <div className="two">
        <div className="side"><div className="who">{t('conflictTheirs')}</div><div className="tx">{text(conflict.theirs.content, code)}</div></div>
        <div className="side"><div className="who">{t('conflictMine')}</div><div className="tx">{text(conflict.mine, code)}</div></div>
      </div>
      <div className="acts">
        <button type="button" className="btn primary sm" onClick={() => onChoose('keepBoth')}>{t('keepBoth')}</button>
        <button type="button" className="btn sm" onClick={() => onChoose('theirs')}>{t('useTheirs')}</button>
        <button type="button" className="btn sm danger" onClick={() => onChoose('overwrite')}>{t('overwrite')}</button>
      </div>
    </div>
  )
}
