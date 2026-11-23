import { useEffect, useRef, type KeyboardEvent, type ReactNode } from 'react'
import { Popover, type Anchor } from './Popover'

export type MenuItem =
  | '-'
  | { head: string }
  | { note: string }
  | { label: string; icon?: ReactNode; kbd?: string; desc?: string; danger?: boolean; disabled?: boolean; onSelect: () => void }

/** A list of actions next to an anchor. Arrow keys move, Escape closes. */
export function Menu({ anchor, items, onClose, label }: { anchor: Anchor; items: MenuItem[]; onClose: () => void; label?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    ref.current?.querySelector<HTMLButtonElement>('.mi:not([disabled])')?.focus()
  }, [])
  const onKey = (e: KeyboardEvent) => {
    const all = [...(ref.current?.querySelectorAll<HTMLButtonElement>('.mi:not([disabled])') ?? [])]
    const i = all.indexOf(document.activeElement as HTMLButtonElement)
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      all[(i + 1) % all.length]?.focus()
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      all[(i - 1 + all.length) % all.length]?.focus()
    }
  }
  return (
    <Popover anchor={anchor} onClose={onClose} label={label}>
      <div ref={ref} role="menu" onKeyDown={onKey}>
        {items.map((it, i) => {
          if (it === '-') return <div key={i} className="msep" />
          if ('head' in it) return <div key={i} className="mh">{it.head}</div>
          if ('note' in it) return <div key={i} className="mnote">{it.note}</div>
          return (
            <button key={i} type="button" role="menuitem" className={`mi${it.danger ? ' danger' : ''}`} disabled={it.disabled}
              onClick={() => { onClose(); it.onSelect() }}>
              {it.icon !== undefined && <span className="mic" aria-hidden>{it.icon}</span>}
              <span className="mt"><span>{it.label}</span>{it.desc && <span className="md" style={{ display: 'block' }}>{it.desc}</span>}</span>
              {it.kbd && <span className="mk2">{it.kbd}</span>}
            </button>
          )
        })}
      </div>
    </Popover>
  )
}
