import type { KeyboardEvent } from 'react'

/** True while an IME is composing (CJK input). Enter then confirms a conversion. */
export function isImeComposing(e: KeyboardEvent): boolean {
  return e.nativeEvent.isComposing || e.keyCode === 229
}

/** Enter pressed outside of IME composition. */
export function isEnter(e: KeyboardEvent): boolean {
  return e.key === 'Enter' && !isImeComposing(e) // eslint-disable-line no-restricted-syntax
}
