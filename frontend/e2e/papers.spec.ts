import { expect, test, type Page } from '@playwright/test'
import { call, createPaper, homeOf, menu, msg, openPaper, paperOf, titleField, unique, waitLive, WS } from './helpers'
import type { Sidebar } from '../src/api/types'

const sidebarOf = (request: Parameters<typeof createPaper>[0]) => call<Sidebar>(request, 'GET', `/workspaces/${WS}/sidebar`)
const rows = (page: Page) => page.locator('.dbt tbody tr')
const filter = (page: Page, group: string, option: string) => page.getByRole('group', { name: group }).getByRole('button', { name: option, exact: true }).click()

test('T32 the papers list filters by place, author, visibility and archived', async ({ page, request }) => {
  const tag = unique('Listed')
  const loose = await createPaper(request, { title: `${tag} loose`, unfiled: true })
  const filed = await createPaper(request, { title: `${tag} filed` })
  const secret = await createPaper(request, { title: `${tag} secret`, visibility: 'private' })
  const old = await createPaper(request, { title: `${tag} old`, unfiled: true })
  await call(request, 'POST', `/workspaces/${WS}/papers/${old.id}/archive`, { subtree: true })

  await page.goto(`/ws/${WS}/papers`)
  const mine = rows(page).filter({ hasText: tag })
  // by default: my unfiled papers (private ones too), without archived ones
  await expect(mine).toHaveCount(2)
  await expect(mine.filter({ hasText: loose.title })).toContainText(msg('unfiled'))
  await expect(mine.filter({ hasText: secret.title })).toContainText(msg('private'))

  await filter(page, msg('place'), msg('filed'))
  await expect(mine).toHaveCount(1)
  await expect(mine).toContainText(filed.title)

  await filter(page, msg('place'), msg('all'))
  await expect(mine).toHaveCount(3)

  await filter(page, msg('visibilityLabel'), msg('private'))
  await expect(mine).toHaveCount(1)
  await expect(mine).toContainText(secret.title)
  await filter(page, msg('visibilityLabel'), msg('publicLabel'))
  await expect(mine).toHaveCount(2)

  await page.getByLabel(msg('includeArchived')).check()
  await expect(mine).toHaveCount(3)
  await expect(mine.filter({ hasText: old.title })).toContainText(msg('archived'))
  await page.getByLabel(msg('includeArchived')).uncheck()
  await expect(mine).toHaveCount(2)

  // Sign-in is off, so every writer is the same person: "Everyone" keeps
  // what "Me" shows (and asks the server for every creator).
  const asked = page.waitForRequest((r) => r.url().includes('/papers?') && r.url().includes('creator=all'))
  await filter(page, msg('author'), msg('everyone'))
  await asked
  await expect(mine).toHaveCount(2)
  await expect(page.getByRole('group', { name: msg('author') }).getByRole('button', { name: msg('everyone') })).toHaveAttribute('aria-pressed', 'true')

  // a filter that matches nothing shows the empty state
  await filter(page, msg('place'), msg('filed'))
  await filter(page, msg('visibilityLabel'), msg('private'))
  await expect(page.getByText(msg('noneMatch'))).toBeVisible()
  await expect(rows(page)).toHaveCount(0)

  // the choice is kept for the next visit
  await page.reload()
  await expect(page.getByRole('group', { name: msg('visibilityLabel') }).getByRole('button', { name: msg('private') })).toHaveAttribute('aria-pressed', 'true')
})

test('T38 a new paper starts unfiled; moving it files it and the unfiled count goes down', async ({ page, request }) => {
  const target = await createPaper(request, { title: unique('Target') })
  await page.goto(`/ws/${WS}/papers`)
  const before = (await sidebarOf(request)).unfiled_count
  expect(before).toBeLessThan(99)
  const badge = page.locator('.sb .sb-row .badge')

  await page.locator('.sb').getByRole('button', { name: msg('newPaper'), exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}/p/[^/]+$`))
  await waitLive(page)
  const id = page.url().split('/p/')[1] ?? ''
  await expect(page.locator('.crumbs .crumb').first()).toHaveText(msg('unfiled'))
  await expect(badge).toHaveText(String(before + 1))
  const title = unique('Loose')
  await titleField(page).fill(title)
  await titleField(page).press('Enter')

  await menu(page, 'moveEllipsis')
  const dialog = page.getByRole('dialog', { name: msg('moveTitle', { title }) })
  await dialog.getByLabel(msg('moveTo')).first().fill(target.title)
  await dialog.getByRole('option', { name: target.title }).click()
  await dialog.getByRole('button', { name: msg('move'), exact: true }).click()

  await expect(page.locator('.crumbs')).toContainText(target.title)
  await expect(page.locator('.crumbs .crumb').first()).not.toHaveText(msg('unfiled'))
  await expect(page.locator('.crumbs .crumb.cur')).toHaveText(title)
  await expect(badge).toHaveText(String(before))
  expect((await paperOf(request, id)).parent_id).toBe(target.id)

  await page.locator('.sb').getByRole('link', { name: msg('papers') }).click()
  await expect(page.locator('.dbt, .list .empty').first()).toBeVisible()
  await expect(rows(page).filter({ hasText: title })).toHaveCount(0)
})

test('T42 the star makes a paper a favorite in the sidebar', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Fav') })
  await openPaper(page, paper.id)
  const star = page.getByRole('button', { name: msg('addFavorite') })
  await expect(star).toHaveAttribute('aria-pressed', 'false')
  await star.click()
  const on = page.locator('.top .star')
  await expect(on).toHaveAttribute('aria-pressed', 'true')
  await expect(on).toHaveClass(/\bon\b/)
  await expect(on).toHaveAccessibleName(msg('removeFavorite'))
  await expect(on.locator('path')).toHaveAttribute('fill', 'currentColor')
  const favs = page.locator('.sb-sec').filter({ has: page.locator('.sb-h', { hasText: msg('favorites') }) })
  await expect(favs.getByRole('link', { name: paper.title })).toBeVisible()
})

test('T46 Mod+K finds a word in a block and opens the paper at that block', async ({ page, request }) => {
  const word = `zebra${Date.now().toString(36)}`
  const filler = Array.from({ length: 60 }, (_, i) => `filler paragraph ${i}`)
  filler.splice(50, 0, `the ${word} lives here`)
  const paper = await createPaper(request, { title: unique('Search'), markdown: filler.join('\n\n') })
  const home = await homeOf(request)
  await page.goto(`/ws/${WS}`)
  await waitLive(page)
  expect(home).toBeTruthy()

  await page.keyboard.press('ControlOrMeta+k')
  const pal = page.getByRole('dialog', { name: msg('search') })
  await expect(pal).toBeVisible()
  await pal.getByRole('textbox', { name: msg('search') }).fill(word)
  await expect(pal.locator('.pr').first()).toContainText(paper.title)
  await expect(pal.locator('.pr').first()).toContainText(word)
  await page.keyboard.press('Enter')

  await expect(page).toHaveURL(new RegExp(`/p/${paper.id}#.+`))
  const hit = page.locator('.blk', { hasText: word })
  await expect(hit).toHaveClass(/flash/)
  await expect(hit).toBeInViewport()
  expect(page.url().split('#')[1]).toBe(await hit.getAttribute('data-block-id'))
})

test('T47 an unfiled paper restored from the trash is unfiled again', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Trashed'), unfiled: true })
  await openPaper(page, paper.id)
  await menu(page, 'moveToTrash')
  await page.getByRole('dialog').getByRole('button', { name: msg('moveToTrash') }).click()
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}/papers$`))

  await page.locator('.sb').getByRole('link', { name: msg('trash'), exact: true }).click()
  const row = page.locator('.lr', { hasText: paper.title })
  await row.getByRole('button', { name: msg('restoreFromTrash') }).click()
  await expect(page.locator('.toast', { hasText: msg('restoredDone') })).toBeVisible()
  await expect(row).toHaveCount(0)

  const p = await paperOf(request, paper.id)
  expect(p.parent_id ?? '').toBe('')
  expect(p.visibility).toBe('public')
  expect(p.deleted_at ?? '').toBe('')
  await openPaper(page, paper.id)
  await expect(page.locator('.crumbs .crumb').first()).toHaveText(msg('unfiled'))
})

test.describe('T51 at phone width', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  const noSideScroll = (page: Page) => page.evaluate(() => {
    const d = document.documentElement
    const main = document.querySelector('.main .scroll')
    return {
      page: d.scrollWidth <= d.clientWidth,
      body: document.body.scrollWidth <= document.body.clientWidth,
      main: !main || main.scrollWidth <= main.clientWidth,
    }
  })

  test('T51 the paper and the papers list do not scroll sideways, and block handles show', async ({ page, request }) => {
    const paper = await createPaper(request, { title: unique('Narrow paper with a fairly long title for a phone'), markdown: '# Heading\n\nA paragraph with enough words to wrap over several lines on a narrow phone screen.\n\n- a list item\n\n```\ncode\n```' })
    await openPaper(page, paper.id)
    await expect(page.locator('.blk')).toHaveCount(4)
    expect(await noSideScroll(page)).toEqual({ page: true, body: true, main: true })
    const handle = page.locator('.blk').first().locator('.hdl')
    await expect(handle).toBeVisible()
    await expect(page.locator('.blk').first().locator('.g')).toHaveCSS('opacity', '1')

    await page.goto(`/ws/${WS}/papers`)
    await page.getByRole('group', { name: msg('place') }).getByRole('button', { name: msg('all'), exact: true }).click()
    await expect(rows(page).first()).toBeVisible()
    expect(await noSideScroll(page)).toEqual({ page: true, body: true, main: true })
  })
})

test('T56 / opens the last workspace, or the chooser; "Continue" opens the last paper', async ({ browser, request }) => {
  const paper = await createPaper(request, { title: unique('Continue') })
  const ctx = await browser.newContext({ locale: 'en-US' })
  const page = await ctx.newPage()

  // nothing opened in this browser and two workspaces: the chooser
  await page.goto('/')
  await expect(page).toHaveURL(/\/workspaces$/)
  await expect(page.locator('.wcard')).toHaveCount(2)

  await page.locator('.wcard').filter({ hasText: 'Product Notes' }).locator('.wmain').click()
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}$`))
  await waitLive(page)
  // afterwards / goes to that workspace's home
  await page.goto('/')
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}$`))
  await waitLive(page)

  // the card's "Continue" opens the paper opened last
  await openPaper(page, paper.id)
  await page.goto('/workspaces')
  const last = page.locator('.wcard').filter({ hasText: 'Product Notes' }).locator('a.wlast')
  await expect(last).toContainText(paper.title)
  await expect(last).toContainText(msg('continue'))
  await last.click()
  await expect(page).toHaveURL(new RegExp(`/ws/${WS}/p/${paper.id}$`))
  await waitLive(page)
  await expect(titleField(page)).toHaveValue(paper.title)
  await ctx.close()
})
