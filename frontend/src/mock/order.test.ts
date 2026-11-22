import { describe, expect, it } from 'vitest'
import { keyBetween } from './order'

describe('keyBetween', () => {
  it('stays ordered under random insertion', () => {
    const keys = [keyBetween('', '')]
    for (let i = 0; i < 500; i++) {
      const pos = (i * 7919) % (keys.length + 1)
      const k = keyBetween(keys[pos - 1] ?? '', keys[pos] ?? '')
      if (pos > 0) expect((keys[pos - 1] as string) < k).toBe(true)
      if (pos < keys.length) expect(k < (keys[pos] as string)).toBe(true)
      keys.splice(pos, 0, k)
    }
  })
})
