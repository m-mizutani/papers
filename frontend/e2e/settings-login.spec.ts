import { expect, test } from '@playwright/test'
import { call, msg, unique } from './helpers'
import type { ApiToken } from '../src/api/types'

test('T48 an agent token is created, its secret shown once, and listed with its kind', async ({ page, request }) => {
  await page.goto('/settings')
  await page.getByRole('button', { name: msg('createToken') }).click()
  const form = page.getByRole('dialog', { name: msg('createToken') })
  const name = unique('Helper')
  await form.getByLabel(msg('tokenName')).fill(name)
  await expect(form.getByRole('radio', { name: new RegExp(msg('tokenAgent')) })).toBeChecked()
  await form.getByRole('button', { name: msg('createToken') }).click()

  const shown = page.getByRole('dialog', { name: msg('createToken') })
  await expect(shown.getByText(msg('tokenOnce'))).toBeVisible()
  const secret = (await shown.locator('pre.out').first().textContent())?.trim() ?? ''
  expect(secret).toMatch(/^pap_/)
  // the dialog shows the token only, no command with it in
  await expect(shown.locator('pre.out')).toHaveCount(1)
  await expect(shown).not.toContainText('Bearer')
  await shown.getByRole('button', { name: msg('done') }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  const row = page.locator('.lr', { hasText: name })
  await expect(row).toContainText(msg('tokenAgent'))
  await expect(row).toContainText(msg('readWrite'))
  await expect(row).toContainText(msg('neverUsed'))
  await expect(row).toContainText(msg('issuedAt', { when: '' }).trim())
  // the secret is not shown again
  await page.reload()
  await expect(page.locator('.lr', { hasText: name })).toBeVisible()
  expect(await page.content()).not.toContain(secret)
  const tokens = await call<{ tokens: ApiToken[] }>(request, 'GET', '/tokens')
  expect(tokens.tokens.find((t) => t.name === name)?.kind).toBe('agent')
})

test('T57 the login page explains a not-allowed account and offers another one', async ({ page }) => {
  await page.goto('/login?error=not_allowed')
  const alert = page.getByRole('alert')
  await expect(alert).toContainText(msg('notAllowedTitle'))
  await expect(alert).toContainText(msg('notAllowedBody'))
  const other = page.getByRole('link', { name: msg('tryOther') })
  await expect(other).toBeVisible()
  await expect(other).toHaveAttribute('href', /^\/api\/auth\/login\?return_to=/)
  await expect(page.getByRole('link', { name: msg('signIn') })).toBeVisible()
})

test('T58 someone with no usable workspace sees "no workspaces" and can sign out', async ({ page }) => {
  // The development server always offers its workspaces, so the answer is replaced here.
  await page.route('**/api/v1/workspaces', (route) => route.fulfill({ json: { workspaces: [], groups: [] } }))
  await page.goto('/')
  await expect(page).toHaveURL(/\/workspaces$/)
  await expect(page.getByText(msg('noWorkspacesTitle'))).toBeVisible()
  await expect(page.getByText(msg('noWorkspacesBody'))).toBeVisible()
  await page.getByRole('button', { name: msg('signOut') }).click()
  await expect(page).toHaveURL(/\/login/)
})
