import { describe, expect, it } from 'vitest'
import { oauthErrorMessage, oauthRequestFrom } from './OAuthAuthorizePage'

describe('oauthErrorMessage', () => {
  it('has a message for each request error the server reports', () => {
    expect(oauthErrorMessage('invalid_client')).toBe('oauthErrInvalidClient')
    expect(oauthErrorMessage('invalid_redirect_uri')).toBe('oauthErrRedirect')
    expect(oauthErrorMessage('unsupported_response_type')).toBe('oauthErrResponseType')
    expect(oauthErrorMessage('invalid_code_challenge')).toBe('oauthErrChallenge')
    expect(oauthErrorMessage('invalid_target')).toBe('oauthErrResource')
    expect(oauthErrorMessage('something else')).toBe('oauthErrOther')
  })
})

describe('oauthRequestFrom', () => {
  it('reads the authorization request from the query', () => {
    const q = new URLSearchParams('response_type=code&client_id=pcl_x&redirect_uri=http%3A%2F%2Flocalhost%3A1%2Fcb&code_challenge=c&code_challenge_method=S256&state=s&scope=read&resource=https%3A%2F%2Fp%2Fmcp')
    expect(oauthRequestFrom(q)).toEqual({
      response_type: 'code', client_id: 'pcl_x', redirect_uri: 'http://localhost:1/cb', code_challenge: 'c',
      code_challenge_method: 'S256', state: 's', scope: 'read', resource: 'https://p/mcp',
    })
    expect(oauthRequestFrom(new URLSearchParams()).client_id).toBe('')
  })
})
