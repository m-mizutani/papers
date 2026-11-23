import { expect, test } from '@playwright/test'
import {
  blocks, body, createPaper, drag, editBlock, editor, eventually, finishEditing, msg, openPaper, paperOf, synced, titleField, topBlocks, unique, WS,
} from './helpers'
import type { Block } from '../src/api/types'

test('T1 Markdown shortcuts turn an empty paragraph into blocks', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Shortcuts') })
  await openPaper(page, paper.id)
  const add = page.getByRole('button', { name: msg('clickToAdd') })

  const cases: { type: string; cls: RegExp; text?: string }[] = [
    { type: '# Heading', cls: /t-heading1/, text: 'Heading' },
    { type: '- bullet', cls: /t-bulleted_list_item/, text: 'bullet' },
    { type: '1. numbered', cls: /t-numbered_list_item/, text: 'numbered' },
    { type: '[] todo', cls: /t-todo/, text: 'todo' },
    { type: '> quote', cls: /t-quote/, text: 'quote' },
  ]
  for (const [i, c] of cases.entries()) {
    await add.click()
    await expect(editor(page)).toBeFocused()
    await page.keyboard.type(c.type)
    await expect(blocks(page).nth(i)).toHaveClass(c.cls)
    await expect(editor(page)).toHaveText(c.text ?? '')
    await finishEditing(page)
    await expect(blocks(page).nth(i).locator('.txt')).toHaveText(c.text ?? '')
  }

  // ``` makes a code block, edited as plain text
  await add.click()
  await page.keyboard.type('```')
  await expect(blocks(page).nth(5)).toHaveClass(/t-code/)
  const code = page.getByLabel(msg('codeText'))
  await expect(code).toBeFocused()
  await expect(code).toHaveValue('')
  await page.keyboard.type('let x = 1')
  await code.press('Escape')
  await synced(page)

  // --- makes a divider and goes on in a new paragraph below it
  await add.click()
  await page.keyboard.type('---')
  await expect(blocks(page).nth(6)).toHaveClass(/t-divider/)
  await expect(editor(page)).toBeFocused()
  await expect(blocks(page).nth(7)).toHaveClass(/editing/)
  await page.keyboard.type('after')
  await finishEditing(page)

  const got = await eventually(() => topBlocks(request, paper.id), (bs) => bs.length === 8 && bs[7]?.content === 'after', 'blocks saved')
  expect(got.map((b) => [b.type, b.content])).toEqual([
    ['heading', 'Heading'],
    ['bulleted_list_item', 'bullet'],
    ['numbered_list_item', 'numbered'],
    ['todo', 'todo'],
    ['quote', 'quote'],
    ['code', 'let x = 1'],
    ['divider', ''],
    ['paragraph', 'after'],
  ])
  expect(got[0]?.props.level).toBe(1)
})

test('T2 Enter splits a paragraph and Backspace joins it again', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Split'), markdown: 'HelloWorld' })
  await openPaper(page, paper.id)
  await editBlock(page, 0)
  for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowLeft')
  await page.keyboard.press('Enter')

  await expect(blocks(page)).toHaveCount(2)
  await expect(body(page, 0)).toHaveText('Hello')
  await expect(blocks(page).nth(1)).toHaveClass(/editing/)
  await expect(editor(page)).toHaveText('World')
  await synced(page)
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'Hello|World', 'split saved')

  // the caret is at the start of "World": Backspace joins, and the caret stays at the seam
  await page.keyboard.press('Backspace')
  await expect(blocks(page)).toHaveCount(1)
  await expect(editor(page)).toHaveText('HelloWorld')
  await page.keyboard.type('_')
  await expect(editor(page)).toHaveText('Hello_World')
  await finishEditing(page)
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'Hello_World', 'join saved')
})

test('T3 Enter while an IME is composing does not split the block', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('IME'), markdown: 'かな' })
  await openPaper(page, paper.id)
  await editBlock(page, 0)

  // A real composition through the browser's IME interface...
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Input.imeSetComposition', { text: 'にほん', selectionStart: 3, selectionEnd: 3 })
  // ...and the Enter that confirms a conversion: browsers send it as a
  // keydown with isComposing (keyCode 229) while the composition is open.
  await editor(page).evaluate((el) => {
    el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 229, isComposing: true, bubbles: true, cancelable: true }))
  })
  await cdp.send('Input.insertText', { text: '日本' })
  await expect(editor(page)).toHaveText('かな日本')
  await expect(blocks(page)).toHaveCount(1)

  // once the composition is over, Enter splits as usual
  await page.keyboard.press('Enter')
  await expect(blocks(page)).toHaveCount(2)
  await finishEditing(page)
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'かな日本|', 'saved')
})

test('T4 colouring text from the format toolbar saves {.red} and survives a reload', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Colour'), markdown: 'make this red' })
  await openPaper(page, paper.id)
  await editBlock(page, 0)
  await page.keyboard.press('ControlOrMeta+a')
  const bar = page.getByRole('toolbar', { name: msg('formatting') })
  await expect(bar).toBeVisible()
  await bar.getByRole('button', { name: msg('color') }).click()
  await page.getByRole('button', { name: `${msg('textColor')} red` }).click()
  await expect(editor(page).locator('.mk-c-red')).toHaveText('make this red')
  // the first Escape closes the toolbar (the selection collapses), the next leaves the block
  await page.keyboard.press('Escape')
  await expect(bar).toHaveCount(0)
  await finishEditing(page)

  const [b] = await eventually(() => topBlocks(request, paper.id), (bs) => !!bs[0]?.content.includes('{.red}'), 'colour saved')
  expect(b?.content).toBe('[make this red]{.red}')
  await page.reload()
  await expect(body(page, 0).locator('.mk-c-red')).toHaveText('make this red')
})

test('T5 dragging a block handle reorders blocks', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Drag'), markdown: 'one\n\ntwo\n\nthree' })
  await openPaper(page, paper.id)
  await expect(blocks(page)).toHaveCount(3)
  const first = await blocks(page).nth(0).boundingBox()
  if (!first) throw new Error('no first block')
  await blocks(page).nth(2).hover()
  await drag(page, blocks(page).nth(2).locator('.hdl'), { x: first.x + first.width / 2, y: first.y + 3 })

  await expect(blocks(page).locator('.txt')).toHaveText(['three', 'one', 'two'])
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join(',') === 'three,one,two', 'order saved')
})

test('T6 "Insert below" → child paper creates and opens a child paper', async ({ page, request }) => {
  const parent = await createPaper(request, { title: unique('Parent'), markdown: 'anchor' })
  await openPaper(page, parent.id)
  // a click on the handle (without dragging) opens the block menu
  await blocks(page).nth(0).hover()
  await blocks(page).nth(0).locator('.hdl').click()
  await page.getByRole('menuitem', { name: msg('insertBelow') }).click()
  await expect(page.locator('.slashpop')).toBeVisible()
  await page.locator('.slashpop').getByRole('option', { name: msg('typeChildPaper') }).click()

  await expect(page).not.toHaveURL(new RegExp(`/p/${parent.id}$`))
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}/p/[^/]+$`))
  const child = page.url().split('/p/')[1] ?? ''
  await expect(titleField(page)).toBeFocused()
  await expect(page.locator('.crumbs')).toContainText(parent.title)
  const p = await paperOf(request, child)
  expect(p.parent_id).toBe(parent.id)
  const bs: Block[] = await topBlocks(request, parent.id)
  expect(bs.map((b) => b.type)).toEqual(['paragraph', 'paper'])
  expect(bs[1]?.props.paper_id).toBe(child)
})

test('T26 undo and redo step through typing, Enter and typing', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Undo'), markdown: 'start' })
  await openPaper(page, paper.id)
  await editBlock(page, 0)
  await page.keyboard.type(' A')
  // a pause sends the typing as one step
  await eventually(() => topBlocks(request, paper.id), (bs) => bs[0]?.content === 'start A', 'typing saved')
  await synced(page)
  await page.keyboard.press('Enter')
  await expect(blocks(page)).toHaveCount(2)
  await synced(page)
  await page.keyboard.type('B')
  await finishEditing(page)
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'start A|B', 'all saved')

  const texts = () => blocks(page).locator('.txt')
  const undo = async (want: string[]) => {
    await page.keyboard.press('ControlOrMeta+z')
    await expect(texts()).toHaveText(want)
    await synced(page)
  }
  await undo(['start A', ''])
  await undo(['start A'])
  await undo(['start'])
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'start', 'undone on the server')

  const redo = async (want: string[]) => {
    await page.keyboard.press('ControlOrMeta+Shift+z')
    await expect(texts()).toHaveText(want)
    await synced(page)
  }
  await redo(['start A'])
  await redo(['start A', ''])
  await redo(['start A', 'B'])
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'start A|B', 'redone on the server')
})
