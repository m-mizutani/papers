import { describe, expect, it } from 'vitest'
import { en } from './en'
import { ja } from './ja'
import { keys } from './keys'
import { t } from './index'

describe('i18n', () => {
  it('has every key in every language', () => {
    for (const k of Object.keys(keys)) {
      expect(en[k as keyof typeof en]).toBeTruthy()
      expect(ja[k as keyof typeof ja]).toBeTruthy()
    }
  })

  it('interpolates parameters', () => {
    expect(t('revision', { n: 3 })).toMatch(/3/)
  })
})
