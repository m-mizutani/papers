import { expect, type APIRequestContext, type Page } from '@playwright/test'
import type { Block, Paper } from '../src/api/types'
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

/** Calls the REST API as the signed-in (development) user. */
export async function call<T>(request: APIRequestContext, method: string, path: string, data?: unknown, extra: Record<string, string> = {}): Promise<T> {
  const res = await request.fetch(`/api/v1${path}`, { method, headers: { ...headers, ...extra }, data })
  expect(res.ok(), `${method} ${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
  const text = await res.text()
  return (text ? JSON.parse(text) : undefined) as T
}

export function createPaper(request: APIRequestContext, input: { title: string; markdown?: string; parent?: string; visibility?: 'public' | 'private' }, ws = WS): Promise<Paper> {
  return call<Paper>(request, 'POST', `/workspaces/${ws}/papers`, input)
}

export async function blocksOf(request: APIRequestContext, paper: string, ws = WS): Promise<Block[]> {
  const page = await call<{ blocks: Block[] }>(request, 'GET', `/workspaces/${ws}/papers/${paper}/blocks?limit=500`)
  return page.blocks
}

export async function paperOf(request: APIRequestContext, paper: string, ws = WS): Promise<Paper> {
  const view = await call<{ paper: Paper }>(request, 'GET', `/workspaces/${ws}/papers/${paper}`)
  return view.paper
}

/**
 * Answers the page's prompts in order (accepting confirms on the way) and
 * records every dialog message.
 */
export function answerDialogs(page: Page, answers: string[] = []): string[] {
  const seen: string[] = []
  page.on('dialog', (d) => {
    seen.push(d.message())
    if (d.type() === 'prompt') void d.accept(answers.shift() ?? d.defaultValue())
    else void d.accept()
  })
  return seen
}

/** Opens a paper and waits until it is live. */
export async function openPaper(page: Page, paper: string, ws = WS): Promise<void> {
  await page.goto(`/ws/${ws}/p/${paper}`)
  await expect(page.getByLabel(msg('paperTitle'))).toBeVisible()
  await expect(page.locator('.status.open')).toBeVisible()
}

export const blocks = (page: Page) => page.locator('.sheet > .block')
/** The shown body of a block, without its tools. */
export const body = (page: Page, n: number) => blocks(page).nth(n).locator(':scope > :first-child')
/** The text editor; only one block (or table cell) is edited at a time. */
export const editor = (page: Page) => page.locator('.rich-editor')

/** Leaves the block being edited with Escape, which sends it. */
export async function finishEditing(page: Page): Promise<void> {
  await editor(page).press('Escape')
  await expect(editor(page)).toHaveCount(0)
}

/** Opens the paper's "More" menu and picks an item. */
export async function menu(page: Page, item: MsgKey): Promise<void> {
  await page.getByLabel(msg('more')).click()
  await page.getByRole('button', { name: msg(item), exact: true }).click()
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
