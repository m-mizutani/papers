import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import type { Paper } from '../src/api/types'
import { blocks, call, createPaper, eventually, homeOf, msg, openPaper, paperOf, unique, WS } from './helpers'

// Databases: a paper of kind "database" whose items are papers, shown as a
// table with one column per property.

interface Seeded {
  db: string
  refs: Record<string, string>
}

/** A database with a select (Status), a date (Due) and two items. */
async function seed(request: APIRequestContext): Promise<Seeded> {
  const db = await call<Paper>(request, 'POST', `/workspaces/${WS}/papers`, { title: unique('Projects'), parent: await homeOf(request), kind: 'database' })
  const res = await call<{ refs: Record<string, string> }>(request, 'POST', `/workspaces/${WS}/papers/${db.id}/ops`, { ops: [
    { op: 'add_property', name: 'Status', property_type: 'select', ref: 's' },
    { op: 'add_element', property: 'ref:s', value: 'Todo', ref: 'todo' },
    { op: 'add_element', property: 'ref:s', value: 'Doing', ref: 'doing' },
    { op: 'add_property', name: 'Due', property_type: 'date', ref: 'due' },
    { op: 'insert_item', title: 'Search', ref: 'i1', set: { 'ref:s': 'ref:doing', 'ref:due': '2026-12-10' } },
    { op: 'insert_item', title: 'Invite', ref: 'i2', set: { 'ref:s': 'ref:todo', 'ref:due': '2027-01-20' } },
    { op: 'insert_item', title: 'Export', ref: 'i3' },
  ] })
  return { db: db.id, refs: res.refs }
}

const rows = (page: Page) => page.locator('.dbt tbody tr[data-item-id]')
const header = (page: Page, name: string) => page.locator('.dbt thead th button', { hasText: name })
const cell = (page: Page, row: number, col: number) => rows(page).nth(row).locator('td').nth(col).locator('button.cellbtn')

async function synced(page: Page) {
  await expect(page.locator('.sync')).toHaveText(msg('synced'))
}

test('T5 the slash menu creates a database and opens its empty table', async ({ page, request }) => {
  const parent = await createPaper(request, { title: unique('Parent'), markdown: 'anchor' })
  await openPaper(page, parent.id)
  await blocks(page).nth(0).hover()
  await blocks(page).nth(0).locator('.hdl').click()
  await page.getByRole('menuitem', { name: msg('insertBelow') }).click()
  await page.locator('.slashpop').getByRole('option', { name: msg('typeDatabase') }).click()

  await expect(page).not.toHaveURL(new RegExp(`/p/${parent.id}$`))
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}/p/[^/]+$`))
  const id = page.url().split('/p/')[1] ?? ''
  await expect(page.locator('.kindmark')).toContainText(msg('database'))
  await expect(page.locator('.dbhint')).toHaveText(msg('dbEmptyHint'))
  const db = await paperOf(request, id)
  expect(db.kind).toBe('database')
  expect(db.parent_id).toBe(parent.id)

  // the parent shows a database card
  await openPaper(page, parent.id)
  await expect(page.locator('.child .kbadge')).toHaveText(msg('database'))
})

test('T11 a property is added from "+" and deleted from its header', async ({ page, request }) => {
  const { db } = await seed(request)
  await openPaper(page, db)
  await page.getByRole('button', { name: msg('addProperty') }).click()
  await page.getByRole('textbox', { name: msg('propertyName') }).fill('Owner')
  await page.getByRole('radio', { name: msg('propSelect'), exact: true }).click()
  await page.getByRole('button', { name: msg('addAction'), exact: true }).click()
  await expect(header(page, 'Owner')).toBeVisible()
  await synced(page)

  await header(page, 'Owner').click()
  await page.getByRole('button', { name: msg('deleteProperty') }).click()
  await page.getByRole('button', { name: msg('deleteAction') }).click()
  await expect(header(page, 'Owner')).toHaveCount(0)
  await page.reload()
  await expect(header(page, 'Status')).toBeVisible()
  await expect(header(page, 'Owner')).toHaveCount(0)
})

test('T17 typing a new value in a select cell creates the element and chooses it', async ({ page, request }) => {
  const { db } = await seed(request)
  await openPaper(page, db)
  await cell(page, 2, 1).click()
  await page.keyboard.type('Review')
  await page.keyboard.press('Enter')
  await expect(cell(page, 2, 1)).toHaveText('Review')
  await synced(page)

  // the new element is offered in another item's cell
  await cell(page, 1, 1).click()
  await expect(page.locator('.dbpop [role="option"]', { hasText: 'Review' })).toBeVisible()
  const saved = await paperOf(request, db)
  expect(saved.properties?.[0]?.elements?.map((e) => e.value)).toEqual(['Todo', 'Doing', 'Review'])
})

test('T23 an item opens with its properties, and a change shows in the table', async ({ page, request }) => {
  const { db, refs } = await seed(request)
  await openPaper(page, db)
  await rows(page).nth(0).hover()
  await rows(page).nth(0).getByRole('link', { name: msg('openItem') }).click()
  await expect(page).toHaveURL(new RegExp(`/p/${refs.i1}$`))
  await expect(page.locator('.crumbs')).toContainText((await paperOf(request, db)).title)
  await expect(page.locator('.props')).toContainText('Doing')

  await page.locator('.props').getByRole('button', { name: 'Due' }).click()
  await page.getByRole('textbox', { name: msg('propDate') }).fill('2026-12-24')
  await page.keyboard.press('Enter')
  await expect(page.locator('.props')).toContainText('2026-12-24')

  await page.goBack()
  await expect(cell(page, 0, 2)).toHaveText('2026-12-24')
})

test('T31 changes in one window show in another without a reload', async ({ page, request, browser }) => {
  const { db, refs } = await seed(request)
  await openPaper(page, db)
  const other = await browser.newPage()
  await openPaper(other, db)

  // a value, a column name and the order of items
  await cell(page, 1, 2).click()
  await page.getByRole('textbox', { name: msg('propDate') }).fill('2028-02-29')
  await page.keyboard.press('Enter')
  await expect(cell(other, 1, 2)).toHaveText('2028-02-29')

  await header(page, 'Status').click()
  await page.getByRole('textbox', { name: msg('propertyName') }).fill('State')
  await page.keyboard.press('Enter')
  await expect(header(other, 'State')).toBeVisible()

  await call(request, 'POST', `/workspaces/${WS}/papers/${db}/ops`, { ops: [{ op: 'move_item', item: refs.i3, after: '' }] })
  await expect(rows(other).nth(0)).toContainText('Export')
  await other.close()
})

test('T35 at phone width the page does not scroll sideways', async ({ page, request }) => {
  const { db, refs } = await seed(request)
  await page.setViewportSize({ width: 390, height: 844 })
  for (const id of [db, refs.i1 ?? '']) {
    await openPaper(page, id)
    const { body, view } = await page.evaluate(() => ({ body: document.body.scrollWidth, view: window.innerWidth }))
    expect(body).toBeLessThanOrEqual(view)
  }
  await openPaper(page, db)
  const wrap = page.locator('.dbwrap')
  expect(await wrap.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true)
})

test('T43 the toolbar filters and sorts, and the URL keeps them', async ({ page, request }) => {
  const { db } = await seed(request)
  await openPaper(page, db)
  await page.getByRole('button', { name: msg('filterItems') }).click()
  await page.getByRole('menuitem', { name: 'Status' }).click()
  // the condition lives in the URL, so a box shows checked once the URL changed
  for (const name of ['Todo', 'Doing']) {
    const box = page.locator('.dbpop label.opt', { hasText: name }).locator('input')
    await box.click()
    await expect(box).toBeChecked()
  }
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: msg('sortItems') }).click()
  await page.getByRole('menuitem', { name: `Due · ${msg('descending')}` }).click()

  await expect(rows(page)).toHaveCount(2)
  await expect(rows(page).locator('td.tt')).toHaveText([/Invite/, /Search/])
  const url = page.url()
  expect(url).toContain('sort=')
  expect(url).toContain('filter=')

  const fresh = await page.context().newPage()
  await fresh.goto(url)
  await expect(rows(fresh)).toHaveCount(2)
  await expect(rows(fresh).locator('td.tt')).toHaveText([/Invite/, /Search/])
  await eventually(async () => rows(fresh).count(), (n) => n === 2, 'same rows')
  await fresh.close()
})
