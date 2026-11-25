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

test('T14 an emoji found by its name in the picker becomes the icon', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Party') })
  await openPaper(page, paper.id)
  await waitLive(page)
  await page.getByRole('button', { name: msg('addIcon') }).click()
  await page.getByLabel(msg('iconInput')).fill('beer')
  await page.locator('.iconpick').getByRole('button', { name: '🍺', exact: true }).click()
  await expect(page.getByRole('button', { name: msg('changeIcon') })).toHaveText('🍺')
  await expect.poll(async () => (await paperOf(request, paper.id)).icon).toBe('🍺')
})

test('T15 the icon above the title has room above and below, and lines up with the title', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Spacing') })
  await openPaper(page, paper.id)
  await waitLive(page)
  await page.getByRole('button', { name: msg('addIcon') }).click()
  await page.locator('.iconpick').getByRole('button', { name: '✅', exact: true }).click()
  const icon = page.getByRole('button', { name: msg('changeIcon') })
  await expect(icon).toHaveText('✅')
  const box = async (sel: string) => {
    const b = await page.locator(sel).first().boundingBox()
    if (!b) throw new Error(`no box for ${sel}`)
    return b
  }
  // the glyph sits inside the button's padding; measure the glyph box
  const glyph = await icon.evaluate((el) => {
    const pad = parseFloat(getComputedStyle(el).paddingLeft)
    const e = el.getBoundingClientRect()
    return { top: e.top + pad, bottom: e.bottom - pad, left: e.left + pad }
  })
  const meta = await box('.meta')
  const title = await box('.title-input')
  const titlePad = await page.locator('.title-input').evaluate((el) => parseFloat(getComputedStyle(el).paddingLeft))
  expect(glyph.top - (meta.y + meta.height)).toBeGreaterThanOrEqual(28)
  expect(title.y - glyph.bottom).toBeGreaterThanOrEqual(16)
  expect(Math.abs(glyph.left - (title.x + titlePad))).toBeLessThanOrEqual(2)
})
