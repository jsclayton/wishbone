import type { Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server.
test.describe('extra lists', () => {
  test.use({ signedIn: true })

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })
  /** A list row with no items: its name, then the count. The avatar between them has no words. */
  const no = (name: string) => new RegExp(`^${name}\\s*No items$`)

  test('a parent makes extra lists for themselves and their child, adds to one by the List choice, and deletes it', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, emma] = [`Mom${suffix}`, `Emma${suffix}`]
    const made = await request.post('/api/v2/users', { data: { name: mom, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    const momId = (await made.json()).id as string
    const child = await request.post('/api/v2/users', { data: { name: emma, birthday: { month: 6, day: 7, year: null }, parent_ids: [momId] } })
    expect(child.status()).toBe(201)

    const link = await request.post(`/api/v2/users/${momId}/login-links`)
    expect(link.status()).toBe(201)
    const device = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(device)
    const page = await device.newPage()
    try {
      await page.goto((await link.json()).url)
      await page.getByRole('button', { name: 'Sign in', exact: true }).click()
      await page.getByRole('button', { name: 'Continue', exact: true }).click()
      await page.getByRole('button', { name: 'Not now', exact: true }).click()
      await expect(h1(page, 'Home')).toBeVisible()

      // New list asks who it is for, because Mom has a child. This one is for Mom.
      await page.getByRole('link', { name: 'My lists', exact: true }).click()
      await page.getByRole('link', { name: 'New list', exact: true }).click()
      await expect(h1(page, 'New list')).toBeVisible()
      await page.getByLabel('Name', { exact: true }).fill('Garden')
      await page.getByLabel(/Description/).fill('Spring planting')
      await expect(page.getByRole('radio', { name: 'Me', exact: true })).toBeChecked()
      await page.getByRole('button', { name: 'Save', exact: true }).click()
      await expect(h1(page, 'Garden')).toBeVisible()
      await expect(page.getByText('Spring planting')).toBeVisible()

      // This one is for Emma. Mom is not its recipient.
      await page.goto('/lists/new')
      await page.getByLabel('Name', { exact: true }).fill('Cousins')
      await page.getByRole('radio', { name: emma, exact: true }).check()
      await page.getByRole('button', { name: 'Save', exact: true }).click()
      await expect(h1(page, 'Cousins')).toBeVisible()
      await expect(page.getByText(emma, { exact: true })).toBeVisible()

      // My lists puts Garden under Mom and Cousins under Emma.
      await page.goto('/lists')
      await expect(page.getByRole('list', { name: 'Your lists', exact: true }).getByRole('listitem')).toHaveText([no(mom), no("Garden")])
      await expect(page.getByRole('region', { name: emma, exact: true }).getByRole('listitem')).toHaveText([no(emma), no("Cousins")])

      // The box on Home opens the add form on Mom's own list. The List choice moves the item to Garden.
      await page.goto('/')
      await page.getByRole('textbox', { name: 'Add to my list', exact: true }).fill('A trowel')
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(page, 'Add an item')).toBeVisible()
      // The choice starts on Mom's primary list, which is the list the add screen's address names.
      const primaryId = new URL(page.url()).pathname.split('/')[2]!
      const choice = page.getByRole('combobox', { name: 'List', exact: true })
      await expect(choice).toHaveValue(primaryId)
      await choice.selectOption({ label: 'Garden' })
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(page, 'Garden')).toBeVisible()
      await expect(page.getByRole('list', { name: 'Items', exact: true }).getByText('A trowel')).toBeVisible()
      const gardenPath = new URL(page.url()).pathname

      // The item's edit form has a List choice too, on the item's own list. It moves the item to Emma's Cousins.
      await page.getByRole('link', { name: /A trowel/ }).click()
      await expect(h1(page, 'A trowel')).toBeVisible()
      await page.getByRole('button', { name: 'Edit', exact: true }).click()
      const moveTo = page.getByRole('combobox', { name: 'List', exact: true })
      await expect(moveTo).toHaveValue(gardenPath.split('/')[2]!)
      await moveTo.selectOption({ label: 'Cousins' })
      await page.getByRole('button', { name: 'Save', exact: true }).click()
      await expect(page.getByRole('link', { name: 'Cousins', exact: true })).toBeVisible()
      await page.getByRole('link', { name: 'Cousins', exact: true }).click()
      await expect(h1(page, 'Cousins')).toBeVisible()
      await expect(page.getByRole('list', { name: 'Items', exact: true }).getByText('A trowel')).toBeVisible()
      await page.goto(gardenPath)
      await expect(h1(page, 'Garden')).toBeVisible()
      await expect(page.getByRole('heading', { level: 2, name: 'This list is empty', exact: true })).toBeVisible()

      // Delete list asks first. Keep it changes nothing. Delete removes the list and its items, and opens My lists.
      await page.getByRole('link', { name: 'Delete list', exact: true }).click()
      await expect(h1(page, 'Delete this list?')).toBeVisible()
      await page.getByRole('link', { name: 'Keep it', exact: true }).click()
      await expect(h1(page, 'Garden')).toBeVisible()
      await page.getByRole('link', { name: 'Delete list', exact: true }).click()
      await page.getByRole('button', { name: 'Delete Garden and its items', exact: true }).click()
      await expect(h1(page, 'My lists')).toBeVisible()
      await expect(page.getByRole('list', { name: 'Your lists', exact: true }).getByRole('listitem')).toHaveText([no(mom)])
    } finally {
      await device.close()
    }
  })
})
