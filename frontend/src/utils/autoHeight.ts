import { useEffect, useLayoutEffect, useRef, type RefObject } from 'react'

/** Sets a textarea's height to the height of its content, wrapped lines included. */
export function fitHeight(el: HTMLTextAreaElement): void {
  el.style.height = '0px'
  el.style.height = `${el.scrollHeight}px`
}

/**
 * Keeps a textarea as tall as its content: when the value changes (typed,
 * loaded or changed by another writer), when the textarea's width changes
 * (window, sidebar, font size) and when web fonts finish loading. Its CSS
 * hides overflow, so a stale height would hide the wrapped lines.
 */
export function useAutoHeight(ref: RefObject<HTMLTextAreaElement | null>, value: string): void {
  // Before paint, so a long value never shows one line first.
  useLayoutEffect(() => {
    if (ref.current) fitHeight(ref.current)
  }, [ref, value])

  // The textarea can mount after the first render (a page shown once loaded),
  // so the observer follows whichever element the ref holds.
  const observed = useRef<{ el: HTMLTextAreaElement; ro: ResizeObserver } | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (observed.current?.el === el) return
    observed.current?.ro.disconnect()
    observed.current = null
    if (!el) return
    // A newly mounted textarea may already hold its value.
    fitHeight(el)
    if (typeof ResizeObserver === 'undefined') return
    let width = el.getBoundingClientRect().width
    // Only a width change refits: the refit itself changes the height.
    const ro = new ResizeObserver(() => {
      const w = el.getBoundingClientRect().width
      if (w === width) return
      width = w
      fitHeight(el)
    })
    ro.observe(el)
    observed.current = { el, ro }
  })
  useEffect(() => () => {
    observed.current?.ro.disconnect()
    observed.current = null
  }, [])

  useEffect(() => {
    const fonts = typeof document !== 'undefined' ? document.fonts : undefined
    if (!fonts) return
    const refit = () => { if (ref.current) fitHeight(ref.current) }
    fonts.addEventListener('loadingdone', refit)
    return () => fonts.removeEventListener('loadingdone', refit)
  }, [ref])
}
