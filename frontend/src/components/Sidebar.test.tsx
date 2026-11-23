import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { SidebarFoot } from './Sidebar'
import { t } from '../i18n'
import type { Me } from '../api/types'

const user = { id: 'u1', email: 'u1@anonymous.invalid', name: 'Anonymous 7fQa' }

function foot(me: Me) {
  return render(<MemoryRouter><SidebarFoot me={me} /></MemoryRouter>).container
}

describe('SidebarFoot', () => {
  it('marks anonymous mode and offers no sign-out', () => {
    const c = foot({ user: { ...user, anonymous: true }, no_authn: false, anonymous: true })
    expect(c.querySelector('.dev-banner')?.textContent).toBe(t('anonymousMode'))
    expect(c.textContent).toContain('Anonymous 7fQa')
    expect(c.textContent).not.toContain(t('signOut'))
  })

  it('keeps sign-out and no banner for a signed-in user', () => {
    const c = foot({ user: { id: 'u2', email: 'alice@example.com', name: 'Alice' }, no_authn: false, anonymous: false })
    expect(c.querySelector('.dev-banner')).toBeNull()
    expect(c.textContent).toContain(t('signOut'))
  })
})
