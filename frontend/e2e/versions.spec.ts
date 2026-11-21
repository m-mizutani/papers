import { expect, test, type Page } from '@playwright/test'
import { answerDialogs, blocks, body, blocksOf, call, createPaper, editor, eventually, finishEditing, msg, openPaper, paperOf, unique, WS } from './helpers'

async function rewriteFirst(page: Page, text: string) {
  await blocks(page).nth(0).click()
  await expect(editor(page)).toBeFocused()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.type(text)
  await finishEditing(page)
}

test('saves versions, shows them, compares and goes back', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Versioned'), markdown: 'alpha' })
  await openPaper(page, paper.id)
  await expect(page.locator('.badge', { hasText: msg('statusDraft') })).toBeVisible()

  answerDialogs(page, ['first cut', 'second cut', 'before going back'])
  await page.getByRole('button', { name: msg('saveVersion') }).click()
  await expect(page.getByText(msg('versionSaved', { n: 1 }))).toBeVisible()
  await expect(page.locator('.badge', { hasText: msg('atVersion', { n: 1 }) })).toBeVisible()

  await rewriteFirst(page, 'beta')
  await expect(page.locator('.badge', { hasText: msg('changedSince', { n: 1 }) })).toBeVisible()
  await page.getByRole('button', { name: msg('saveVersion') }).click()
  await expect(page.getByText(msg('versionSaved', { n: 2 }))).toBeVisible()
  await rewriteFirst(page, 'gamma')
  await expect(page.locator('.badge', { hasText: msg('changedSince', { n: 2 }) })).toBeVisible()

  // the list of versions, and what changed since v1
  await page.getByRole('button', { name: msg('versions'), exact: true }).click()
  const panel = page.getByRole('complementary', { name: msg('versions') })
  await expect(panel.getByText('first cut')).toBeVisible()
  await expect(panel.getByText('second cut')).toBeVisible()
  await panel.locator('li', { hasText: 'first cut' }).getByRole('button', { name: msg('compareWithCurrent') }).click()
  await expect(panel.locator('ins', { hasText: 'gamma' })).toBeVisible()

  // v1 opens read-only
  await panel.getByRole('link', { name: 'v1', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/p/${paper.id}/v/1$`))
  await expect(page.locator('.version-flag')).toContainText(msg('showingVersion', { n: 1 }))
  await expect(body(page, 0)).toHaveText('alpha')
  await expect(page.getByRole('button', { name: msg('saveVersion') })).toHaveCount(0)

  // going back to v1 first offers to save the unsaved change (v3)
  await page.getByRole('button', { name: msg('restoreThisVersion') }).click()
  await expect(page).toHaveURL(new RegExp(`/p/${paper.id}$`))
  await expect(body(page, 0)).toHaveText('alpha')
  await eventually(() => paperOf(request, paper.id), (p) => p.version_number === 3, 'v3 saved')
  expect((await blocksOf(request, paper.id))[0]?.content).toBe('alpha')

  // the latest version holds what was there before going back
  await page.getByRole('link', { name: msg('latestVersion') }).click()
  await expect(body(page, 0)).toHaveText('gamma')
  await expect(page.getByRole('link', { name: msg('openCurrent') })).toBeVisible()
})

test('saving without changes makes no version', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Still'), markdown: 'same' })
  await call(request, 'POST', `/workspaces/${WS}/papers/${paper.id}/versions`, { note: 'v1' })
  await openPaper(page, paper.id)
  answerDialogs(page, [''])
  await page.getByRole('button', { name: msg('saveVersion') }).click()
  await expect(page.getByText(msg('versionUnchanged'))).toBeVisible()
})
