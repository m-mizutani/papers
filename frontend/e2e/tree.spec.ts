import { expect, test } from '@playwright/test'
import { answerDialogs, blocks, blocksOf, call, createPaper, eventually, menu, msg, openPaper, OTHER_WS, paperOf, unique, WS } from './helpers'
import type { Paper, PaperRef } from '../src/api/types'

test('adds a child paper, opens it and deletes it with what is under it', async ({ page, request }) => {
  const parent = await createPaper(request, { title: unique('Parent') })
  const childTitle = unique('Child')
  await openPaper(page, parent.id)
  answerDialogs(page, [childTitle])
  await page.getByRole('button', { name: `+ ${msg('typeChildPaper')}` }).click()
  const link = page.locator('.b-paper', { hasText: childTitle })
  await expect(link).toBeVisible()
  await link.click()
  await expect(page.getByLabel(msg('paperTitle'))).toHaveValue(childTitle)
  const child = page.url().split('/p/')[1] ?? ''
  const grand = await createPaper(request, { title: unique('Grand'), parent: child })
  await expect(page.getByRole('navigation', { name: msg('location') }).getByRole('link', { name: parent.title })).toBeVisible()

  // deleting the paper block sends the child and its subtree to the trash
  await page.goBack()
  await expect(link).toBeVisible()
  const block = blocks(page).filter({ has: page.locator('.b-paper') })
  await block.hover()
  await block.getByRole('button', { name: msg('deleteBlock') }).click()
  await expect(link).toHaveCount(0)
  await eventually(() => paperOf(request, grand.id), (p) => p.deleted_with === child, 'subtree trashed')
})

test('moves a paper under another parent', async ({ page, request }) => {
  const from = await createPaper(request, { title: unique('From') })
  const to = await createPaper(request, { title: unique('To') })
  const paper = await createPaper(request, { title: unique('Moving'), parent: from.id })
  await openPaper(page, paper.id)
  answerDialogs(page, [to.id])
  await menu(page, 'move')
  await expect(page.getByRole('navigation', { name: msg('location') }).getByRole('link', { name: to.title })).toBeVisible()
  const kids = await call<{ papers: PaperRef[] }>(request, 'GET', `/workspaces/${WS}/papers/${to.id}/children`)
  expect(kids.papers.map((k) => k.id)).toContain(paper.id)
  expect((await blocksOf(request, from.id)).some((b) => b.props.paper_id === paper.id)).toBe(false)
})

test('moves a paper with its subtree to another workspace', async ({ page, request }) => {
  const root = await createPaper(request, { title: unique('Travelling'), markdown: 'carried along' })
  const kids: Paper[] = []
  for (let i = 0; i < 3; i++) kids.push(await createPaper(request, { title: `kid ${i}`, parent: root.id }))
  await openPaper(page, root.id)
  answerDialogs(page, [OTHER_WS])
  await menu(page, 'moveWorkspace')

  // when the move ends, the page goes to the copy
  await expect(page).toHaveURL(new RegExp(`/ws/${OTHER_WS}/p/`), { timeout: 20_000 })
  await expect(page.getByLabel(msg('paperTitle'))).toHaveValue(root.title)
  await expect(page.locator('.b-paper')).toHaveCount(3)
  await expect(blocks(page).filter({ hasText: 'carried along' })).toBeVisible()

  // the old address leads to the new place
  await page.goto(`/ws/${WS}/p/${root.id}`)
  await expect(page).toHaveURL(new RegExp(`/ws/${OTHER_WS}/p/`))
  await expect(page.getByLabel(msg('paperTitle'))).toHaveValue(root.title)
})

test('deletes a paper to the trash and restores it with its subtree', async ({ page, request }) => {
  const root = await createPaper(request, { title: unique('Doomed') })
  const kids: Paper[] = []
  for (let i = 0; i < 5; i++) kids.push(await createPaper(request, { title: `part ${i}`, parent: root.id }))
  await openPaper(page, root.id)
  const dialogs = answerDialogs(page)
  await menu(page, 'deletePaper')
  expect(dialogs[0]).toContain(root.title)
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}$`))
  await eventually(() => paperOf(request, kids[4]?.id ?? ''), (p) => !!p.deleted_at, 'subtree trashed')

  await page.getByRole('link', { name: msg('trash') }).click()
  const row = page.locator('.hits li', { hasText: root.title })
  await expect(row).toBeVisible()
  await expect(page.locator('.hits li', { hasText: 'part 0' })).toHaveCount(0) // only what was deleted directly
  await row.getByRole('button', { name: msg('restoreFromTrash') }).click()
  await expect(page.getByText(msg('restoredFromTrash'))).toBeVisible()
  await expect(row).toHaveCount(0)
  for (const k of kids) await eventually(() => paperOf(request, k.id), (p) => !p.deleted_at, 'restored')

  // a trashed paper opened directly offers to restore it
  await call(request, 'POST', `/workspaces/${WS}/papers/${root.id}/trash`)
  await page.goto(`/ws/${WS}/p/${root.id}`)
  await expect(page.getByText(msg('inTrash'))).toBeVisible()
  await page.locator('.notice').getByRole('button', { name: msg('restoreFromTrash') }).click()
  await expect(page.getByText(msg('inTrash'))).toHaveCount(0)
})

test('archives a paper with its subtree and brings it back', async ({ page, request }) => {
  const word = `archivable${Date.now().toString(36)}`
  const root = await createPaper(request, { title: unique('Old'), markdown: `${word} words` })
  const kid = await createPaper(request, { title: unique('Old kid'), parent: root.id })
  await openPaper(page, root.id)
  answerDialogs(page)
  await menu(page, 'archive')
  await expect(page.locator('.paper-bar .badge', { hasText: msg('statusArchived') })).toBeVisible()
  await eventually(() => paperOf(request, kid.id), (p) => !!p.archived_at, 'subtree archived')
  // archived papers leave search
  const hits = await call<{ hits: unknown[] }>(request, 'GET', `/workspaces/${WS}/search?q=${word}`)
  expect(hits.hits).toHaveLength(0)

  // unarchiving without the subtree brings back only the paper itself
  await menu(page, 'unarchive')
  await expect(page.locator('.paper-bar .badge', { hasText: msg('statusArchived') })).toHaveCount(0)
  await expect(page.locator('.b-paper', { hasText: kid.title }).locator('.badge', { hasText: msg('statusArchived') })).toBeVisible()
})

test('keeps a private paper out of the tree and makes it public', async ({ page, request }) => {
  const title = unique('Secret')
  await page.goto(`/ws/${WS}`)
  answerDialogs(page, [title, ''])
  await page.getByRole('button', { name: `+ ${msg('newPrivatePaper')}` }).click()
  await expect(page.locator('.badge', { hasText: msg('private') })).toBeVisible()
  const id = page.url().split('/p/')[1] ?? ''
  const nav = page.getByRole('navigation', { name: msg('navigation') })
  await expect(nav.getByRole('link', { name: `🔒 ${title}` })).toBeVisible()
  await expect(page.getByRole('button', { name: `+ ${msg('typeChildPaper')}` })).toHaveCount(0)

  await menu(page, 'makePublic')
  await expect(page.locator('.badge', { hasText: msg('private') })).toHaveCount(0)
  await expect(nav.getByRole('link', { name: `🔒 ${title}` })).toHaveCount(0)
  const p = await paperOf(request, id)
  expect(p.visibility).toBe('public')
  expect(p.parent_id).toBeTruthy()
})

test('pins a paper to the sidebar and unpins it', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Pinned') })
  await openPaper(page, paper.id)
  await menu(page, 'pin')
  const pins = page.getByRole('navigation', { name: msg('navigation') }).locator('section', { has: page.getByRole('heading', { name: msg('pins') }) })
  await expect(pins.getByRole('link', { name: paper.title })).toBeVisible()
  await pins.locator('li', { hasText: paper.title }).getByRole('button', { name: msg('unpin') }).click()
  await expect(pins.getByRole('link', { name: paper.title })).toHaveCount(0)
})

test('finds papers by their words', async ({ page, request }) => {
  const word = `zebra${Date.now().toString(36)}`
  const paper = await createPaper(request, { title: unique('Findable'), markdown: `the ${word} crossed` })
  await page.goto(`/ws/${WS}`)
  await page.getByLabel(msg('searchPlaceholder')).fill(word)
  await page.getByLabel(msg('searchPlaceholder')).press('Enter')
  await expect(page.getByRole('heading', { name: msg('searchResults', { query: word }) })).toBeVisible()
  const hit = page.locator('.hits li', { hasText: paper.title })
  await expect(hit).toContainText(word)
  await hit.getByRole('link').click()
  await expect(page).toHaveURL(new RegExp(`/p/${paper.id}$`))

  const none = `nothing${Date.now()}`
  await page.goto(`/ws/${WS}/search?q=${none}`)
  await expect(page.getByText(msg('noResults', { query: none }))).toBeVisible()
})
