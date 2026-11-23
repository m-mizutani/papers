import { useEffect, useRef } from 'react'
import { useTranslation } from '../i18n'
import { Popover, type Anchor } from '../ui/Popover'
import { isEnter } from '../utils/keyboard'
import type { TypeChoice } from './blockTypes'

/** The block kinds offered after "/" (or from "Insert below"), grouped. */
export function SlashMenu({ anchor, items, active, onPick, onHover, onClose, filter }: {
  anchor: Anchor
  items: TypeChoice[]
  active: number
  onPick: (c: TypeChoice) => void
  onHover: (i: number) => void
  onClose: () => void
  /** A filter field of its own (when not typing in a block). */
  filter?: { value: string; onChange: (v: string) => void; onKey: (key: string) => void }
}) {
  const { t } = useTranslation()
  const listRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    listRef.current?.querySelector('.mi.hi')?.scrollIntoView({ block: 'nearest' })
  }, [active])
  let group = ''
  return (
    <Popover anchor={anchor} onClose={onClose} className="slashpop" label={t('turnInto')}>
      {filter && (
        <input className="inp" style={{ height: 32, margin: '2px 0 4px' }} autoFocus value={filter.value} placeholder={t('filterTypes')} aria-label={t('filterTypes')}
          onChange={(e) => filter.onChange(e.target.value)}
          onKeyDown={(e) => {
            if (['ArrowUp', 'ArrowDown', 'Escape'].includes(e.key) || isEnter(e)) {
              e.preventDefault()
              filter.onKey(e.key)
            }
          }} />
      )}
      <div ref={listRef} role="listbox" onMouseDown={(e) => e.preventDefault()}>
        {items.length === 0 && <div className="mh">{t('noMatch')}</div>}
        {items.map((c, i) => {
          const head = c.group !== group
          group = c.group
          return (
            <div key={c.key}>
              {head && <div className="mh">{c.group === 'basic' ? t('menuBasic') : t('menuMedia')}</div>}
              <button type="button" role="option" aria-selected={i === active} className={`mi${i === active ? ' hi' : ''}`} onMouseMove={() => onHover(i)} onClick={() => onPick(c)}>
                <span className="mic boxed" aria-hidden>{c.icon}</span>
                <span className="mt">{t(c.label)}</span>
              </button>
            </div>
          )
        })}
      </div>
    </Popover>
  )
}
