import { expect, test } from '@playwright/test'
import { DEMO_URL, WS, titleField } from './helpers'

// Demo mode (papers serve --demo) fills each empty workspace with the sample
// papers of pkg/usecase/sample at startup. The default e2e server runs
// without it.

const SAMPLES = ['Welcome to papers', 'Diagrams and rich blocks', 'Release dashboard']

test.describe('with --demo', () => {
  test.use({ baseURL: DEMO_URL })

  test('T14 the home of an empty workspace links the sample papers, and they nest', async ({ page }) => {
    await page.goto(`/ws/${WS}`)
    const body = page.locator('.blocks')
    await expect(body).toContainText('demo mode')
    for (const title of SAMPLES) {
      await expect(body.getByText(title, { exact: true })).toBeVisible()
    }
    await body.getByText('Welcome to papers', { exact: true }).click()
    await expect(page.locator('.blocks').getByText('Working with AI agents', { exact: true })).toBeVisible()
  })

  test('T16 the HTML sample paper shows its document and the rich sample draws its diagram', async ({ page }) => {
    await page.goto(`/ws/${WS}`)
    await page.locator('.blocks').getByText('Release dashboard', { exact: true }).click()
    const doc = page.frameLocator('.htmlpage iframe')
    await expect(doc.locator('h1')).toHaveText('Release 2.4 dashboard')
    await expect(doc.locator('#done')).toHaveText('3')

    await page.goto(`/ws/${WS}`)
    await page.locator('.blocks').getByText('Diagrams and rich blocks', { exact: true }).click()
    await expect(page.locator('.b-mermaid svg')).toBeVisible()
  })
})

test('T15 the server without --demo writes no samples', async ({ page }) => {
  await page.goto(`/ws/${WS}`)
  await expect(titleField(page)).toBeVisible()
  await expect(page.getByText('Welcome to papers', { exact: true })).toHaveCount(0)
})
