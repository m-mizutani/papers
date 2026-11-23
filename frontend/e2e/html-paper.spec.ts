import { expect, test } from '@playwright/test'
import { call, createPaper, eventually, homeOf, msg, openPaper, ops, paperOf, titleField, unique, OTHER_WS, WS } from './helpers'

// HTML papers: the whole body is one document written through the API,
// shown in a sandboxed frame where its scripts run. People only view it.

interface HtmlDoc { block_id: string; rev: number; attachment_id: string; html: string }
const docOf = (request: Parameters<typeof createPaper>[0], paper: string, ws = WS) =>
  call<HtmlDoc>(request, 'GET', `/workspaces/${ws}/papers/${paper}/html`)

const frameOf = (page: import('@playwright/test').Page) => page.frameLocator('.htmlpage iframe')

test('T40 T42 T28 an HTML paper made through the API shows its document and runs its scripts, with nothing to edit', async ({ page, request }) => {
  const html = '<!doctype html><html><body><h1>Weekly incidents</h1><p id="out">waiting</p><script>document.getElementById("out").textContent = "ran"</script></body></html>'
  const paper = await createPaper(request, { title: unique('Weekly'), kind: 'html', html })
  expect(paper.kind).toBe('html')
  await page.goto(`/ws/${WS}/p/${paper.id}`)
  await expect(titleField(page)).toBeVisible()

  await expect(frameOf(page).locator('h1')).toHaveText('Weekly incidents')
  await expect(frameOf(page).locator('#out')).toHaveText('ran')
  await expect(page.locator('.meta .kind-badge')).toHaveText(msg('htmlBadge'))

  // nothing in the body is editable, the title is
  await expect(page.locator('.blocks')).toHaveCount(0)
  await expect(page.getByRole('button', { name: msg('clickToAdd') })).toHaveCount(0)
  await titleField(page).fill('Weekly report')
  await titleField(page).press('Enter')
  await eventually(() => paperOf(request, paper.id), (p) => p.title === 'Weekly report', 'title saved')
  await expect(page.locator('.blocks')).toHaveCount(0)
})

test('T41 a document replaced through the API shows at once', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Live'), kind: 'html', html: '<p id="v">one</p>' })
  await openPaper(page, paper.id)
  await expect(frameOf(page).locator('#v')).toHaveText('one')
  const doc = await docOf(request, paper.id)
  await ops(request, paper.id, [{ op: 'set_html', html: '<p id="v">two</p>', base_rev: doc.rev }])
  await expect(frameOf(page).locator('#v')).toHaveText('two')
})

test('T49 the document cannot reach the page, its cookies or the API', async ({ page, request, baseURL }) => {
  const html = `<p id="r">…</p><script>(async function(){
    var r = {}
    try { void parent.document.title; r.parent = 'reachable' } catch (e) { r.parent = 'blocked' }
    try { void document.cookie; r.cookie = 'readable' } catch (e) { r.cookie = 'blocked' }
    try { var res = await fetch(${JSON.stringify(`${baseURL}/api/v1/workspaces`)}, { credentials: 'include' }); await res.text(); r.api = 'read' } catch (e) { r.api = 'blocked' }
    document.getElementById('r').textContent = JSON.stringify(r)
  })()</script>`
  const paper = await createPaper(request, { title: unique('Sandbox'), kind: 'html', html })
  await openPaper(page, paper.id)
  await expect(frameOf(page).locator('#r')).toHaveText(JSON.stringify({ parent: 'blocked', cookie: 'blocked', api: 'blocked' }))
})

test('T57 a workspace without external resources keeps the document to what it embeds', async ({ page, request, baseURL }) => {
  // the example "security" workspace sets [sandbox] external_resources = false
  const html = (ws: string) => `<p id="r">…</p><p id="ws">${ws}</p><script>
    var blocked = false
    document.addEventListener('securitypolicyviolation', function () { blocked = true })
    var img = new Image()
    img.onload = img.onerror = function () { document.getElementById('r').textContent = blocked ? 'blocked' : 'allowed' }
    img.src = ${JSON.stringify(`${baseURL}/healthz?img`)}
  </script>`
  const closed = await createPaper(request, { title: unique('Closed'), kind: 'html', html: html('closed'), parent: await homeOf(request, OTHER_WS) }, OTHER_WS)
  await openPaper(page, closed.id, OTHER_WS)
  await expect(frameOf(page).locator('#r')).toHaveText('blocked') // the inline script still ran

  const open = await createPaper(request, { title: unique('Open'), kind: 'html', html: html('open') })
  await openPaper(page, open.id)
  await expect(frameOf(page).locator('#r')).toHaveText('allowed')
})

test('T36 the parent shows an HTML child, and the menu opens the HTML instead of exporting Markdown', async ({ page, request }) => {
  const parent = await createPaper(request, { title: unique('Reports') })
  const child = await createPaper(request, { title: unique('Q3'), kind: 'html', html: '<p>numbers</p>', parent: parent.id })
  await openPaper(page, parent.id)
  const card = page.locator('.child', { hasText: child.title })
  await expect(card.locator('.pi')).toHaveText('</>')
  await expect(card.locator('.cs')).toContainText(msg('htmlBadge'))

  await card.click()
  await expect(frameOf(page).locator('p')).toHaveText('numbers')
  await page.getByRole('button', { name: msg('more'), exact: true }).click()
  await expect(page.getByRole('menuitem', { name: msg('exportMarkdown') })).toHaveCount(0)
  const popup = page.waitForEvent('popup')
  await page.getByRole('menuitem', { name: msg('openHtml') }).click()
  const opened = await popup
  await expect(opened.locator('p')).toHaveText('numbers')
  await opened.close()
})

test('T29 a saved version shows the document it had', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Versioned'), kind: 'html', html: '<p id="v">first</p>' })
  await call(request, 'POST', `/workspaces/${WS}/papers/${paper.id}/versions`, { note: '' })
  const doc = await docOf(request, paper.id)
  await ops(request, paper.id, [{ op: 'set_html', html: '<p id="v">second</p>', base_rev: doc.rev }])
  await page.goto(`/ws/${WS}/p/${paper.id}/v/1`)
  await expect(frameOf(page).locator('#v')).toHaveText('first')
  // the banner offers only the way back to the latest
  const banner = page.locator('.banner.ver')
  await expect(banner.getByRole('button')).toHaveCount(0)
  await expect(banner.getByRole('link')).toHaveText([msg('showLatest')])
  await banner.getByRole('link', { name: msg('showLatest') }).click()
  await expect(frameOf(page).locator('#v')).toHaveText('second')
})
