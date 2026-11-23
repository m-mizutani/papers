import { expect, test } from '@playwright/test'
import {
  blocks, call, createPaper, paperOf, editBlock, editor, eventually, finishEditing, msg, openPaper, ops, saveVersion, synced, titleField, topBlocks, unique, WS,
} from './helpers'
import type { VersionSummary } from '../src/api/types'

const versionsOf = (request: Parameters<typeof createPaper>[0], paper: string) =>
  call<{ versions: VersionSummary[] }>(request, 'GET', `/workspaces/${WS}/papers/${paper}/versions?limit=50`).then((r) => r.versions)

test('T16 T17 saving a version takes one click, shows its URL, then the saved state', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Save'), markdown: 'first' })
  await openPaper(page, paper.id)
  const dialogs: string[] = []
  page.on('dialog', (d) => { dialogs.push(d.message()); void d.dismiss() })

  // the first version
  await page.locator('.savebtn').click()
  let saved = page.getByRole('dialog', { name: msg('savedVersion', { n: 1 }) })
  await expect(saved).toBeVisible()
  await expect(saved.locator('.urlrow .inp')).toHaveValue(new RegExp(`/ws/${WS}/p/${paper.id}/v/1$`))
  await saved.getByRole('button', { name: msg('done') }).click()
  await expect(page.locator('.btn.saved')).toContainText('v1')

  // a change: the save button comes back, and one click saves v2
  await editBlock(page, 0)
  await page.keyboard.type(' and more')
  await finishEditing(page)
  await expect(page.locator('.savebtn')).toBeEnabled()
  await page.locator('.savebtn').click()
  saved = page.getByRole('dialog', { name: msg('savedVersion', { n: 2 }) })
  await expect(saved).toBeVisible()
  await expect(saved.locator('.urlrow .inp')).toHaveValue(`${new URL(page.url()).origin}/ws/${WS}/p/${paper.id}/v/2`)
  await expect(saved.locator('.stat')).toContainText(`~1 ${msg('changed')}`)
  expect(dialogs).toEqual([])
  expect((await versionsOf(request, paper.id)).map((v) => v.number)).toEqual([2, 1])

  // T17: right after saving, the button shows the saved state; Mod+S says nothing changed
  await saved.getByRole('button', { name: msg('done') }).click()
  await expect(page.locator('.btn.saved')).toContainText(msg('savedState'))
  await expect(page.locator('.btn.saved')).toContainText('v2')
  await expect(page.locator('.savebtn')).toHaveCount(0)
  await page.keyboard.press('ControlOrMeta+s')
  await expect(page.locator('.toast', { hasText: msg('nothingToSave') })).toBeVisible()
  expect((await versionsOf(request, paper.id)).length).toBe(2)
})

test('T18 an older version compared with the current state shows added, changed and removed blocks, read-only', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Diff'), markdown: 'alpha\n\nbeta\n\ngamma' })
  await saveVersion(request, paper.id)
  const [a, b, c] = await topBlocks(request, paper.id)
  await ops(request, paper.id, [
    { op: 'update', block_id: b?.id, base_rev: b?.rev, content: 'beta changed' },
    { op: 'delete', block_id: c?.id, base_rev: c?.rev },
    { op: 'insert', type: 'paragraph', content: 'delta', after: a?.id },
  ])

  await openPaper(page, paper.id)
  await page.getByRole('button', { name: msg('history'), exact: true }).click()
  const drawer = page.getByRole('complementary', { name: msg('history') })
  await drawer.getByRole('link', { name: /^v1/ }).click()
  await expect(page).toHaveURL(new RegExp(`/p/${paper.id}/v/1$`))
  const banner = page.locator('.banner.ver')
  await expect(banner).toContainText(msg('viewing', { n: 1 }))
  await banner.getByRole('button', { name: msg('comparedToNow') }).click()
  await expect(banner.getByRole('button', { name: msg('comparedToNow') })).toHaveAttribute('aria-pressed', 'true')

  await expect(page.locator('.blk.mark-changed')).toContainText('beta')
  await expect(page.locator('.blk.mark-removed')).toContainText('gamma')
  await expect(page.locator('.blk.mark-added')).toContainText('delta')
  await expect(page.locator('.blk:not([class*="mark-"])')).toHaveText(['alpha'])

  // read-only: no handles, no save button, no editor
  await expect(page.locator('.hdl')).toHaveCount(0)
  await expect(page.locator('.savebtn')).toHaveCount(0)
  await expect(titleField(page)).toHaveAttribute('readonly', '')
  await blocks(page).filter({ hasText: 'alpha' }).locator('.txt').click()
  await expect(editor(page)).toHaveCount(0)
})

test('T19 restoring with unsaved changes can save them first', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Restore'), markdown: 'old' })
  await saveVersion(request, paper.id)
  const [b] = await topBlocks(request, paper.id)
  const r1 = await ops(request, paper.id, [{ op: 'update', block_id: b?.id, base_rev: b?.rev, content: 'middle' }])
  await saveVersion(request, paper.id)
  const b2 = r1.blocks?.find((x) => x.id === b?.id)
  await ops(request, paper.id, [{ op: 'update', block_id: b?.id, base_rev: b2?.rev, content: 'unsaved' }])

  await page.goto(`/ws/${WS}/p/${paper.id}/v/1`)
  await page.locator('.banner.ver').getByRole('button', { name: msg('restoreThis') }).click()
  const dialog = page.getByRole('dialog', { name: msg('restoreTitle', { n: 1 }) })
  await expect(dialog).toContainText(msg('restoreDirty', { n: 1, m: 2 }))
  await dialog.getByRole('button', { name: msg('saveThenRestore') }).click()

  await expect(page).toHaveURL(new RegExp(`/p/${paper.id}$`))
  await expect(page.locator('.toast', { hasText: msg('restored', { n: 1 }) })).toBeVisible()
  await expect(blocks(page).locator('.txt')).toHaveText(['old'])
  await synced(page)
  // the unsaved state became v3 before the restore
  const versions = await versionsOf(request, paper.id)
  expect(versions.map((v) => v.number)).toEqual([3, 2, 1])
  const v3 = await call<{ blocks: { content: string }[] }>(request, 'GET', `/workspaces/${WS}/papers/${paper.id}/versions/3`)
  expect(v3.blocks.map((x) => x.content)).toEqual(['unsaved'])
  await eventually(() => topBlocks(request, paper.id), (bs) => bs.map((x) => x.content).join() === 'old', 'restored')
})

test('restoring a version warns which child papers come back from the trash, even into an unfiled paper', async ({ page, request }) => {
  const parent = await createPaper(request, { title: unique('Parent') })
  const kid = await createPaper(request, { title: unique('Kid'), parent: parent.id })
  await saveVersion(request, parent.id)
  await call(request, 'POST', `/workspaces/${WS}/papers/${kid.id}/trash`)
  await eventually(() => paperOf(request, kid.id), (p) => !!p.deleted_at, 'trashed')
  await call(request, 'POST', `/workspaces/${WS}/papers/${parent.id}/move`, { unfiled: true })

  // the trash says how long it is kept
  await page.goto(`/ws/${WS}/trash`)
  await expect(page.locator('.lr', { hasText: kid.title })).toContainText(msg('daysLeft', { n: 30 }))

  await page.goto(`/ws/${WS}/p/${parent.id}/v/1`)
  await page.locator('.banner.ver').getByRole('button', { name: msg('restoreThis') }).click()
  const dialog = page.getByRole('dialog', { name: msg('restoreTitle', { n: 1 }) })
  await expect(dialog.locator('.kids')).toContainText(msg('restoreBack'))
  await expect(dialog.locator('.kids')).toContainText(kid.title)
  await dialog.getByRole('button', { name: msg('restore'), exact: true }).click()

  await eventually(() => paperOf(request, kid.id), (p) => !p.deleted_at && p.parent_id === parent.id, 'back under the unfiled parent')
  expect((await paperOf(request, parent.id)).parent_id ?? '').toBe('')
})
