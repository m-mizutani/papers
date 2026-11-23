import { expect, test, type Page } from '@playwright/test'
import { ANONYMOUS_URL, WS, call, createPaper, msg, unique } from './helpers'

// Every test gets a fresh browser context, that is a browser without a
// session cookie, so it becomes a new anonymous user on its first request.
test.use({ baseURL: ANONYMOUS_URL })

async function guestName(page: Page): Promise<string> {
  const me = page.getByRole('navigation', { name: msg('navigation') }).locator('.me')
  await expect(me).toContainText('Anonymous ')
  return (await me.textContent())?.trim() ?? ''
}

test('each browser is its own anonymous user and keeps it across reloads', async ({ page, browser }) => {
  await page.goto(`/ws/${WS}`)
  const nav = page.getByRole('navigation', { name: msg('navigation') })
  await expect(nav.getByText(msg('anonymousMode'))).toBeVisible()
  await expect(nav.getByRole('button', { name: msg('signOut') })).toHaveCount(0)
  const nameA = await guestName(page)

  // page.request shares the browser's cookies, so this is the same guest
  const word = unique('guestnote').replace(' ', '')
  const paper = await createPaper(page.request, { title: word, visibility: 'private' })
  await page.reload()
  await expect(nav.getByRole('link', { name: word })).toBeVisible()
  expect(await guestName(page)).toBe(nameA)
  await expect.poll(async () => (await call<{ hits: unknown[] }>(page.request, 'GET', `/workspaces/${WS}/search?q=${word}`)).hits.length).toBe(1)

  // another browser is another guest and cannot see the private paper
  const other = await browser.newContext({ baseURL: ANONYMOUS_URL })
  const pageB = await other.newPage()
  await pageB.goto(`/ws/${WS}`)
  const nameB = await guestName(pageB)
  expect(nameB).not.toBe(nameA)
  await expect(pageB.getByRole('navigation', { name: msg('navigation') }).getByRole('link', { name: word })).toHaveCount(0)
  const hits = await call<{ hits: unknown[] }>(pageB.request, 'GET', `/workspaces/${WS}/search?q=${word}`)
  expect(hits.hits).toHaveLength(0)
  const res = await pageB.request.get(`/api/v1/workspaces/${WS}/papers/${paper.id}`)
  expect(res.status()).toBe(404)
  await other.close()
})
