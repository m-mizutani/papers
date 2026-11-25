import { describe, expect, it } from 'vitest'
import { loginPath } from './login'

describe('loginPath', () => {
  // T22: signing in from the consent screen comes back to it with its query
  it('keeps the path and the query', () => {
    const search = '?response_type=code&client_id=pcl_x&redirect_uri=http%3A%2F%2Flocalhost%3A5%2Fcb&state=s'
    const path = loginPath('/oauth/authorize', search)
    const returnTo = new URL(path, 'http://x').searchParams.get('return_to')
    expect(returnTo).toBe('/oauth/authorize' + search)
  })
  it('adds the error', () => {
    expect(loginPath('/settings', '', 'expired')).toBe('/login?return_to=%2Fsettings&error=expired')
  })
})
