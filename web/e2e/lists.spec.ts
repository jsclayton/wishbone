import type { Page } from '@playwright/test'
import { expect, finishProfile, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes a user of its own
// for this spec. That user's primary list is theirs alone, so other specs can
// run on the same server.
test.describe('lists and items', () => {
  test.use({ signedIn: true })

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })
  // The items load after the list does, so wait for them.
  const expectTitles = (page: Page, expected: string[]) =>
    expect.poll(() => page.getByRole('list', { name: 'Items' }).locator('strong').allTextContents()).toEqual(expected)

  test('a user adds, edits, reorders, and deletes items on their primary list', async ({ browser, request }) => {
    const first = `Hedy${Math.random().toString(36).slice(2, 8)}`
    const made = await request.post('/api/v2/users', { data: { name: first, birthday: { month: 11, day: 9, year: null } } })
    expect(made.status()).toBe(201)
    const link = await request.post(`/api/v2/users/${(await made.json()).id}/login-links`)
    expect(link.status()).toBe(201)

    const device = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(device)
    const page = await device.newPage()
    try {
      await page.goto((await link.json()).url)
      await page.getByRole('button', { name: 'Sign in' }).click()
      await finishProfile(page)

      // My lists leads to the user's own list, named after them.
      await page.getByRole('link', { name: 'My lists' }).click()
      await expect(h1(page, 'My lists')).toBeVisible()
      await page.getByRole('link', { name: new RegExp(`${first}\\s*No items`) }).click()
      await expect(h1(page, first)).toBeVisible()
      await expect(page.getByRole('heading', { level: 2, name: 'This list is empty' })).toBeVisible()

      // The box on Home opens the add form with typed words, which adds them to the primary list.
      await page.goto('/')
      await page.getByRole('textbox', { name: 'Add to my list' }).fill('a book about sharks')
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(page, 'Add an item')).toBeVisible()
      await expect(page.getByLabel('Title')).toHaveValue('a book about sharks')
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(page, first)).toBeVisible()
      await expect(page.getByRole('list', { name: 'Items' }).getByText('a book about sharks')).toBeVisible()

      // Add on the list's page opens the form, with a price and a quantity.
      await page.getByRole('link', { name: 'Add', exact: true }).click()
      await expect(h1(page, 'Add an item')).toBeVisible()
      await page.getByLabel('Title').fill('A kettle')
      await page.getByLabel('Price').fill('45')
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(page, first)).toBeVisible()
      await page.getByRole('link', { name: 'Add', exact: true }).click()
      await page.getByLabel('Title').fill('A lamp')
      await page.getByLabel('Quantity').fill('2')
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(page, first)).toBeVisible()
      await expectTitles(page, ['a book about sharks', 'A kettle', 'A lamp'])

      // An item's page: edit it.
      await page.getByRole('link', { name: /A kettle/ }).click()
      await expect(h1(page, 'A kettle')).toBeVisible()
      await expect(page.getByText('$45.00')).toBeVisible()
      await page.getByRole('button', { name: 'Edit', exact: true }).click()
      await page.getByLabel('Title').fill('A copper kettle')
      await page.getByLabel('Price').fill('60')
      await page.getByLabel('Notes').fill('Not too big')
      await page.getByRole('button', { name: 'Save' }).click()
      await expect(h1(page, 'A copper kettle')).toBeVisible()
      await expect(page.getByText('$60.00')).toBeVisible()
      await expect(page.getByText('Not too big')).toBeVisible()
      await page.getByRole('button', { name: 'One more' }).click()
      await expect(page.getByText('2', { exact: true })).toBeVisible()

      // The list shows the change, and Change order moves the lamp to the top.
      await page.getByRole('link', { name: first, exact: true }).first().click()
      await expect(h1(page, first)).toBeVisible()
      await expectTitles(page, ['a book about sharks', 'A copper kettle', 'A lamp'])
      await page.getByRole('button', { name: 'Change order' }).click()
      await page.getByRole('button', { name: 'Move up: A lamp' }).click()
      await page.getByRole('button', { name: 'Move up: A lamp' }).click()
      await expectTitles(page, ['A lamp', 'a book about sharks', 'A copper kettle'])
      await page.getByRole('button', { name: 'Done' }).click()

      // The order stays after a reload.
      await page.reload()
      await expectTitles(page, ['A lamp', 'a book about sharks', 'A copper kettle'])

      // Delete asks first. Keep it changes nothing. Delete removes the item and opens the list.
      await page.getByRole('link', { name: /A lamp/ }).click()
      await page.getByRole('link', { name: 'Delete this item' }).click()
      await expect(h1(page, 'Delete this item?')).toBeVisible()
      await page.getByRole('link', { name: 'Keep it' }).click()
      await expect(h1(page, 'A lamp')).toBeVisible()
      await page.getByRole('link', { name: 'Delete this item' }).click()
      await page.getByRole('button', { name: 'Delete this item' }).click()
      await expect(h1(page, first)).toBeVisible()
      await expectTitles(page, ['a book about sharks', 'A copper kettle'])

      // Edit list renames the list and gives it a description.
      await page.getByRole('link', { name: 'Edit list' }).click()
      await page.getByLabel('Name').fill('Things I want')
      await page.getByLabel('Description').fill('Mostly **books**.')
      await page.getByRole('button', { name: 'Save' }).click()
      await expect(h1(page, 'Things I want')).toBeVisible()
      await expect(page.getByText('books', { exact: true })).toBeVisible()
    } finally {
      await device.close()
    }
  })
})
