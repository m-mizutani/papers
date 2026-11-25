import { expect, test } from '@playwright/test'
import type { Paper } from '../src/api/types'
import { blocksOf, body, call, createPaper, editBlock, editor, finishEditing, homeOf, msg, openPaper, paperOf, synced, unique, WS } from './helpers'

// People shown as mentions: "@name" in text, the creator in the papers list
// and person property values, each opening a card that links to the papers
// the person created. The e2e server has one person, the no-authn dev user.

const DEV = 'Developer'
// A person who is not a user of the server; mentions and person values
// keep the name written with them, so this one still shows.
const ALEX = { user_id: '0Ud2lz0GcCaMILz7tUH0us', name: 'Alex Chen' }


test('T7 typing @ in a block inserts a mention, removed whole by one Backspace', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Mention'), markdown: 'Ask' })
  await openPaper(page, paper.id)
  await editBlock(page, 0)
  await page.keyboard.type(' @Dev')
  await expect(page.locator('.mentionpop [role="option"]', { hasText: DEV })).toBeVisible()
  await page.keyboard.press('Enter')
  await expect(editor(page).locator('.mention')).toHaveText(`@${DEV}`)
  await finishEditing(page)

  await expect(body(page, 0).locator('.mention')).toHaveText(`@${DEV}`)
  const id = paper.created_by.user_id
  const saved = await blocksOf(request, paper.id)
  expect(saved[0]?.content).toContain(`[@${DEV}]{user=${id}}`)

  // the space after the mention, then the mention itself
  await editBlock(page, 0)
  await page.keyboard.press('Backspace')
  await page.keyboard.press('Backspace')
  await expect(editor(page).locator('.mention')).toHaveCount(0)
  await finishEditing(page)
  expect((await blocksOf(request, paper.id))[0]?.content.trim()).toBe('Ask')
})

test('T9 the papers list shows the author as a mention', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Listed'), unfiled: true })
  await page.goto(`/ws/${WS}/papers`)
  const row = page.locator('.dbt tbody tr', { hasText: paper.title })
  const author = row.locator('.mention')
  await expect(author).toHaveText(`@${DEV}`)
  await author.click()
  await expect(page.locator('.personcard')).toContainText(DEV)
})

test('T12 a mention in text opens a card whose link lists the person\'s papers', async ({ page, request }) => {
  const mine = await createPaper(request, { title: unique('Mine'), unfiled: true })
  const id = mine.created_by.user_id
  const paper = await createPaper(request, { title: unique('Text'), markdown: `Ask [@${DEV}]{user=${id}} about it` })
  await openPaper(page, paper.id)
  await body(page, 0).locator('.mention').click()
  await page.locator('.personcard').getByRole('link', { name: msg('papersByPerson') }).click()
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}/papers\\?place=all&creator=${id}$`))
  await expect(page.locator('.filters .mention')).toContainText(`@${DEV}`)
  await expect(page.locator('.dbt tbody tr', { hasText: mine.title })).toBeVisible()
  await expect(page.locator('.dbt tbody tr', { hasText: paper.title })).toBeVisible()
})

test('T16 a person property keeps the people chosen in a cell', async ({ page, request }) => {
  const db = await call<Paper>(request, 'POST', `/workspaces/${WS}/papers`, { title: unique('Owners'), parent: await homeOf(request), kind: 'database' })
  const res = await call<{ refs: Record<string, string> }>(request, 'POST', `/workspaces/${WS}/papers/${db.id}/ops`, { ops: [
    { op: 'insert_item', title: 'Search', ref: 'i1' },
  ] })
  await openPaper(page, db.id)

  await page.getByRole('button', { name: msg('addProperty') }).click()
  await page.getByRole('textbox', { name: msg('propertyName') }).fill('Owner')
  await page.getByRole('radio', { name: msg('propPerson'), exact: true }).click()
  await page.getByRole('button', { name: msg('addAction'), exact: true }).click()
  await expect(page.locator('.dbt thead th button', { hasText: 'Owner' })).toBeVisible()
  await synced(page)

  // a person who is not a user, set by an agent, then the dev user from the cell
  const owner = (await paperOf(request, db.id)).properties?.find((p) => p.name === 'Owner')
  expect(owner?.type).toBe('person')
  await call(request, 'POST', `/workspaces/${WS}/papers/${db.id}/ops`, { ops: [
    { op: 'set_values', item: res.refs.i1, set: { [owner?.id ?? '']: [ALEX] } },
  ] })
  const cell = page.locator('.dbt tbody tr[data-item-id]').nth(0).locator('td').nth(1).locator('button.cellbtn')
  await expect(cell.locator('.mention')).toHaveText(`@${ALEX.name}`)

  await cell.click({ position: { x: 4, y: 4 } })
  await page.locator('.dbpop [role="option"]', { hasText: DEV }).click()
  await expect(page.locator('.dbpop .tagin .mention')).toHaveCount(2)
  await page.locator('.dbpop .tagin .mention', { hasText: ALEX.name }).getByRole('button', { name: msg('unchoose') }).click()
  await page.keyboard.press('Escape')
  await expect(cell.locator('.mention')).toHaveText([`@${DEV}`])
  await synced(page)

  await page.reload()
  await expect(cell.locator('.mention')).toHaveText([`@${DEV}`])
})
