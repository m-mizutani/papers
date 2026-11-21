import { expect, test } from '@playwright/test'
import { blocksOf, createPaper, editor, eventually, finishEditing, msg, openPaper, unique } from './helpers'

// A 1x1 transparent PNG.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')

test('builds a table: cells, rows and columns', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Table') })
  await openPaper(page, paper.id)
  await page.getByRole('button', { name: `+ ${msg('typeTable')}` }).click()
  const table = page.locator('.t-table table')
  const rows = table.locator('tr:not(.line-tools)')
  await expect(rows).toHaveCount(2)

  await table.locator('.cell').first().click()
  await expect(editor(page)).toBeFocused()
  await page.keyboard.type('Host')
  await finishEditing(page)
  await expect(table.locator('.cell').first()).toHaveText('Host')

  await page.getByRole('button', { name: msg('addRow') }).click()
  await expect(rows).toHaveCount(3)
  await page.getByRole('button', { name: msg('addColumn') }).click()
  await expect(rows.first().locator('.cell')).toHaveCount(3)
  await page.getByRole('button', { name: msg('removeRow') }).last().click()
  await expect(rows).toHaveCount(2)

  const saved = await eventually(() => blocksOf(request, paper.id), (bs) => bs.filter((b) => b.type === 'table_cell').length === 6, 'six cells')
  const t = saved.find((b) => b.type === 'table')
  expect(t?.props.rows).toHaveLength(2)
  expect(t?.props.cols).toHaveLength(3)
  expect(saved.some((b) => b.type === 'table_cell' && b.content === 'Host')).toBe(true)
})

test('attaches an image', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Image') })
  await openPaper(page, paper.id)
  const chooser = page.waitForEvent('filechooser')
  await page.getByRole('button', { name: `+ ${msg('typeImage')}` }).click()
  await (await chooser).setFiles({ name: 'dot.png', mimeType: 'image/png', buffer: PNG })

  const img = page.locator('.b-image img')
  await expect(img).toBeVisible()
  await expect.poll(() => img.evaluate((el) => (el as HTMLImageElement).naturalWidth)).toBe(1)
  const saved = await eventually(() => blocksOf(request, paper.id), (bs) => bs.some((b) => b.type === 'image' && !!b.props.attachment_id), 'image saved')
  expect(saved.find((b) => b.type === 'image')?.props.attachment_id).toBeTruthy()
})
