import { expect, test } from '@playwright/test'
import {
  blocks, body, call, createPaper, drag, editBlock, editor, eventually, finishEditing, msg, openPaper, paperOf, synced, titleField, topBlocks, unique, WS,
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

test('the title is sent while typing, without leaving the field', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Before') })
  await call(request, 'PUT', `/workspaces/${WS}/papers/${paper.id}/favorite`, { after: null })
  await openPaper(page, paper.id)
  const title = unique('Typed')
  await titleField(page).fill(title)
  await expect(titleField(page)).toBeFocused()
  // the breadcrumb follows at once; the server and the sidebar after a pause
  await expect(page.locator('.crumbs .crumb.cur')).toHaveText(title)
  await expect.poll(async () => (await paperOf(request, paper.id)).title).toBe(title)
  await expect(page.locator('.sb').getByText(title)).not.toHaveCount(0)
  await expect(titleField(page)).toBeFocused()

  // the sends of one focus are one undo step back to the title before it
  await titleField(page).press('End')
  await titleField(page).pressSequentially(' more', { delay: 50 })
  await expect.poll(async () => (await paperOf(request, paper.id)).title).toBe(`${title} more`)
  await titleField(page).blur()
  await synced(page)
  await page.keyboard.press('ControlOrMeta+z')
  await expect.poll(async () => (await paperOf(request, paper.id)).title).toBe(paper.title)
  await expect(titleField(page)).toHaveValue(paper.title)
})

test('a title typed back to what it was is sent even while the earlier send is on its way', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Back') })
  await openPaper(page, paper.id)
  // hold the first title send until the second one has been decided
  let release: () => void = () => undefined
  const held = new Promise<void>((r) => { release = r })
  let first = true
  await page.route(`**/papers/${paper.id}/ops`, async (route) => {
    if (first) {
      first = false
      await held
    }
    await route.continue()
  })
  await titleField(page).click()
  await titleField(page).press('End')
  await titleField(page).pressSequentially('X')
  await page.waitForTimeout(1000)
  await titleField(page).press('Backspace')
  await page.waitForTimeout(1000)
  release()
  // still in the field: the server ends with the title shown, not the held one
  await synced(page)
  await expect(titleField(page)).toBeFocused()
  await expect.poll(async () => (await paperOf(request, paper.id)).title).toBe(paper.title)
})

test('T59 typing and Enter keep every line shown while the server answers', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Steady'), markdown: 'first' })
  await openPaper(page, paper.id)
  // A slow server: the change feed and the answer both come well after the keys.
  await page.route('**/ops', async (route) => {
    await new Promise((r) => setTimeout(r, 500))
    await route.continue()
  })
  // Every state the blocks pass through, so a flicker between two checks is caught.
  await page.evaluate(() => {
    const w = window as unknown as { shownStates: string[] }
    w.shownStates = []
    const root = document.querySelector('.blocks')
    if (!root) throw new Error('no blocks')
    const snap = () => [...root.querySelectorAll(':scope > .blk')]
      .map((b) => `${b.classList.contains('editing') ? '*' : ''}${b.classList.contains('flash') ? '!' : ''}${(b.querySelector('.bd') as HTMLElement | null)?.innerText.trim() ?? ''}`).join('|')
    new MutationObserver(() => w.shownStates.push(snap())).observe(root, { subtree: true, childList: true, characterData: true, attributes: true })
  })

  await editBlock(page, 0)
  await page.keyboard.type(' typed')
  await page.keyboard.press('Enter')
  await page.keyboard.type('second')
  await page.keyboard.press('Enter')
  await page.keyboard.type('third')
  await finishEditing(page)
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'first typed|second|third', 'saved')
  await expect(blocks(page).locator('.bd')).toHaveText(['first typed', 'second', 'third'])

  const states = await page.evaluate(() => (window as unknown as { shownStates: string[] }).shownStates)
  for (const s of states) {
    const lines = s.split('|')
    // a block that is not being edited shows the whole text typed into it
    const left = lines.filter((l) => !l.startsWith('*')).map((l) => l.replace(/^!/, ''))
    expect(left.filter((l) => !['first', 'first typed', 'second', 'third'].includes(l)), s).toEqual([])
    if (lines.length > 1) expect(lines[0], s).not.toBe('first')
    // no line shows twice, and my own lines do not flash as someone else's change
    expect(lines.length, s).toBeLessThanOrEqual(3)
    expect(new Set(left).size, s).toBe(left.length)
    expect(lines.filter((l) => l.startsWith('!') || l.startsWith('*!')), s).toEqual([])
  }
})

test('T60 going back into a block right after Enter keeps editing it without a conflict', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Back'), markdown: 'first' })
  await openPaper(page, paper.id)
  await page.route('**/ops', async (route) => {
    await new Promise((r) => setTimeout(r, 500))
    await route.continue()
  })
  await editBlock(page, 0)
  await page.keyboard.type(' typed')
  await page.keyboard.press('Enter')
  await page.keyboard.press('ArrowUp')
  await expect(blocks(page).nth(0)).toHaveClass(/editing/)
  await expect(editor(page)).toHaveText('first typed')
  await page.keyboard.press('End')
  await page.keyboard.type('!')
  await page.waitForTimeout(1500)
  await expect(page.locator('.conf')).toHaveCount(0)
  await finishEditing(page)
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((b) => b.content).join('|') === 'first typed!|', 'saved')
})
