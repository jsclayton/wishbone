import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server.
test.describe("other users' lists", () => {
  test.use({ signedIn: true })

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })

  async function createUser(request: APIRequestContext, name: string, extra: object = {}) {
    const made = await request.post('/api/v2/users', { data: { name, birthday: { month: 5, day: 4, year: null }, ...extra } })
    expect(made.status()).toBe(201)
    return (await made.json()).id as string
  }

  /** Signs a user in on a device of their own, through a login link from the admin, and finishes their profile. */
  async function signInAs(browser: Browser, request: APIRequestContext, userId: string) {
    const link = await request.post(`/api/v2/users/${userId}/login-links`)
    expect(link.status()).toBe(201)
    const device = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(device)
    const page = await device.newPage()
    await page.goto((await link.json()).url)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await page.getByRole('button', { name: 'Continue' }).click()
    await page.getByRole('button', { name: 'Not now' }).click()
    await expect(h1(page, 'Home')).toBeVisible()
    return { device, page }
  }

  test('a giver sees a user’s list, and a parent edits their child’s list', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, sue, emma, hedy] = [`Mom${suffix}`, `Sue${suffix}`, `Emma${suffix}`, `Hedy${suffix}`]
    const momId = await createUser(request, mom)
    const sueId = await createUser(request, sue)
    await createUser(request, emma, { parent_ids: [momId] })
    const hedyId = await createUser(request, hedy)
    const group = await request.post('/api/v2/groups', { data: { name: `Cousins ${suffix}` } })
    expect(group.status()).toBe(201)
    const groupId = (await group.json()).id as string
    for (const id of [momId, sueId]) {
      expect((await request.post(`/api/v2/groups/${groupId}/users`, { data: { user_id: id } })).status()).toBe(201)
    }
    expect((await request.patch(`/api/v2/users/${momId}`, { data: { good_to_know: 'Likes **tea**.' } })).status()).toBe(200)

    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: sueDevice, page: sues } = await signInAs(browser, request, sueId)
    const { device: hedyDevice, page: hedys } = await signInAs(browser, request, hedyId)
    try {
      // Mom adds to her list with the box on Home.
      await moms.getByRole('textbox', { name: 'Add to my list' }).fill('A kettle')
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, 'Add an item')).toBeVisible()
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, mom)).toBeVisible()

      // Sue shares a group with Mom: Mom's page shows her good to know and her list, with no way to change it.
      await sues.getByRole('region', { name: `Cousins ${suffix}` }).getByRole('link', { name: mom }).click()
      await expect(h1(sues, mom)).toBeVisible()
      await expect(sues.getByRole('region', { name: 'Good to know' })).toContainText('Likes tea.')
      await expect(sues.getByRole('list', { name: 'Items' }).getByText('A kettle')).toBeVisible()
      await expect(sues.getByRole('link', { name: 'Add', exact: true })).toHaveCount(0)
      await sues.getByRole('link', { name: /A kettle/ }).click()
      await expect(h1(sues, 'A kettle')).toBeVisible()
      await expect(sues.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0)

      // Hedy shares no group with Mom, so Mom's list is not hers.
      await hedys.goto(`/users/${momId}`)
      await expect(h1(hedys, "We can't find that page")).toBeVisible()

      // Mom's page shows her Emma, whose list she edits as a parent.
      await moms.goto('/')
      await moms.getByRole('region', { name: 'Your children' }).getByRole('link', { name: emma }).click()
      await expect(h1(moms, emma)).toBeVisible()
      await expect(moms.getByRole('heading', { level: 2, name: 'This list is empty' })).toBeVisible()
      await moms.getByRole('link', { name: 'Add', exact: true }).click()
      await moms.getByLabel('Title').fill('A bike')
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(moms.getByRole('list', { name: 'Items' }).getByText('A bike')).toBeVisible()

      // The item says who added it, and Mom's own page is My lists, with a part for Emma.
      await moms.getByRole('link', { name: /A bike/ }).click()
      await expect(h1(moms, 'A bike')).toBeVisible()
      await expect(moms.getByText(`Added by ${mom}`)).toBeVisible()
      await moms.goto(`/users/${momId}`)
      await expect(h1(moms, 'My lists')).toBeVisible()
      const part = moms.getByRole('region', { name: emma })
      await expect(part.getByRole('listitem')).toHaveText([`${emma}1 item`])
    } finally {
      await momDevice.close()
      await sueDevice.close()
      await hedyDevice.close()
    }
  })
})
