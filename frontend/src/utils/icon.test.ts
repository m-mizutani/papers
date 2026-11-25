import { describe, expect, it } from 'vitest'
import { DEFAULT_ICONS, isIcon, paperIcon } from './icon'

describe('paperIcon', () => {
  it('T13 shows the icon set, else the default for the home or the kind', () => {
    expect(paperIcon({ id: 'a', icon: '🚀', kind: 'database' }, 'a')).toBe('🚀')
    expect(paperIcon({ id: 'h', kind: 'blocks' }, 'h')).toBe(DEFAULT_ICONS.home)
    expect(paperIcon({ id: 'a', kind: 'blocks' }, 'h')).toBe('📄')
    expect(paperIcon({ id: 'a', kind: 'database' })).toBe('🗃️')
    expect(paperIcon({ id: 'a', kind: 'html' })).toBe('🧩')
    expect(paperIcon({ id: 'a' })).toBe('📄')
    expect(DEFAULT_ICONS.home).toBe('🧭')
  })
})

describe('isIcon', () => {
  it('accepts one character on screen of at most 32 bytes', () => {
    for (const ok of ['🚀', ' 🚀 ', '👩‍💻', '🇯🇵', '1️⃣', '★']) expect(isIcon(ok)).toBe(true)
    for (const bad of ['', ' ', '🚀🔥', 'ab', '\u0007', '👩🏻‍👩🏻‍👧🏻‍👦🏻', '\u200b', '\u200d', '\u0301', '\ufe0f']) expect(isIcon(bad)).toBe(false)
  })
})
