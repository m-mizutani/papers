import { expect, test } from '@playwright/test'
import { createPaper, msg, unique } from './helpers'

test('lists the workspaces in their groups and opens a workspace home', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByText(msg('pickWorkspace'))).toBeVisible()
  const nav = page.getByRole('navigation', { name: msg('navigation') })
  await expect(nav.getByText('Teams')).toBeVisible()
  await expect(nav.getByRole('link', { name: /Product Notes/ })).toBeVisible()

  await nav.getByRole('link', { name: /Security Team/ }).click()
  await expect(page).toHaveURL(/\/ws\/security$/)
  // the home shows the workspace's rules
  await expect(page.locator('.ws-rules')).toContainText('Runbooks for on-call')
  await expect(page.getByLabel(msg('paperTitle'))).toHaveValue('Security Team')
})

test('shows where a paper sits and what was opened recently', async ({ page, request }) => {
  const parentTitle = unique('Parent')
  const childTitle = unique('Child')
  const parent = await createPaper(request, { title: parentTitle })
  const child = await createPaper(request, { title: childTitle, parent: parent.id })

  await page.goto(`/ws/product/p/${child.id}`)
  const crumbs = page.getByRole('navigation', { name: msg('location') })
  await expect(crumbs).toContainText(childTitle)
  await crumbs.getByRole('link', { name: parentTitle }).click()
  await expect(page).toHaveURL(new RegExp(`/p/${parent.id}$`))
  // the child shows as a paper block in its parent's body
  await expect(page.locator('.b-paper', { hasText: childTitle })).toBeVisible()

  const recent = page.getByRole('navigation', { name: msg('navigation') }).locator('section', { has: page.getByRole('heading', { name: msg('recent') }) })
  await expect(recent.getByRole('link', { name: childTitle })).toBeVisible()
  await expect(recent.getByRole('link', { name: parentTitle })).toBeVisible()
})

test('an unknown address goes back to the start', async ({ page }) => {
  await page.goto('/no/such/page')
  await expect(page).toHaveURL(/\/$/)
  await page.goto('/ws/nope')
  await expect(page.getByText(msg('workspaceNotFound'))).toBeVisible()
})
