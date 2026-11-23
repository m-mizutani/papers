import { expect, type APIRequestContext, type Locator, type Page, type PlaywrightWorkerArgs } from '@playwright/test'
import type { Block, Paper, Workspace } from '../src/api/types'
import { en } from '../src/i18n/en'
import type { MsgKey } from '../src/i18n/keys'

/** The English UI text of a message key, with its parameters filled in. */
export function msg(key: MsgKey, params: Record<string, string | number> = {}): string {
  return en[key].replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? `{${name}}`))
}

/** The example workspaces the e2e server is started with. */
export const WS = 'product'
export const OTHER_WS = 'security'

const headers = { 'X-Requested-With': 'papers' }

/** A title no other test uses. */
export function unique(prefix: string): string {
  return `${prefix} ${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`
}

/** Calls the REST API as the signed-in (development) user, or with the context's own credentials. */
export async function call<T>(request: APIRequestContext, method: string, path: string, data?: unknown, extra: Record<string, string> = {}): Promise<T> {
  const res = await request.fetch(`/api/v1${path}`, { method, headers: { ...headers, ...extra }, data })
  expect(res.ok(), `${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

export async function workspaceOf(request: APIRequestContext, ws = WS): Promise<Workspace> {
  const r = await call<{ workspaces: Workspace[] }>(request, 'GET', '/workspaces')
  const w = r.workspaces.find((x) => x.id === ws)
  if (!w) throw new Error(`workspace ${ws} not found`)
  return w
}

export async function homeOf(request: APIRequestContext, ws = WS): Promise<string> {
  return (await workspaceOf(request, ws)).home_paper_id
}

/**
 * Creates a paper. It is filed under the workspace's home unless a parent is
 * given or it is asked to be unfiled (or private): tests keep the sidebar's
 * unfiled count small.
 */
export async function createPaper(request: APIRequestContext, input: { title: string; markdown?: string; parent?: string; visibility?: 'public' | 'private'; unfiled?: boolean }, ws = WS): Promise<Paper> {
  const { unfiled, ...rest } = input
  const parent = rest.parent ?? (unfiled || rest.visibility === 'private' ? undefined : await homeOf(request, ws))
  return call<Paper>(request, 'POST', `/workspaces/${ws}/papers`, { ...rest, parent })
}

export async function blocksOf(request: APIRequestContext, paper: string, ws = WS): Promise<Block[]> {
  const page = await call<{ blocks: Block[] }>(request, 'GET', `/workspaces/${ws}/papers/${paper}/blocks?limit=500`)
  return page.blocks
}

/** The top-level blocks of a paper, in order. */
export async function topBlocks(request: APIRequestContext, paper: string, ws = WS): Promise<Block[]> {
  return (await blocksOf(request, paper, ws)).filter((b) => !b.parent_id)
}

export async function paperOf(request: APIRequestContext, paper: string, ws = WS): Promise<Paper> {
  const view = await call<{ paper: Paper }>(request, 'GET', `/workspaces/${ws}/papers/${paper}`)
  return view.paper
}

export function ops(request: APIRequestContext, paper: string, list: unknown[], ws = WS): Promise<{ blocks?: Block[]; refs?: Record<string, string> }> {
  return call(request, 'POST', `/workspaces/${ws}/papers/${paper}/ops`, { ops: list })
}

export function saveVersion(request: APIRequestContext, paper: string, ws = WS): Promise<unknown> {
  return call(request, 'POST', `/workspaces/${ws}/papers/${paper}/versions`, { note: '' })
}

/**
 * Another writer: an AI agent token of the development user. Sign-in is off,
 * so every browser is the same person; an agent is a different actor, with
 * its own presence and its own changes arriving through the change feed.
 */
export async function agent(playwright: PlaywrightWorkerArgs['playwright'], request: APIRequestContext, baseURL: string, name = 'Robo'): Promise<{ api: APIRequestContext; name: string }> {
  const res = await call<{ secret: string }>(request, 'POST', '/tokens', { name: unique(name), kind: 'agent', agent_name: name, scope: 'write', ttl_hours: 24, workspaces: [] })
  const api = await playwright.request.newContext({ baseURL, extraHTTPHeaders: { Authorization: `Bearer ${res.secret}` } })
  return { api, name }
}

/** Opens a paper and waits until it is live. */
export async function openPaper(page: Page, paper: string, ws = WS): Promise<void> {
  await page.goto(`/ws/${ws}/p/${paper}`)
  await waitLive(page)
}

/** The paper's title field. */
export const titleField = (page: Page) => page.getByRole('textbox', { name: msg('paperTitle'), exact: true })

export async function waitLive(page: Page): Promise<void> {
  await expect(titleField(page)).toBeVisible()
  await expect(page.locator('.sync')).toHaveText(msg('synced'))
}

/** Waits until nothing is on its way to the server. */
export async function synced(page: Page): Promise<void> {
  await expect(page.locator('.sync')).toHaveText(msg('synced'))
  await expect(page.locator('.blk.pending')).toHaveCount(0)
}

export const blocks = (page: Page) => page.locator('.blocks > .blk')
/** The shown body of the n-th block. */
export const body = (page: Page, n: number): Locator => blocks(page).nth(n).locator('.bd')
/** The text editor; only one block (or table cell) is edited at a time. */
export const editor = (page: Page) => page.locator('.rich-editor')

/** Starts editing a text block with the caret at its end. */
export async function editBlock(page: Page, n: number): Promise<void> {
  await blocks(page).nth(n).locator('.txt').click()
  await expect(editor(page)).toBeFocused()
  await page.keyboard.press('End')
}

/** Leaves the block being edited with Escape, which sends it. */
export async function finishEditing(page: Page): Promise<void> {
  await editor(page).press('Escape')
  await expect(editor(page)).toHaveCount(0)
  await synced(page)
}

/** Opens the paper's "More" menu and picks an item. */
export async function menu(page: Page, item: MsgKey): Promise<void> {
  await page.getByRole('button', { name: msg('more'), exact: true }).click()
  await page.getByRole('menuitem', { name: msg(item) }).click()
}

/** Polls until fn's result passes the check (the server may still be working in the background). */
export async function eventually<T>(fn: () => Promise<T>, check: (v: T) => boolean, message = 'condition'): Promise<T> {
  let last: T | undefined
  await expect.poll(async () => {
    last = await fn()
    return check(last) ? 'ok' : JSON.stringify(last).slice(0, 2000)
  }, { message, timeout: 15_000 }).toBe('ok')
  return last as T
}

/** Drags with the mouse from one element to a point, in small steps (pointer events). */
export async function drag(page: Page, from: Locator, to: { x: number; y: number }): Promise<void> {
  const a = await from.boundingBox()
  if (!a) throw new Error('nothing to drag')
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2 - 8, { steps: 3 })
  await page.mouse.move(to.x, to.y, { steps: 10 })
  await page.mouse.up()
}
