import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server.
test.describe('invitations', () => {
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

  test('a recipient invites a user, who accepts and then leaves', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, dad, house] = [`Mom${suffix}`, `Dad${suffix}`, `House${suffix}`]
    const momId = await createUser(request, mom)
    const dadId = await createUser(request, dad)
    // Mom and Dad share a group, so Mom sees Dad and can invite him.
    const group = await request.post('/api/v2/groups', { data: { name: `Family ${suffix}` } })
    expect(group.status()).toBe(201)
    const groupId = (await group.json()).id as string
    for (const id of [momId, dadId]) {
      expect((await request.post(`/api/v2/groups/${groupId}/users`, { data: { user_id: id } })).status()).toBe(201)
    }

    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: dadDevice, page: dads } = await signInAs(browser, request, dadId)
    try {
      // Mom makes a list and invites Dad from its page, with no confirmation.
      await moms.getByRole('link', { name: 'My lists', exact: true }).click()
      await moms.getByRole('link', { name: 'New list', exact: true }).click()
      await moms.getByLabel('Name', { exact: true }).fill(house)
      await moms.getByRole('button', { name: 'Save', exact: true }).click()
      await expect(h1(moms, house)).toBeVisible()
      const listPath = new URL(moms.url()).pathname
      await moms.getByRole('link', { name: 'Invite', exact: true }).click()
      await expect(h1(moms, `Invite someone to ${house}`)).toBeVisible()
      await moms.getByRole('button', { name: `Invite ${dad}`, exact: true }).click()
      await expect(h1(moms, house)).toBeVisible()
      await expect(moms.getByRole('list', { name: 'Invitations', exact: true }).getByRole('listitem')).toHaveText(`${dad} · invited by ${mom}Withdraw`)

      // Dad's Home counts the invitation. My lists shows it above his lists.
      await dads.reload()
      await expect(dads.getByRole('link', { name: 'My lists · 1 invitation', exact: true })).toBeVisible()
      await dads.getByRole('link', { name: 'My lists · 1 invitation', exact: true }).click()
      await expect(h1(dads, 'My lists')).toBeVisible()
      await expect(dads.getByRole('link', { name: `${mom} invited you to ${house}`, exact: true })).toBeVisible()
      await dads.getByRole('button', { name: 'Accept', exact: true }).click()
      await expect(dads.getByRole('list', { name: 'Invitations', exact: true })).toHaveCount(0)
      await expect(dads.getByRole('list', { name: 'Your lists', exact: true }).getByRole('link', { name: new RegExp(`^${house}\\s*No items$`) })).toBeVisible()

      // Both are recipients now. Dad leaves: the confirmation warns him, because he still sees the list through Mom.
      await dads.goto(listPath)
      await expect(h1(dads, house)).toBeVisible()
      await expect(dads.getByText(mom, { exact: true })).toBeVisible()
      await dads.getByRole('link', { name: 'Leave', exact: true }).click()
      await expect(h1(dads, 'Leave this list?')).toBeVisible()
      await expect(dads.getByText("You'll see what's been claimed on this list, including gifts for you.", { exact: true })).toBeVisible()
      await dads.getByRole('button', { name: `Leave ${house}`, exact: true }).click()
      await expect(h1(dads, 'My lists')).toBeVisible()
      await expect(dads.getByRole('list', { name: 'Your lists', exact: true }).getByRole('link', { name: new RegExp(house) })).toHaveCount(0)

      // Mom is the only recipient again: she can't leave, and can delete.
      await moms.reload()
      await expect(h1(moms, house)).toBeVisible()
      await expect(moms.getByText(dad, { exact: true })).toHaveCount(0)
      await expect(moms.getByRole('link', { name: 'Leave', exact: true })).toHaveCount(0)
      await expect(moms.getByRole('link', { name: 'Delete list', exact: true })).toBeVisible()
    } finally {
      await momDevice.close()
      await dadDevice.close()
    }
  })

  test('a recipient withdraws an invitation, and the invited user declines another', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, dad] = [`Mom${suffix}`, `Dad${suffix}`]
    const momId = await createUser(request, mom)
    const dadId = await createUser(request, dad)
    const group = await request.post('/api/v2/groups', { data: { name: `Family ${suffix}` } })
    const groupId = (await group.json()).id as string
    for (const id of [momId, dadId]) {
      expect((await request.post(`/api/v2/groups/${groupId}/users`, { data: { user_id: id } })).status()).toBe(201)
    }
    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: dadDevice, page: dads } = await signInAs(browser, request, dadId)
    try {
      for (const name of ['Garden', 'Trip']) {
        await moms.goto('/lists/new')
        await moms.getByLabel('Name', { exact: true }).fill(`${name}${suffix}`)
        await moms.getByRole('button', { name: 'Save', exact: true }).click()
        await expect(h1(moms, `${name}${suffix}`)).toBeVisible()
        await moms.getByRole('link', { name: 'Invite', exact: true }).click()
        await moms.getByRole('button', { name: `Invite ${dad}`, exact: true }).click()
        await expect(h1(moms, `${name}${suffix}`)).toBeVisible()
        if (name === 'Garden') {
          // Withdraw needs no confirmation, and Dad can be invited again.
          await moms.getByRole('button', { name: `Withdraw ${dad}'s invitation`, exact: true }).click()
          await expect(moms.getByRole('list', { name: 'Invitations', exact: true })).toHaveCount(0)
        }
      }

      await dads.goto('/lists')
      await expect(dads.getByRole('list', { name: 'Invitations', exact: true }).getByRole('listitem')).toHaveCount(1)
      await expect(dads.getByRole('link', { name: `${mom} invited you to Trip${suffix}`, exact: true })).toBeVisible()
      await dads.getByRole('button', { name: 'Decline', exact: true }).click()
      await expect(dads.getByRole('list', { name: 'Invitations', exact: true })).toHaveCount(0)
      await dads.goto('/')
      await expect(dads.getByRole('link', { name: 'My lists', exact: true })).toBeVisible()
    } finally {
      await momDevice.close()
      await dadDevice.close()
    }
  })
})
