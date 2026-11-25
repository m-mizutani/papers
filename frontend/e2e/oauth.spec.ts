import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { createHash, randomBytes } from 'node:crypto'
import { call, msg } from './helpers'
import type { ApiToken, Me } from '../src/api/types'

// An MCP client connecting through OAuth: it registers, the person answers
// the consent screen in the browser, and the client exchanges the code.

const REDIRECT = 'http://localhost:51234/callback'
const CLIENT = 'Claude Code (papers)'

function pkce() {
  const verifier = randomBytes(32).toString('base64url')
  return { verifier, challenge: createHash('sha256').update(verifier).digest('base64url') }
}

async function register(request: APIRequestContext): Promise<string> {
  const res = await request.post('/oauth/register', { data: { client_name: CLIENT, redirect_uris: [REDIRECT] } })
  expect(res.status()).toBe(201)
  return ((await res.json()) as { client_id: string }).client_id
}

/** Opens the consent screen and catches the redirect back to the client. */
async function openConsent(page: Page, clientID: string, challenge: string): Promise<() => Promise<URL>> {
  let back: URL | undefined
  await page.route('http://localhost:51234/**', async (route) => {
    back = new URL(route.request().url())
    await route.fulfill({ contentType: 'text/html', body: '<html>back in the client</html>' })
  })
  const q = new URLSearchParams({
    response_type: 'code', client_id: clientID, redirect_uri: REDIRECT, code_challenge: challenge,
    code_challenge_method: 'S256', state: 'st-1', scope: 'read write',
  })
  await page.goto(`/oauth/authorize?${q.toString()}`)
  return async () => {
    await expect.poll(() => back?.toString() ?? '').not.toBe('')
    return back as URL
  }
}

test('OAuth: allowing on the consent screen gives the client a code it exchanges for a token', async ({ page, request }) => {
  const clientID = await register(request)
  const { verifier, challenge } = pkce()
  const returned = await openConsent(page, clientID, challenge)

  await expect(page.getByRole('heading', { name: msg('oauthTitle', { client: CLIENT }) })).toBeVisible()
  await expect(page.getByRole('radio', { name: msg('readWrite') })).toBeChecked()
  await page.getByRole('button', { name: msg('oauthAllow'), exact: true }).click()

  const back = await returned()
  expect(back.searchParams.get('state')).toBe('st-1')
  const code = back.searchParams.get('code') ?? ''
  expect(code).toMatch(/^pac_/)

  const res = await request.post('/oauth/token', {
    form: { grant_type: 'authorization_code', code, redirect_uri: REDIRECT, client_id: clientID, code_verifier: verifier },
  })
  expect(res.status()).toBe(200)
  expect(res.headers()['cache-control']).toBe('no-store')
  const tok = (await res.json()) as { access_token: string; token_type: string; scope: string }
  expect(tok.access_token).toMatch(/^pap_/)
  expect(tok.token_type).toBe('Bearer')
  expect(tok.scope).toBe('write')

  // the token works as a bearer token and is listed as an OAuth token
  const ws = await request.get('/api/v1/workspaces', { headers: { Authorization: `Bearer ${tok.access_token}` } })
  expect(ws.status()).toBe(200)
  const list = await call<{ tokens: ApiToken[] }>(request, 'GET', '/tokens')
  const listed = list.tokens.find((t) => t.oauth?.client_name === CLIENT && t.oauth.redirect_uri === REDIRECT)
  expect(listed?.kind).toBe('agent')
  await page.unroute('http://localhost:51234/**')
  await page.goto('/settings')
  await expect(page.locator('.lr', { hasText: CLIENT }).first()).toContainText(msg('tokenOAuth'))
  await call(request, 'DELETE', `/tokens/${listed?.id ?? ''}`)
})

test('OAuth: the consent screen names the client, where it returns and the account, and a denial goes back with access_denied', async ({ page, request }) => {
  const clientID = await register(request)
  const returned = await openConsent(page, clientID, pkce().challenge)
  const me = (await (await request.get('/api/auth/me')).json()) as Me

  await expect(page.getByText('localhost:51234')).toBeVisible()
  await expect(page.getByText(msg('oauthAccountValue', { name: me.user.name, email: me.user.email }))).toBeVisible()
  await expect(page.getByText(msg('days', { n: 90 }))).toBeVisible()
  await page.getByRole('radio', { name: msg('readOnly') }).check()
  await expect(page.getByText(msg('oauthInfoRead'))).toBeVisible()
  await page.getByRole('button', { name: msg('oauthDeny') }).click()

  const back = await returned()
  expect(back.searchParams.get('error')).toBe('access_denied')
  expect(back.searchParams.get('state')).toBe('st-1')
  expect(back.searchParams.get('code')).toBeNull()
})

test('OAuth: a client that asks to read only cannot be given write access', async ({ page, request }) => {
  const clientID = await register(request)
  const q = new URLSearchParams({
    response_type: 'code', client_id: clientID, redirect_uri: REDIRECT, code_challenge: pkce().challenge,
    code_challenge_method: 'S256', state: 's', scope: 'read',
  })
  await page.goto(`/oauth/authorize?${q.toString()}`)
  await expect(page.getByRole('radio', { name: msg('readOnly') })).toBeChecked()
  await expect(page.getByRole('radio', { name: msg('readWrite') })).toBeDisabled()
})

test('OAuth: a request whose redirect is not registered is shown as an error and not followed', async ({ page, request }) => {
  const clientID = await register(request)
  const q = new URLSearchParams({
    response_type: 'code', client_id: clientID, redirect_uri: 'http://localhost:9/elsewhere', code_challenge: pkce().challenge,
    code_challenge_method: 'S256', state: 's',
  })
  await page.goto(`/oauth/authorize?${q.toString()}`)
  await expect(page.getByRole('heading', { name: msg('oauthErrorTitle') })).toBeVisible()
  await expect(page.getByRole('alert')).toHaveText(msg('oauthErrRedirect'))
  await expect(page.getByRole('link', { name: msg('oauthBack') })).toHaveAttribute('href', '/')
  expect(new URL(page.url()).pathname).toBe('/oauth/authorize')
})

test('OAuth: settings show the command for the MCP URL with no token in it', async ({ page, baseURL }) => {
  await page.goto('/settings')
  const command = `claude mcp add --transport http papers ${baseURL ?? ''}/mcp`
  await expect(page.locator('pre.out')).toHaveText(command)
  await expect(page.getByText(msg('mcpHint'))).toBeVisible()
  await expect(page.getByRole('button', { name: msg('copyCommand') })).toBeVisible()
})
