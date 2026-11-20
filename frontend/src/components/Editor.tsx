import { useLayoutEffect, useRef, type KeyboardEvent } from 'react'
import { isEnter, isImeComposing } from '../utils/keyboard'

export interface EditorKeys {
  /** Enter without Shift: split / finish. Return true if handled. */
  onEnter?: () => void
  /** Backspace in an empty editor. */
  onBackspaceEmpty?: () => void
  onEscape: () => void
  /** When true, Enter inserts a newline and Mod+Enter finishes. */
  multiline?: boolean
}

export function Editor({ value, onChange, onBlur, keys, placeholder, label }: {
  value: string
  onChange: (v: string) => void
  onBlur: () => void
  keys: EditorKeys
  placeholder?: string
  label: string
}) {
  const ref = useRef<HTMLTextAreaElement>(null)

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    el.style.height = '0px'
    el.style.height = `${el.scrollHeight}px`
  }, [value])

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    el.focus()
    el.setSelectionRange(el.value.length, el.value.length)
  }, [])

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (isImeComposing(e)) return
    if (e.key === 'Escape') {
      e.preventDefault()
      keys.onEscape()
      return
    }
    if (isEnter(e)) {
      if (keys.multiline) {
        if (e.metaKey || e.ctrlKey) {
          e.preventDefault()
          keys.onEscape()
        }
        return
      }
      if (!e.shiftKey && keys.onEnter) {
        e.preventDefault()
        keys.onEnter()
      }
      return
    }
    if (e.key === 'Backspace' && value === '' && keys.onBackspaceEmpty) {
      e.preventDefault()
      keys.onBackspaceEmpty()
    }
  }

  return (
    <textarea
      ref={ref}
      className="editor"
      rows={1}
      value={value}
      aria-label={label}
      placeholder={placeholder}
      spellCheck
      onChange={(e) => onChange(e.target.value)}
      onBlur={onBlur}
      onKeyDown={onKeyDown}
    />
  )
}
