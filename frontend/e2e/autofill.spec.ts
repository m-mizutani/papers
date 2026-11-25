import { expect, test, type Page } from '@playwright/test'
import { createPaper, msg, openPaper, unique, WS } from './helpers'

const TEXT_FIELDS = 'input:not([type=checkbox]):not([type=radio]):not([type=file]), textarea'

/** Every text field on the page carries the password managers' ignore attributes. */
async function expectNoAutofill(page: Page): Promise<void> {
  const fields = page.locator(TEXT_FIELDS)
  expect(await fields.count()).toBeGreaterThan(0)
  for (const field of await fields.all()) {
    await expect(field).toHaveAttribute('autocomplete', 'off')
    await expect(field).toHaveAttribute('data-1p-ignore', 'true')
    await expect(field).toHaveAttribute('data-lpignore', 'true')
    await expect(field).toHaveAttribute('data-bwignore', 'true')
    await expect(field).toHaveAttribute('data-form-type', 'other')
  }
}

test('T4 the author filter search is left alone by password managers', async ({ page }) => {
  await page.goto(`/ws/${WS}/papers`)
  await page.locator('.pfilters').getByRole('button', { name: /^Author:/ }).click()
  const search = page.getByLabel(msg('searchByName'))
  await expect(search).toBeVisible()
  await expect(search).toHaveAttribute('data-1p-ignore', 'true')
  await expect(search).toHaveAttribute('data-lpignore', 'true')
  await expect(search).toHaveAttribute('data-bwignore', 'true')
  await expect(search).toHaveAttribute('data-form-type', 'other')
  await expect(search).toHaveAttribute('autocomplete', 'off')
})

test('T5 the paper page and the settings page have no field password managers fill', async ({ page, request }) => {
  const paper = await createPaper(request, { title: unique('Autofill'), markdown: 'Some text\n\n```go\nfmt.Println(1)\n```' })
  await openPaper(page, paper.id)
  await expectNoAutofill(page)
  // the code block's language field shows only while the block is edited
  await page.locator('.code pre').click()
  await expect(page.getByLabel(msg('codeLanguage'))).toBeVisible()
  await expectNoAutofill(page)

  await page.goto('/settings')
  await page.getByRole('button', { name: msg('createToken') }).click()
  await expect(page.getByRole('dialog', { name: msg('createToken') }).getByLabel(msg('tokenName'))).toBeVisible()
  await expectNoAutofill(page)
})
