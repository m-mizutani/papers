import { expect, test } from '@playwright/test'
import { createPaper, msg, openPaper, paperOf, unique, waitLive, WS } from './helpers'

test('T16 an icon picked on a paper shows above its title, in the crumbs, the sidebar and the papers list', async ({ page, request }) => {
  const parent = await createPaper(request, { title: unique('Shelf') })
  const paper = await createPaper(request, { title: unique('Launch'), parent: parent.id })
  await openPaper(page, paper.id)
  await waitLive(page)

  // without an icon: the default shows in the crumbs, and "Add icon" opens the picker
  await expect(page.locator('.crumbs .crumb.cur')).toContainText('📄')
  await page.getByRole('button', { name: msg('addIcon') }).click()
  await page.locator('.iconpick').getByRole('button', { name: '🚀', exact: true }).click()

  await expect(page.getByRole('button', { name: msg('changeIcon') })).toHaveText('🚀')
  await expect(page.locator('.crumbs .crumb.cur')).toContainText('🚀')
  await expect.poll(async () => (await paperOf(request, paper.id)).icon).toBe('🚀')
  const recent = page.locator('.sb-scroll .tree-item').filter({ hasText: paper.title })
  await expect(recent.first().locator('.ticon')).toHaveText('🚀')

  // a typed emoji replaces it, and it stays after a reload
  await page.getByRole('button', { name: msg('changeIcon') }).click()
  await page.getByLabel(msg('iconInput')).fill('🧪')
  await page.getByLabel(msg('iconInput')).press('Enter')
  await expect(page.getByRole('button', { name: msg('changeIcon') })).toHaveText('🧪')
  await page.reload()
  await expect(page.getByRole('button', { name: msg('changeIcon') })).toHaveText('🧪')

  await page.goto(`/ws/${WS}/papers?place=all`)
  const row = page.locator('.plist tbody tr').filter({ hasText: paper.title })
  await expect(row.locator('.ptt .pic')).toHaveText('🧪')

  // removing it brings back "Add icon" and the default
  await openPaper(page, paper.id)
  await page.getByRole('button', { name: msg('changeIcon') }).click()
  await page.locator('.iconpick').getByRole('button', { name: msg('iconRemove') }).click()
  await expect(page.getByRole('button', { name: msg('addIcon') })).toBeAttached()
  await expect.poll(async () => (await paperOf(request, paper.id)).icon ?? '').toBe('')
})
