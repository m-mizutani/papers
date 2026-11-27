import { expect, test, type APIRequestContext } from '@playwright/test'
import type { Paper } from '../src/api/types'
import { blocksOf, call, homeOf, msg, openPaper, unique, WS } from './helpers'

// HTML connected to a database: the document reads and writes the databases
// listed on it through papers.db, as the viewer, without any credential.

interface Seeded {
  db: Paper
  refs: Record<string, string>
}

async function seed(request: APIRequestContext): Promise<Seeded> {
  const db = await call<Paper>(request, 'POST', `/workspaces/${WS}/papers`, { title: unique('Roadmap'), parent: await homeOf(request), kind: 'database' })
  const res = await call<{ refs: Record<string, string> }>(request, 'POST', `/workspaces/${WS}/papers/${db.id}/ops`, { ops: [
    { op: 'add_property', name: 'Status', property_type: 'select', ref: 's' },
    { op: 'add_element', property: 'ref:s', value: 'Todo', ref: 'todo' },
    { op: 'add_element', property: 'ref:s', value: 'Done', ref: 'done' },
    { op: 'insert_item', title: 'Search', ref: 'i1', set: { 'ref:s': 'ref:todo' } },
  ] })
  return { db, refs: res.refs }
}

// A small document: lists items with their status, a button that marks the
// first item done, and a button that writes a note into its page.
const HTML = `<!doctype html><html><body>
<ul id="list"></ul>
<button id="done">Mark done</button>
<button id="note">Add note</button>
<button id="reload" onclick="location.reload()">Reload</button>
<p id="state">loading</p>
<script>
(async () => {
  const list = await papers.db.databases()
  const db = list[0]
  const status = db.paper.properties.find((p) => p.type === 'select')
  const name = (id) => (status.elements.find((e) => e.id === id) || {}).value || '-'
  let first = ''
  async function draw() {
    const page = await papers.db.items(db.id, { limit: 100 })
    const ul = document.getElementById('list')
    ul.replaceChildren()
    for (const r of page.items) {
      const v = (r.values.find((x) => x.property_id === status.id) || {}).value
      const li = document.createElement('li')
      li.textContent = r.item.title + ': ' + name(v)
      ul.append(li)
    }
    first = page.items[0].item.id
    document.getElementById('state').textContent = 'ready ' + (db.write ? 'write' : 'read')
  }
  papers.db.subscribe(db.id, () => { draw() })
  document.getElementById('done').onclick = () => papers.db.setValues(db.id, first, { [status.id]: status.elements[1].id })
  document.getElementById('note').onclick = () => papers.db.writeItem(first, [{ op: 'insert', type: 'paragraph', content: 'Written from the page' }])
  await draw()
})().catch((e) => { document.getElementById('state').textContent = 'error ' + (e && e.code) })
</script></body></html>`

async function connected(request: APIRequestContext, db: string, write: boolean): Promise<Paper> {
  return call<Paper>(request, 'POST', `/workspaces/${WS}/papers`, { title: unique('Board'), parent: await homeOf(request), kind: 'html', html: HTML, databases: [{ id: db, write }] })
}

const frameOf = (page: import('@playwright/test').Page) => page.frameLocator('.htmlpage iframe')

test('T20 an HTML paper shows its databases under the title and reads them', async ({ page, request }) => {
  const { db } = await seed(request)
  const html = await connected(request, db.id, true)
  await openPaper(page, html.id)

  const line = page.locator('.dbline')
  await expect(line).toContainText(msg('dbConnectedPage'))
  await expect(line.locator('.dbchip')).toContainText(db.title)
  await expect(line.locator('.dbchip')).toContainText(msg('dbReadWrite'))
  await expect(line.locator('.dbchip')).toHaveAttribute('href', `/ws/${WS}/p/${db.id}`)

  const frame = frameOf(page)
  await expect(frame.locator('#state')).toHaveText('ready write')
  await expect(frame.locator('#list li')).toHaveText(['Search: Todo'])
})

test('T21 a value changed in the frame is saved and shows in another page', async ({ page, browser, request }) => {
  const { db, refs } = await seed(request)
  const html = await connected(request, db.id, true)
  await openPaper(page, html.id)
  await expect(frameOf(page).locator('#state')).toHaveText('ready write')

  const other = await browser.newPage()
  await openPaper(other, html.id)
  await expect(frameOf(other).locator('#list li')).toHaveText(['Search: Todo'])

  await frameOf(page).locator('#done').click()
  await expect(frameOf(page).locator('#list li')).toHaveText(['Search: Done'])
  await expect(frameOf(other).locator('#list li')).toHaveText(['Search: Done'])

  const items = await call<{ items: { item: Paper; values: { property_id: string; value: unknown }[] }[] }>(request, 'GET', `/workspaces/${WS}/papers/${db.id}/items?limit=10`)
  expect(items.items[0]?.values.find((v) => v.property_id === refs.s)?.value).toBe(refs.done)
  await other.close()
})

test('T22 the frame writes into the item page, and a read-only link cannot', async ({ page, request }) => {
  const { db, refs } = await seed(request)
  const item = refs.i1 ?? ''
  const html = await connected(request, db.id, true)
  await openPaper(page, html.id)
  await expect(frameOf(page).locator('#state')).toHaveText('ready write')
  await frameOf(page).locator('#note').click()
  await expect.poll(async () => (await blocksOf(request, item)).map((b) => b.content)).toContain('Written from the page')

  const ro = await connected(request, db.id, false)
  await openPaper(page, ro.id)
  await expect(page.locator('.dbline .dbchip')).toContainText(msg('dbReadOnly'))
  await expect(frameOf(page).locator('#state')).toHaveText('ready read')
  await frameOf(page).locator('#done').click()
  // read_only: nothing changes
  await page.waitForTimeout(500)
  const items = await call<{ items: { values: { property_id: string; value: unknown }[] }[] }>(request, 'GET', `/workspaces/${WS}/papers/${db.id}/items?limit=10`)
  expect(items.items[0]?.values.find((v) => v.property_id === refs.s)?.value).toBe(refs.todo)
})

test('a document the frame reloads is not connected, and its calls fail with unavailable', async ({ page, request }) => {
  const { db } = await seed(request)
  const html = await connected(request, db.id, true)
  await openPaper(page, html.id)
  await expect(frameOf(page).locator('#state')).toHaveText('ready write')
  await frameOf(page).locator('#reload').click()
  await expect(frameOf(page).locator('#state')).toHaveText('error unavailable')
})
