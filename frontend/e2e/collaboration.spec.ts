import { expect, test } from '@playwright/test'
import { agent, blocks, call, createPaper, editBlock, editor, eventually, msg, openPaper, synced, topBlocks, unique, WS } from './helpers'

test('T20 someone changes the block I am editing; "keep both" keeps theirs and adds mine below', async ({ page, request, playwright, baseURL }) => {
  const paper = await createPaper(request, { title: unique('Conflict'), markdown: 'shared text' })
  const other = await agent(playwright, request, baseURL ?? '')
  await openPaper(page, paper.id)
  const [b] = await topBlocks(request, paper.id)

  await editBlock(page, 0)
  await page.keyboard.type(' mine')
  // their change lands before mine is sent (it waits for a pause in typing)
  await call(other.api, 'POST', `/workspaces/${WS}/papers/${paper.id}/ops`, { ops: [{ op: 'update', block_id: b?.id, base_rev: b?.rev, content: 'theirs now' }] })

  const conf = page.locator('.conf')
  await expect(conf).toBeVisible()
  await expect(conf).toContainText(msg('conflictTitle', { name: `${other.name} (${msg('forUser', { name: 'Developer' })})` }))
  await expect(conf).toContainText('theirs now')
  await expect(conf).toContainText('shared text mine')
  await conf.getByRole('button', { name: msg('keepBoth') }).click()
  await expect(conf).toHaveCount(0)

  await expect(blocks(page).locator('.txt, .rich-editor')).toHaveText(['theirs now', 'shared text mine'])
  await page.keyboard.press('Escape')
  await synced(page)
  const bs = await eventually(() => topBlocks(request, paper.id), (x) => x.length === 2, 'both kept')
  expect(bs.map((x) => x.content)).toEqual(['theirs now', 'shared text mine'])
  expect(bs[0]?.id).toBe(b?.id)
  await other.api.dispose()
})

test('T21 someone deletes the block I am editing; "add as a new block" keeps my text', async ({ page, request, playwright, baseURL }) => {
  const paper = await createPaper(request, { title: unique('Deleted'), markdown: 'before\n\ndoomed' })
  const other = await agent(playwright, request, baseURL ?? '')
  await openPaper(page, paper.id)
  const [, b] = await topBlocks(request, paper.id)

  await editBlock(page, 1)
  await page.keyboard.type(' but mine')
  await call(other.api, 'POST', `/workspaces/${WS}/papers/${paper.id}/ops`, { ops: [{ op: 'delete', block_id: b?.id, base_rev: b?.rev }] })

  const conf = page.locator('.conf')
  await expect(conf).toBeVisible()
  await expect(conf).toContainText('doomed but mine')
  await conf.getByRole('button', { name: msg('addAsNew') }).click()
  await expect(conf).toHaveCount(0)
  await expect(editor(page)).toHaveText('doomed but mine')
  await page.keyboard.press('Escape')
  await synced(page)

  const bs = await eventually(() => topBlocks(request, paper.id), (x) => x.length === 2 && x[1]?.content === 'doomed but mine', 'my text kept')
  expect(bs[0]?.content).toBe('before')
  expect(bs[1]?.id).not.toBe(b?.id)
  await expect(blocks(page).locator('.txt')).toHaveText(['before', 'doomed but mine'])
  await other.api.dispose()
})

test('T22 presence from another session shows a name tag on the block', async ({ page, request, playwright, baseURL }) => {
  const paper = await createPaper(request, { title: unique('Presence'), markdown: 'one\n\ntwo' })
  const other = await agent(playwright, request, baseURL ?? '', 'Scribe')
  await openPaper(page, paper.id)
  const [, b] = await topBlocks(request, paper.id)

  await call(other.api, 'POST', `/workspaces/${WS}/papers/${paper.id}/presence`, { block_id: b?.id, leaving: false })
  const tag = blocks(page).nth(1).locator('.ptag')
  await expect(tag).toHaveText(`✦ ${msg('editing', { name: other.name })}`)
  await expect(blocks(page).nth(1)).toHaveClass(/pres/)
  await expect(blocks(page).nth(0).locator('.ptag')).toHaveCount(0)
  await expect(page.locator('.faces .face.ai')).toHaveCount(1)

  // leaving takes the tag away
  await call(other.api, 'POST', `/workspaces/${WS}/papers/${paper.id}/presence`, { block_id: b?.id, leaving: true })
  await expect(tag).toHaveCount(0)
  await other.api.dispose()
})
