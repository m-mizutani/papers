import { expect, test, type Page } from '@playwright/test'
import { ANONYMOUS_URL, WS, call, createPaper, msg, unique } from './helpers'

// Every test gets a fresh browser context, that is a browser without a
// session cookie, so it becomes a new anonymous user on its first request.
test.use({ baseURL: ANONYMOUS_URL })

/** The account button at the bottom of the sidebar, which shows the user's name. */
function account(page: Page) {
  return page.getByRole('complementary', { name: msg('workspace') }).locator('.sb-foot button.sb-row')
}

async function guestName(page: Page): Promise<string> {
  await expect(account(page)).toContainText(/Anonymous \w{4}/)
  return (await account(page).locator('.lbl').textContent())?.trim() ?? ''
}

test('each browser is its own anonymous user and keeps it across reloads', async ({ page, browser }) => {
  await page.goto(`/ws/${WS}`)
  const nameA = await guestName(page)

  // the account menu explains anonymous mode and offers no sign-out
  await account(page).click()
  const menu = page.getByRole('menu')
  await expect(menu).toContainText(msg('anonymousMode'))
  await expect(menu.getByRole('menuitem', { name: msg('signOut') })).toHaveCount(0)
  await page.keyboard.press('Escape')

  // page.request shares the browser's cookies, so this is the same guest
  const word = unique('guestnote').replace(' ', '')
  const paper = await createPaper(page.request, { title: word, visibility: 'private' })
  await page.goto(`/ws/${WS}/papers`)
  await expect(page.getByText(word)).toBeVisible()
  await page.reload()
  await expect(page.getByText(word)).toBeVisible()
  expect(await guestName(page)).toBe(nameA)
  await expect.poll(async () => (await call<{ hits: unknown[] }>(page.request, 'GET', `/workspaces/${WS}/search?q=${word}`)).hits.length).toBe(1)

  // another browser is another guest and cannot see the private paper
  const other = await browser.newContext({ baseURL: ANONYMOUS_URL })
  const pageB = await other.newPage()
  await pageB.goto(`/ws/${WS}`)
  expect(await guestName(pageB)).not.toBe(nameA)
  const hits = await call<{ hits: unknown[] }>(pageB.request, 'GET', `/workspaces/${WS}/search?q=${word}`)
  expect(hits.hits).toHaveLength(0)
  const res = await pageB.request.get(`/api/v1/workspaces/${WS}/papers/${paper.id}`)
  expect(res.status()).toBe(404)
  await other.close()
})
