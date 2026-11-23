import { describe, expect, it } from 'vitest'
import { isMacPlatform, modKey } from './keyboard'

describe('modKey', () => {
  it('on a Mac reacts to Cmd only, leaving Ctrl to Emacs-style editing', () => {
    expect(isMacPlatform('MacIntel')).toBe(true)
    expect(modKey({ metaKey: false, ctrlKey: true }, true)).toBe(false)
    expect(modKey({ metaKey: true, ctrlKey: false }, true)).toBe(true)
    expect(modKey({ metaKey: true, ctrlKey: true }, true)).toBe(false)
  })

  it('elsewhere reacts to Ctrl only', () => {
    expect(isMacPlatform('Linux x86_64')).toBe(false)
    expect(modKey({ metaKey: false, ctrlKey: true }, false)).toBe(true)
    expect(modKey({ metaKey: true, ctrlKey: false }, false)).toBe(false)
  })
})
