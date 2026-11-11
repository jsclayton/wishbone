import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server.
test.describe('gifts', () => {
  test.use({ signedIn: true })

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })

  async function createUser(request: APIRequestContext, name: string) {
    const made = await request.post('/api/v2/users', { data: { name, birthday: { month: 5, day: 4, year: null } } })
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
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    await page.getByRole('button', { name: 'Continue', exact: true }).click()
    await page.getByRole('button', { name: 'Not now', exact: true }).click()
    await expect(h1(page, 'Home')).toBeVisible()
    return { device, page }
  }

  test('a giver claims an item, changes the gift in Giving, and unclaims it, and the recipient sees none of it', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, sue] = [`Mom${suffix}`, `Sue${suffix}`]
    const momId = await createUser(request, mom)
    const sueId = await createUser(request, sue)
    const group = await request.post('/api/v2/groups', { data: { name: `Cousins ${suffix}` } })
    expect(group.status()).toBe(201)
    const groupId = (await group.json()).id as string
    for (const id of [momId, sueId]) {
      expect((await request.post(`/api/v2/groups/${groupId}/users`, { data: { user_id: id } })).status()).toBe(201)
    }

    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: sueDevice, page: sues } = await signInAs(browser, request, sueId)
    try {
      // Mom adds a kettle and wants three of them.
      await moms.getByRole('textbox', { name: 'Add to my list', exact: true }).fill('A kettle')
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, 'Add an item')).toBeVisible()
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, mom)).toBeVisible()
      await moms.getByRole('list', { name: 'Items', exact: true }).getByText('A kettle', { exact: true }).click()
      await expect(h1(moms, 'A kettle')).toBeVisible()
      await moms.getByRole('button', { name: 'One more', exact: true }).click()
      await expect(moms.getByText('2', { exact: true })).toBeVisible()
      await moms.getByRole('button', { name: 'One more', exact: true }).click()
      await expect(moms.getByText('3', { exact: true })).toBeVisible()

      // Sue sees how many are still wanted on Mom's list.
      await sues.getByRole('region', { name: `Cousins ${suffix}`, exact: true }).getByRole('link', { name: mom, exact: true }).click()
      await expect(h1(sues, mom)).toBeVisible()
      const items = sues.getByRole('list', { name: 'Items', exact: true })
      await expect(items.getByText('3 of 3 still wanted', { exact: true })).toBeVisible()
      await items.getByText('A kettle', { exact: true }).click()
      await expect(h1(sues, 'A kettle')).toBeVisible()
      await expect(sues.getByText('3 of 3 still wanted', { exact: true })).toBeVisible()

      // Claim: the occasion picker starts on Mom's birthday, the only occasion she has. Sue takes two.
      await sues.getByRole('link', { name: "I'll get this", exact: true }).click()
      await expect(h1(sues, "I'll get this")).toBeVisible()
      await expect(sues.getByRole('radio', { name: new RegExp(`^${mom}'s birthday`) })).toBeChecked()
      await sues.getByRole('button', { name: 'One more', exact: true }).click()
      await sues.getByRole('button', { name: "I'll get this", exact: true }).click()
      await expect(h1(sues, 'A kettle')).toBeVisible()
      await expect(sues.getByText(`You claimed 2 for ${mom}'s birthday`, { exact: true })).toBeVisible()
      await expect(sues.getByText('1 of 3 still wanted', { exact: true })).toBeVisible()

      // Giving: change the gift. Bought, a private note, and one fewer.
      await sues.goto('/')
      await sues.getByRole('link', { name: 'Giving', exact: true }).click()
      await expect(h1(sues, 'Giving')).toBeVisible()
      const group1 = sues.getByRole('region', { name: `${mom}, birthday`, exact: true })
      await expect(group1.getByRole('heading', { level: 3, name: 'A kettle (2)', exact: true })).toBeVisible()
      await group1.getByRole('button', { name: 'Bought: A kettle', exact: true }).click()
      await expect(group1.getByRole('button', { name: 'Bought: A kettle', exact: true })).toHaveAttribute('aria-pressed', 'true')
      await group1.getByRole('button', { name: 'Add a note: A kettle', exact: true }).click()
      await group1.getByRole('textbox', { name: 'Private note: A kettle', exact: true }).fill('The blue one')
      await group1.getByRole('button', { name: 'Save note: A kettle', exact: true }).click()
      await expect(group1.getByText('The blue one', { exact: true })).toBeVisible()
      await group1.getByRole('button', { name: 'One fewer: A kettle', exact: true }).click()
      await expect(group1.getByRole('heading', { level: 3, name: 'A kettle', exact: true })).toBeVisible()

      // Mom sees the item and its quantity, and nothing of the claim.
      await moms.reload()
      await expect(h1(moms, 'A kettle')).toBeVisible()
      await expect(moms.getByText(/still wanted|You claimed/)).toHaveCount(0)
      await expect(moms.getByRole('link', { name: "I'll get this", exact: true })).toHaveCount(0)

      // Unclaim: the screen before it, then Giving is empty and the quantity is back.
      await group1.getByRole('link', { name: "I won't get this: A kettle", exact: true }).click()
      await expect(h1(sues, 'Not getting this?')).toBeVisible()
      await sues.getByRole('button', { name: "I won't get this", exact: true }).click()
      await expect(h1(sues, 'Giving')).toBeVisible()
      await expect(sues.getByRole('heading', { level: 2, name: 'Nothing yet', exact: true })).toBeVisible()
      await sues.goto(`/users/${momId}`)
      await expect(sues.getByRole('list', { name: 'Items', exact: true }).getByText('3 of 3 still wanted', { exact: true })).toBeVisible()
    } finally {
      await momDevice.close()
      await sueDevice.close()
    }
  })
})
