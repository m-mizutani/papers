import type { KeyboardEvent } from 'react'

/** True while an IME is composing (CJK input). Enter then confirms a conversion. */
export function isImeComposing(e: KeyboardEvent): boolean {
  return e.nativeEvent.isComposing || e.keyCode === 229
}

/** Enter pressed outside of IME composition. */
export function isEnter(e: KeyboardEvent): boolean {
  return e.key === 'Enter' && !isImeComposing(e) // eslint-disable-line no-restricted-syntax
}

/** isEnter for DOM events (the rich editor gets native events). */
export function isNativeEnter(e: globalThis.KeyboardEvent): boolean {
  return e.key === 'Enter' && !e.isComposing && e.keyCode !== 229 // eslint-disable-line no-restricted-syntax
}

/** True on Apple platforms, where Cmd is the primary modifier and Ctrl is left to Emacs-style editing. */
export function isMacPlatform(platform = typeof navigator === 'undefined' ? '' : navigator.platform): boolean {
  return /Mac|iPhone|iPad/.test(platform)
}

/** The platform's primary modifier alone: Cmd on Apple platforms, Ctrl elsewhere (never both). */
export function modKey(e: { metaKey: boolean; ctrlKey: boolean }, mac = isMacPlatform()): boolean {
  return mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey
}

/** The label of the primary modifier, for shortcut hints. */
export const MOD_LABEL = isMacPlatform() ? '⌘' : 'Ctrl'
