import { expect, test, type Page } from '@playwright/test'
import {
  blocks, createPaper, drag, editor, eventually, msg, openPaper, saveVersion, synced, topBlocks, unique, WS,
} from './helpers'

const SIX = 'zero\n\none\n\ntwo\n\nthree\n\nfour\n\nfive'
const selected = (page: Page) => page.locator('.blocks > .blk.sel')

/** The middle of the n-th block's body. */
async function middle(page: Page, n: number): Promise<{ x: number; y: number; left: number }> {
  const box = await blocks(page).nth(n).locator('.bd').boundingBox()
  if (!box) throw new Error(`no block ${n}`)
  return { x: box.x + box.width / 2, y: box.y + box.height / 2, left: box.x }
}

/** Drags in the left margin, beside the column, from the height of one block to another's. */
async function marginDrag(page: Page, from: number, to: number): Promise<void> {
  const a = await middle(page, from)
  const b = await middle(page, to)
  const x = a.left - 60
  await page.mouse.move(x, a.y)
  await page.mouse.down()
  await page.mouse.move(x + 4, a.y + 8, { steps: 2 })
  await page.mouse.move(x + 10, b.y, { steps: 8 })
  await page.mouse.up()
}

const order = (bs: { content: string }[]) => bs.map((b) => b.content).join(',')

test('T64 dragging in the margin selects the blocks the band overlaps, without editing or selecting text', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Band'), markdown: SIX })
  await openPaper(page, paper.id)
  await marginDrag(page, 1, 3)
  await expect(selected(page).locator('.txt')).toHaveText(['one', 'two', 'three'])
  await expect(editor(page)).toHaveCount(0)
  expect(await page.evaluate(() => window.getSelection()?.toString() ?? '')).toBe('')
})

test('T65 a drag from below the add line selects without adding; a click there still adds', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Below'), markdown: 'a\n\nb\n\nc' })
  await openPaper(page, paper.id)
  const line = await page.locator('.addline').boundingBox()
  const first = await middle(page, 1)
  if (!line) throw new Error('no add line')
  await page.mouse.move(line.x + 20, line.y + line.height + 30)
  await page.mouse.down()
  await page.mouse.move(line.x + 24, line.y + line.height + 20, { steps: 2 })
  await page.mouse.move(line.x + 30, first.y, { steps: 8 })
  await page.mouse.up()
  await expect(selected(page).locator('.txt')).toHaveText(['b', 'c'])
  await expect(blocks(page)).toHaveCount(3)
  await page.mouse.click(line.x + 20, line.y + line.height + 30)
  await expect(blocks(page)).toHaveCount(4)
  await expect(selected(page)).toHaveCount(0)
})

test('T66 T67 a drag in the text of a block that leaves the block selects blocks; one inside it does not', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Text drag'), markdown: 'first line of text\n\nsecond line\n\nthird line\n\nfourth' })
  await openPaper(page, paper.id)
  const a = await middle(page, 0)
  // T67: within one block
  await page.mouse.move(a.left + 4, a.y)
  await page.mouse.down()
  await page.mouse.move(a.left + 60, a.y, { steps: 5 })
  await page.mouse.up()
  await expect(selected(page)).toHaveCount(0)
  await blocks(page).nth(1).locator('.txt').dblclick()
  await expect(selected(page)).toHaveCount(0)
  await editor(page).press('Escape')
  await synced(page)
  // T66: out of the block
  const c = await middle(page, 2)
  await page.mouse.move(a.left + 4, a.y)
  await page.mouse.down()
  await page.mouse.move(a.left + 20, c.y, { steps: 8 })
  await page.mouse.up()
  await expect(selected(page).locator('.txt')).toHaveText(['first line of text', 'second line', 'third line'])
  await expect(editor(page)).toHaveCount(0)
  expect(await page.evaluate(() => window.getSelection()?.toString() ?? '')).toBe('')
})

test('T70 ⇧↓ and ⇧↑ extend and shrink the selection; a plain arrow selects one block', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Shift'), markdown: SIX })
  await openPaper(page, paper.id)
  await marginDrag(page, 1, 1)
  await expect(selected(page)).toHaveCount(1)
  await page.keyboard.press('Shift+ArrowDown')
  await page.keyboard.press('Shift+ArrowDown')
  await page.keyboard.press('Shift+ArrowUp')
  await expect(selected(page).locator('.txt')).toHaveText(['one', 'two'])
  await page.keyboard.press('ArrowDown')
  await expect(selected(page).locator('.txt')).toHaveText(['three'])
})

test('T73 T79 dragging a selected handle moves the blocks in one batch; undo and redo move them all', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Group drag'), markdown: SIX })
  await openPaper(page, paper.id)
  await marginDrag(page, 1, 3)
  await expect(selected(page)).toHaveCount(3)
  const sends: string[] = []
  page.on('request', (r) => { if (r.method() === 'POST' && r.url().endsWith('/ops')) sends.push(r.url()) })
  const last = await blocks(page).nth(5).boundingBox()
  if (!last) throw new Error('no last block')
  await blocks(page).nth(2).hover()
  await drag(page, blocks(page).nth(2).locator('.hdl'), { x: last.x + last.width / 2, y: last.y + last.height - 2 })
  await expect(blocks(page).locator('.txt')).toHaveText(['zero', 'four', 'five', 'one', 'two', 'three'])
  await eventually(() => topBlocks(request, paper.id), (bs) => order(bs) === 'zero,four,five,one,two,three', 'order saved')
  expect(sends).toHaveLength(1)
  await expect(selected(page).locator('.txt')).toHaveText(['one', 'two', 'three'])

  await page.keyboard.press('ControlOrMeta+z')
  await eventually(() => topBlocks(request, paper.id), (bs) => order(bs) === 'zero,one,two,three,four,five', 'undone')
  await synced(page)
  await page.keyboard.press('ControlOrMeta+Shift+z')
  await eventually(() => topBlocks(request, paper.id), (bs) => order(bs) === 'zero,four,five,one,two,three', 'redone')
})

test('T74 ⌥↓ moves the selected blocks down one place', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Alt'), markdown: SIX })
  await openPaper(page, paper.id)
  await marginDrag(page, 1, 2)
  await page.keyboard.press('Alt+ArrowDown')
  await eventually(() => topBlocks(request, paper.id), (bs) => order(bs) === 'zero,three,one,two,four,five', 'moved down')
  await expect(selected(page).locator('.txt')).toHaveText(['one', 'two'])
})

test('T75 Delete removes the selected blocks together; undo brings them back', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Delete'), markdown: SIX })
  await openPaper(page, paper.id)
  await marginDrag(page, 1, 3)
  await page.keyboard.press('Delete')
  await expect(page.getByText(msg('blocksDeleted', { n: 3 }))).toBeVisible()
  await eventually(() => topBlocks(request, paper.id), (bs) => order(bs) === 'zero,four,five', 'deleted')
  await page.keyboard.press('ControlOrMeta+z')
  await eventually(() => topBlocks(request, paper.id), (bs) => order(bs) === 'zero,one,two,three,four,five', 'back')
})

test('T76 a selection with a child paper is not deleted', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('With child'), markdown: 'a\n\nb' })
  await createPaper(request, { title: unique('Child'), parent: paper.id })
  await openPaper(page, paper.id)
  await expect(blocks(page)).toHaveCount(3)
  await marginDrag(page, 1, 2)
  await expect(selected(page)).toHaveCount(2)
  await page.keyboard.press('Delete')
  await expect(page.getByText(msg('childPapersInSelection'))).toBeVisible()
  await expect(selected(page)).toHaveCount(2)
  await expect(blocks(page)).toHaveCount(3)
  expect((await topBlocks(request, paper.id)).length).toBe(3)
})

test('T80 Escape, a click in the margin and editing a block clear the selection', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Clear'), markdown: SIX })
  await openPaper(page, paper.id)
  await marginDrag(page, 1, 3)
  await expect(selected(page)).toHaveCount(3)
  await page.keyboard.press('Escape')
  await expect(selected(page)).toHaveCount(0)
  await marginDrag(page, 1, 3)
  const a = await middle(page, 4)
  await page.mouse.click(a.left - 60, a.y)
  await expect(selected(page)).toHaveCount(0)
  await marginDrag(page, 1, 3)
  await blocks(page).nth(2).locator('.txt').click()
  await expect(selected(page)).toHaveCount(0)
  await expect(editor(page)).toBeFocused()
})

test('T81 the handle of an unselected block drags only that block; a click on a selected handle opens its menu', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Other handle'), markdown: SIX })
  await openPaper(page, paper.id)
  await marginDrag(page, 1, 2)
  const top = await blocks(page).nth(0).boundingBox()
  if (!top) throw new Error('no first block')
  await blocks(page).nth(4).hover()
  await drag(page, blocks(page).nth(4).locator('.hdl'), { x: top.x + top.width / 2, y: top.y + 3 })
  await eventually(() => topBlocks(request, paper.id), (bs) => order(bs) === 'four,zero,one,two,three,five', 'one moved')
  await expect(selected(page)).toHaveCount(0)
  await synced(page)

  await marginDrag(page, 2, 3)
  await expect(selected(page)).toHaveCount(2)
  await blocks(page).nth(3).locator('.hdl').click()
  await expect(page.getByRole('menuitem', { name: msg('moveUp') })).toBeVisible()
  await expect(selected(page).locator('.txt')).toHaveText(['two'])
})

test('T82 a saved version shown read-only selects nothing', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Version'), markdown: SIX })
  await saveVersion(request, paper.id)
  await page.goto(`/ws/${WS}/p/${paper.id}/v/1`)
  await expect(blocks(page)).toHaveCount(6)
  await marginDrag(page, 1, 3)
  await expect(selected(page)).toHaveCount(0)
  await expect(page.locator('.blocks .hdl')).toHaveCount(0)
})
