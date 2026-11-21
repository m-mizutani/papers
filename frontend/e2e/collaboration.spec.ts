import { expect, test, type Page } from '@playwright/test'
import { blocks, blocksOf, body, call, createPaper, editor, eventually, finishEditing, msg, openPaper, unique, WS } from './helpers'
import type { Block } from '../src/api/types'

async function startEditing(page: Page, text: string) {
  await blocks(page).nth(0).click()
  await expect(editor(page)).toBeFocused()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.type(text)
}

test('shows a change made elsewhere at once', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Live'), markdown: 'existing' })
  await openPaper(page, paper.id)
  const [first] = await blocksOf(request, paper.id)
  await call(request, 'POST', `/workspaces/${WS}/papers/${paper.id}/ops`, {
    ops: [
      { op: 'insert', type: 'paragraph', content: 'from elsewhere', after: first?.id },
      { op: 'update', block_id: first?.id, base_rev: first?.rev, content: 'existing, edited' },
    ],
  })
  await expect(blocks(page).filter({ hasText: 'from elsewhere' })).toBeVisible()
  await expect(body(page, 0)).toHaveText('existing, edited')
})

for (const choice of ['theirs', 'mine'] as const) {
  test(`resolves a conflict by keeping ${choice}`, async ({ browser, request }) => {
    const paper = await createPaper(request, { title: unique('Conflict'), markdown: 'original' })
    const a = await (await browser.newContext({ locale: 'en-US' })).newPage()
    const b = await (await browser.newContext({ locale: 'en-US' })).newPage()
    await openPaper(a, paper.id)
    await openPaper(b, paper.id)

    // a starts editing; b changes the same block and saves first
    await startEditing(a, 'from A')
    await startEditing(b, 'from B')
    await finishEditing(b)
    await expect(a.locator('.updated-note')).toBeVisible()

    // a saves: the change is based on an older revision
    await a.keyboard.press('Escape')
    const box = a.locator('.conflict')
    await expect(box).toBeVisible()
    await expect(box).toContainText('from B')
    await box.getByRole('button', { name: choice === 'theirs' ? msg('conflictKeepTheirs') : msg('conflictOverwrite') }).click()
    await expect(box).toHaveCount(0)

    const want = choice === 'theirs' ? 'from B' : 'from A'
    await expect(body(a, 0)).toHaveText(want)
    await expect(body(b, 0)).toHaveText(want)
    await eventually(() => blocksOf(request, paper.id), (bs: Block[]) => bs[0]?.content === want, 'saved')
  })
}

test('lets an AI agent write with a token and marks its blocks', async ({ page, request, playwright }) => {
  const paper = await createPaper(request, { title: unique('Agent'), markdown: 'by a person' })
  await page.goto('/settings/tokens')
  const name = unique('agent')
  await page.getByLabel(msg('tokenName')).fill(name)
  await page.getByRole('button', { name: msg('createToken') }).click()
  await expect(page.getByText(msg('tokenCreated'))).toBeVisible()
  const secret = (await page.locator('.secret pre').first().textContent())?.trim() ?? ''
  expect(secret).toMatch(/^pap_/)

  const agent = await playwright.request.newContext({ baseURL: test.info().project.use.baseURL, extraHTTPHeaders: { Authorization: `Bearer ${secret}` } })
  await openPaper(page, paper.id)
  const [first] = await blocksOf(request, paper.id)
  const res = await agent.post(`/api/v1/workspaces/${WS}/papers/${paper.id}/ops`, { data: { ops: [{ op: 'insert', type: 'paragraph', content: 'by an agent', after: first?.id }] } })
  expect(res.ok()).toBeTruthy()
  await expect(page.locator('.block.by-agent', { hasText: 'by an agent' })).toBeVisible()

  // a revoked token stops working
  await page.goto('/settings/tokens')
  await page.locator('.token-row', { hasText: name }).getByRole('button', { name: msg('revoke') }).click()
  await expect(page.locator('.token-row', { hasText: name })).toHaveCount(0)
  const denied = await agent.get(`/api/v1/workspaces/${WS}/papers/${paper.id}`)
  expect(denied.status()).toBe(401)
  await agent.dispose()
})
