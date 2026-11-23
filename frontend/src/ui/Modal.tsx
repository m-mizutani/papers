import { useEffect, useRef, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { isEnter, modKey } from '../utils/keyboard'

export interface ModalAction {
  label: string
  kind?: 'primary' | 'danger' | 'danger solid' | 'ghost'
  disabled?: boolean
  /** Return false to keep the dialog open. */
  onClick?: () => void | boolean | Promise<void | boolean>
}

/** A dialog over the page. Escape and a click outside close it; Mod+Enter runs the primary action. */
export function Modal({ title, children, actions, onClose, wide, initialFocus }: {
  title: string
  children: ReactNode
  actions: ModalAction[]
  onClose: () => void
  wide?: boolean
  /** A selector inside the dialog to focus first. */
  initialFocus?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const close = useRef(onClose)
  close.current = onClose

  useEffect(() => {
    const el = ref.current
    const first = (initialFocus ? el?.querySelector<HTMLElement>(initialFocus) : null)
      ?? el?.querySelector<HTMLElement>('input, textarea')
      ?? el?.querySelector<HTMLElement>('.mf .btn.primary, .mf .btn.danger.solid, .mf .btn')
    first?.focus()
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        close.current()
      }
    }
    document.addEventListener('keydown', key, true)
    return () => document.removeEventListener('keydown', key, true)
  }, [initialFocus])

  const run = async (a: ModalAction) => {
    const r = await a.onClick?.()
    if (r !== false) close.current()
  }

  return createPortal(
    <div className="scrim" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div ref={ref} className={`modal${wide ? ' wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}
        onKeyDown={(e) => {
          if (isEnter(e) && modKey(e)) {
            const p = actions.find((a) => a.kind === 'primary' && !a.disabled)
            if (p) void run(p)
          }
        }}>
        <h3>{title}</h3>
        <div className="mb">{children}</div>
        <div className="mf">
          {actions.map((a) => (
            <button key={a.label} type="button" className={`btn ${a.kind ?? ''}`} disabled={a.disabled} onClick={() => void run(a)}>{a.label}</button>
          ))}
        </div>
      </div>
    </div>,
    document.body,
  )
}
