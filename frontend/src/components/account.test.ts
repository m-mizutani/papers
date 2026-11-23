import { describe, expect, it } from 'vitest'
import { accountNote, canSignOut } from './account'
import { t } from '../i18n'

describe('account menu', () => {
  it('explains anonymous mode and offers no sign-out', () => {
    const me = { user: { id: 'u1', email: 'u1@anonymous.invalid', name: 'Anonymous 7fQa', anonymous: true }, no_authn: false, anonymous: true }
    expect(accountNote(me)).toBe(t('anonymousMode'))
    expect(canSignOut(me)).toBe(false)
  })

  it('shows the email and offers sign-out to a signed-in user', () => {
    const me = { user: { id: 'u2', email: 'alice@example.com', name: 'Alice' }, no_authn: false, anonymous: false }
    expect(accountNote(me)).toBe('alice@example.com')
    expect(canSignOut(me)).toBe(true)
  })
})
