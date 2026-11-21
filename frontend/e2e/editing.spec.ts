import { expect, test } from '@playwright/test'
import { answerDialogs, blocks, blocksOf, createPaper, editor, eventually, finishEditing, msg, openPaper, paperOf, unique } from './helpers'

test('creates a paper from the sidebar and renames it', async ({ page, request }) => {
  const title = unique('Fresh')
  await page.goto('/ws/product')
  answerDialogs(page, [title])
  await page.getByRole('button', { name: `+ ${msg('newPaper')}` }).click()
  await expect(page).toHaveURL(/\/ws\/product\/p\//)
  const id = page.url().split('/p/')[1] ?? ''
  await expect(page.getByLabel(msg('paperTitle'))).toHaveValue(title)
  await expect(page.locator('.badge', { hasText: msg('statusDraft') })).toBeVisible()

  const renamed = unique('Renamed')
  await page.getByLabel(msg('paperTitle')).fill(renamed)
  await page.getByLabel(msg('paperTitle')).press('Enter')
  await eventually(() => paperOf(request, id), (p) => p.title === renamed, 'title saved')
  await expect(page.getByRole('navigation', { name: msg('location') })).toContainText(renamed)
})

test('writes blocks, splits them with Enter and formats text', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Writing') })
  await openPaper(page, paper.id)
  await expect(page.getByText(msg('emptyPaper'))).toBeVisible()

  await page.getByRole('button', { name: `+ ${msg('addBlock')}` }).click()
  await expect(editor(page)).toBeFocused()
  await page.keyboard.type('first line')
  await page.keyboard.press('Enter')
  await page.keyboard.type('second line')
  await finishEditing(page)
  await expect(blocks(page)).toHaveCount(2)
  await eventually(() => blocksOf(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'first line|second line', 'both blocks saved')

  // bold, then a text colour, on the whole first block
  await blocks(page).nth(0).click()
  await expect(editor(page)).toBeFocused()
  await page.keyboard.press('ControlOrMeta+a')
  await page.getByRole('button', { name: msg('bold'), exact: true }).click()
  await page.getByRole('button', { name: msg('textColor', { color: 'red' }), exact: true }).click()
  await finishEditing(page)
  await expect(blocks(page).nth(0).locator('strong')).toHaveText('first line')
  await expect(blocks(page).nth(0).locator('.mk-c-red')).toBeVisible()
  const saved = await eventually(() => blocksOf(request, paper.id), (bs) => (bs[0]?.content ?? '').includes('{.red}'), 'formatting saved')
  expect(saved[0]?.content).toContain('**')
})

test('changes block types, reorders, checks a to-do and deletes blocks', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Types'), markdown: 'one\n\ntwo\n\nthree' })
  await openPaper(page, paper.id)
  await expect(blocks(page)).toHaveCount(3)

  // "two" becomes a heading
  await blocks(page).nth(1).hover()
  await blocks(page).nth(1).getByLabel(msg('blockType')).selectOption('h2')
  await expect(page.locator('h2.b-heading-2')).toHaveText('two')

  // "three" moves above "two"
  await blocks(page).nth(2).hover()
  await blocks(page).nth(2).getByRole('button', { name: msg('moveUp') }).click()
  await expect(blocks(page).nth(1)).toContainText('three')

  // "one" becomes a to-do and is checked
  await blocks(page).nth(0).hover()
  await blocks(page).nth(0).getByLabel(msg('blockType')).selectOption('todo')
  await page.locator('.b-todo-box').click()
  await expect(page.locator('.b-todo-box')).toBeChecked()
  await expect(page.locator('.b-todo-done')).toBeVisible()

  // "three" is deleted
  await blocks(page).nth(1).hover()
  await blocks(page).nth(1).getByRole('button', { name: msg('deleteBlock') }).click()
  await expect(blocks(page)).toHaveCount(2)

  const saved = await eventually(() => blocksOf(request, paper.id), (bs) => bs.length === 2, 'two blocks left')
  expect(saved.map((b) => b.type)).toEqual(['todo', 'heading'])
  expect(saved[0]?.props.checked).toBe(true)
  expect(saved[1]?.props.level).toBe(2)
})

test('keeps blocks written in Markdown and exports them', async ({ page, request }) => {
  const title = unique('Export')
  const paper = await createPaper(request, { title, markdown: '# Steps\n\n1. check\n2. fix\n\n```sh\nmake\n```' })
  await openPaper(page, paper.id)
  await expect(page.locator('.b-list-marker').first()).toHaveText('1.')
  await expect(page.locator('pre.b-code')).toContainText('make')

  const download = page.waitForEvent('download')
  await page.getByRole('link', { name: msg('exportMarkdown') }).click()
  const file = await download
  const text = await (await import('node:fs/promises')).readFile(await file.path(), 'utf8')
  expect(text).toContain(`# ${title}`)
  expect(text).toContain('1. check')
  expect(text).toContain('```sh')
})

test('prints the whole paper', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Print'), markdown: 'printed text' })
  await page.addInitScript(() => {
    window.print = () => { (window as unknown as { printed: boolean }).printed = true }
  })
  await openPaper(page, paper.id)
  await page.getByRole('button', { name: msg('print') }).click()
  await expect.poll(() => page.evaluate(() => (window as unknown as { printed?: boolean }).printed === true)).toBe(true)
})
