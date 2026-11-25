import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'

// A box floating next to an anchor (a menu, the formatting toolbar, the
// slash menu). It closes on a click outside and on Escape, and keeps inside
// the window.

export type Anchor = DOMRect | HTMLElement

function rectOf(a: Anchor): DOMRect {
  return a instanceof HTMLElement ? a.getBoundingClientRect() : a
}

export function Popover({ anchor, onClose, className = '', children, placement = 'below', label, ignoreWithin }: {
  anchor: Anchor
  onClose: () => void
  className?: string
  children: ReactNode
  /** below: under the anchor; above: over it (the formatting toolbar). */
  placement?: 'below' | 'above'
  label?: string
  /** A press inside this element (an editor the popover belongs to) does not close it. */
  ignoreWithin?: Node | null
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number }>({ left: -9999, top: -9999 })
  const close = useRef(onClose)
  close.current = onClose

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const r = rectOf(anchor)
    const w = el.offsetWidth
    const h = el.offsetHeight
    let left = placement === 'above' ? r.left + r.width / 2 - w / 2 : r.left
    let top = placement === 'above' ? r.top - h - 8 : r.bottom + 6
    left = Math.max(8, Math.min(window.innerWidth - w - 8, left))
    if (placement === 'below' && top + h > window.innerHeight - 8) top = Math.max(8, r.top - h - 6)
    if (placement === 'above' && top < 8) top = r.bottom + 8
    setPos({ left, top })
  }, [anchor, placement, children])

  useEffect(() => {
    const down = (e: MouseEvent) => {
      if (ref.current?.contains(e.target as Node)) return
      if (anchor instanceof HTMLElement && anchor.contains(e.target as Node)) return
      if (ignoreWithin?.contains(e.target as Node)) return
      close.current()
    }
    const key = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        close.current()
      }
    }
    document.addEventListener('mousedown', down)
    document.addEventListener('keydown', key, true)
    return () => {
      document.removeEventListener('mousedown', down)
      document.removeEventListener('keydown', key, true)
    }
  }, [anchor, ignoreWithin])

  return createPortal(
    <div ref={ref} className={`pop ${className}`} style={{ left: pos.left, top: pos.top }} role="dialog" aria-label={label}>
      {children}
    </div>,
    document.body,
  )
}
