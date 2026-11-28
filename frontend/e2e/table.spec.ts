import { expect, test, type Page } from '@playwright/test'
import { agent, blocks, blocksOf, call, createPaper, drag, editor, eventually, msg, openPaper, ops, synced, unique, WS } from './helpers'
import type { Block } from '../src/api/types'

/** A paper with one table, filled row by row. */
async function tablePaper(request: Parameters<typeof createPaper>[0], cells: string[][]) {
  const paper = await createPaper(request, { title: unique('Table') })
  await ops(request, paper.id, [{ op: 'insert', type: 'table', props: { header_row: false }, cells }])
  return paper
}

/** The table block and its cells, as rows of contents in the table's order. */
async function grid(request: Parameters<typeof createPaper>[0], paper: string): Promise<{ table: Block; rows: string[][]; cell: (r: number, c: number) => Block | undefined }> {
  const all = await blocksOf(request, paper)
  const table = all.find((b) => b.type === 'table')
  if (!table) throw new Error('no table')
  const sort = <T extends { order: string; id: string }>(xs: T[]) => [...xs].sort((a, b) => (a.order < b.order ? -1 : a.order > b.order ? 1 : a.id < b.id ? -1 : 1))
  const rows = sort(table.props.rows ?? [])
  const cols = sort(table.props.cols ?? [])
  const at = (r: string, c: string) => all.find((b) => b.parent_id === table.id && b.props.row_id === r && b.props.col_id === c)
  return {
    table,
    rows: rows.map((r) => cols.map((c) => at(r.id, c.id)?.content ?? '')),
    cell: (r, c) => at(rows[r]?.id ?? '', cols[c]?.id ?? ''),
  }
}

const rowsOf = (page: Page) => page.locator('.blk.t-table table.tb tr')

test('T7 the bottom strip, the right strip and Tab in the last cell add a row, a column and a row', async ({ page, request }) => {
  const paper = await tablePaper(request, [['a', 'b'], ['c', 'd']])
  await openPaper(page, paper.id)
  await expect(rowsOf(page)).toHaveCount(2)

  await page.locator('.blk.t-table').hover()
  await page.getByRole('button', { name: msg('addRow') }).click()
  await expect(rowsOf(page)).toHaveCount(3)
  await synced(page)

  await page.getByRole('button', { name: msg('addColumn') }).click()
  await expect(rowsOf(page).first().locator('td')).toHaveCount(3)
  await synced(page)
  await eventually(() => grid(request, paper.id), (x) => x.rows.length === 3 && x.rows[0]?.length === 3, 'row and column added')

  // Tab in the last cell adds a row and goes on in its first cell
  await rowsOf(page).nth(2).locator('td').nth(2).locator('.cell').click()
  await expect(editor(page)).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(rowsOf(page)).toHaveCount(4)
  await expect(rowsOf(page).nth(3).locator('td').first()).toHaveClass(/focus/)
  await page.keyboard.type('new')
  await page.keyboard.press('Escape')
  await synced(page)
  const g = await eventually(() => grid(request, paper.id), (x) => x.rows.length === 4 && x.rows[3]?.[0] === 'new', 'tab row added')
  expect(g.rows).toEqual([['a', 'b', ''], ['c', 'd', ''], ['', '', ''], ['new', '', '']])
})

test('T8 dragging a row grip moves the row with its cells', async ({ page, request }) => {
  const paper = await tablePaper(request, [['r1', 'x1'], ['r2', 'x2'], ['r3', 'x3'], ['r4', 'x4']])
  await openPaper(page, paper.id)
  await expect(rowsOf(page)).toHaveCount(4)
  const row2 = await rowsOf(page).nth(1).boundingBox()
  if (!row2) throw new Error('no row')
  // the grip shows while the row is hovered
  await rowsOf(page).nth(3).hover()
  const grip = rowsOf(page).nth(3).getByRole('button', { name: msg('dragRow') })
  await expect(grip).toBeVisible()
  await drag(page, grip, { x: row2.x + row2.width / 2, y: row2.y + 3 })

  await expect(rowsOf(page).locator('td:first-child')).toHaveText(['r1', 'r4', 'r2', 'r3'])
  await expect(rowsOf(page).locator('td:nth-child(2)')).toHaveText(['x1', 'x4', 'x2', 'x3'])
  await eventually(() => grid(request, paper.id), (x) => JSON.stringify(x.rows) === JSON.stringify([['r1', 'x1'], ['r4', 'x4'], ['r2', 'x2'], ['r3', 'x3']]), 'row moved')
})

test('T9 deleting a row someone is editing asks first, and cancel keeps it', async ({ page, request, playwright, baseURL }) => {
  const paper = await tablePaper(request, [['a', 'b'], ['busy', 'c'], ['d', 'e']])
  const other = await agent(playwright, request, baseURL ?? '', 'Robo')
  await openPaper(page, paper.id)
  const g = await grid(request, paper.id)
  const busy = g.cell(1, 0)
  await call(other.api, 'POST', `/workspaces/${WS}/papers/${paper.id}/presence`, { block_id: busy?.id, leaving: false })
  await expect(rowsOf(page).nth(1).locator('td').first().locator('.ptag')).toHaveText(msg('editing', { name: other.name }))

  // a click on the row grip (without dragging) opens the row menu
  await rowsOf(page).nth(1).hover()
  await rowsOf(page).nth(1).getByRole('button', { name: msg('dragRow') }).click()
  await page.getByRole('menuitem', { name: msg('deleteRow') }).click()
  const dialog = page.getByRole('dialog', { name: msg('cellBusyTitle', { name: `${other.name} (${msg('forUser', { name: 'Developer' })})` }) })
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText(other.name)
  await dialog.getByRole('button', { name: msg('cancel') }).click()
  await expect(dialog).toHaveCount(0)

  await expect(rowsOf(page)).toHaveCount(3)
  const after = await grid(request, paper.id)
  expect(after.rows).toEqual([['a', 'b'], ['busy', 'c'], ['d', 'e']])
  expect(after.table.rev).toBe(g.table.rev)
  await other.api.dispose()
})

test('T15 a value in an html block is edited in place; Escape puts it back', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Fields') })
  const res = await ops(request, paper.id, [{
    op: 'insert', ref: 'h', type: 'html',
    content: '<p>Timeout: <b data-field="timeout">5m</b></p>',
    props: { fields: { timeout: '5m' } },
  }])
  const id = res.refs?.h ?? ''
  const rev0 = res.blocks?.find((b) => b.id === id)?.rev ?? 0
  await openPaper(page, paper.id)
  const frame = blocks(page).first().frameLocator('iframe')
  const value = frame.locator('[data-field="timeout"]')
  await expect(value).toHaveText('5m')

  await value.click()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.type('10m')
  await page.keyboard.press('Enter')
  const saved = await eventually(async () => (await blocksOf(request, paper.id)).find((b) => b.id === id), (b) => b?.props.fields?.timeout === '10m', 'field saved')
  expect(saved?.rev).toBeGreaterThan(rev0)
  await expect(value).toHaveText('10m')

  // Escape puts the value back and sends nothing
  await value.click()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.type('99h')
  await expect(value).toHaveText('99h')
  await page.keyboard.press('Escape')
  await expect(value).toHaveText('10m')
  await synced(page)
  const still = (await blocksOf(request, paper.id)).find((b) => b.id === id)
  expect(still?.rev).toBe(saved?.rev)
  expect(still?.props.fields?.timeout).toBe('10m')
})
