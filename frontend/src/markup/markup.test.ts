import cases from '../../../testdata/markup/cases.json'
import { normalize, plainText } from './markup'

interface Case { in: string; normalized?: string; plain?: string; error?: boolean }

describe('markup shared cases', () => {
  for (const c of cases as Case[]) {
    it(JSON.stringify(c.in), () => {
      if (c.error) {
        expect(() => normalize(c.in)).toThrow()
        return
      }
      const got = normalize(c.in)
      expect(got).toBe(c.normalized)
      expect(normalize(got)).toBe(got)
      expect(plainText(c.in)).toBe(c.plain)
    })
  }
})
