import { describe, expect, it } from 'vitest'
import { en } from '../i18n/en'
import { matchChoices } from './blockTypes'

describe('matchChoices', () => {
  const label = (k: keyof typeof en) => en[k]
  it('matches Japanese keywords', () => {
    expect(matchChoices('見出し', label, true).map((c) => c.key)).toEqual(['h1', 'h2', 'h3'])
    expect(matchChoices('表', label, true).map((c) => c.key)).toEqual(['table', 'database'])
    expect(matchChoices('データベース', label, true).map((c) => c.key)).toEqual(['database'])
  })
  it('leaves out child papers where a paper cannot have children', () => {
    expect(matchChoices('', label, false).some((c) => c.type === 'paper')).toBe(false)
    expect(matchChoices('', label, true).some((c) => c.type === 'paper')).toBe(true)
  })
})
