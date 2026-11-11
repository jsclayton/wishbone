import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server.
test.describe('gifts when the recipients of a list change', () => {
  test.use({ signedIn: true })

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })

  async function createUser(request: APIRequestContext, name: string) {
    const made = await request.post('/api/v2/users', { data: { name, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    return (await made.json()).id as string
  }

  async function group(request: APIRequestContext, name: string, userIds: string[]) {
    const made = await request.post('/api/v2/groups', { data: { name } })
    expect(made.status()).toBe(201)
    const id = (await made.json()).id as string
    for (const user_id of userIds) expect((await request.post(`/api/v2/groups/${id}/users`, { data: { user_id } })).status()).toBe(201)
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

  /** Mom makes an extra list "House" with a grill on it, and invites Dad, who accepts. Returns the list and the grill. */
  async function house(moms: Page, dads: Page, suffix: string, dadId: string) {
    const made = await moms.request.post('/api/v2/lists', { data: { name: `House ${suffix}` } })
    expect(made.status()).toBe(201)
    const listId = (await made.json()).id as string
    const invited = await moms.request.post(`/api/v2/lists/${listId}/invitations`, { data: { user_id: dadId } })
    expect(invited.status()).toBe(201)
    expect((await dads.request.post(`/api/v2/invitations/${(await invited.json()).id}/accept`)).status()).toBe(200)
    const grill = await moms.request.post(`/api/v2/lists/${listId}/items`, { data: { title: 'Grill' } })
    expect(grill.status()).toBe(201)
    return { listId, itemId: (await grill.json()).id as string }
  }

  test('a giver is told who the gift is for now, and that it moved to the next recipient', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, dad, sue] = [`Mom${suffix}`, `Dad${suffix}`, `Sue${suffix}`]
    const [momId, dadId, sueId] = [await createUser(request, mom), await createUser(request, dad), await createUser(request, sue)]
    await group(request, `Cousins ${suffix}`, [momId, dadId, sueId])
    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: dadDevice, page: dads } = await signInAs(browser, request, dadId)
    const { device: sueDevice, page: sues } = await signInAs(browser, request, sueId)
    try {
      const { listId, itemId } = await house(moms, dads, suffix, dadId)
      // Sue claims the grill for Dad's birthday.
      const options = await (await sues.request.get(`/api/v2/occasions?list_id=${listId}`)).json()
      const dadsBirthday = options.data.find((o: { kind: string; user: { id: string } | null }) => o.kind === 'birthday' && o.user?.id === dadId)
      expect((await sues.request.post(`/api/v2/items/${itemId}/gifts`, { data: { occasion_id: dadsBirthday.id } })).status()).toBe(201)

      // Dad leaves the list.
      expect((await dads.request.delete(`/api/v2/lists/${listId}/recipients/${dadId}`)).status()).toBe(204)

      await sues.goto('/giving')
      await expect(h1(sues, 'Giving')).toBeVisible()
      const grill = sues.getByRole('listitem').filter({ has: sues.getByRole('heading', { level: 3, name: 'Grill', exact: true }) })
      await expect(grill.getByText(`Now for ${mom}.`, { exact: true })).toBeVisible()
      await expect(grill.getByText(`Moved to ${mom}'s birthday.`, { exact: true })).toBeVisible()
      await grill.getByRole('button', { name: 'Got it, recipients: Grill', exact: true }).click()
      await expect(grill.getByText(`Now for ${mom}.`, { exact: true })).toHaveCount(0)
      await grill.getByRole('button', { name: 'Got it, occasion: Grill', exact: true }).click()
      await expect(grill.getByText(`Moved to ${mom}'s birthday.`, { exact: true })).toHaveCount(0)
      await expect(sues.getByRole('heading', { level: 3, name: 'Grill', exact: true })).toBeVisible()
    } finally {
      await momDevice.close()
      await dadDevice.close()
      await sueDevice.close()
    }
  })

  test('a giver who sees none of the recipients any more is told, and cannot pick an occasion', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, dad, sue] = [`Mom${suffix}`, `Dad${suffix}`, `Sue${suffix}`]
    const [momId, dadId, sueId] = [await createUser(request, mom), await createUser(request, dad), await createUser(request, sue)]
    // Sue sees Mom, and only Mom, on "House".
    await group(request, `Parents ${suffix}`, [momId, dadId])
    await group(request, `Cousins ${suffix}`, [momId, sueId])
    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: dadDevice, page: dads } = await signInAs(browser, request, dadId)
    const { device: sueDevice, page: sues } = await signInAs(browser, request, sueId)
    try {
      const { listId, itemId } = await house(moms, dads, suffix, dadId)
      expect((await sues.request.post(`/api/v2/items/${itemId}/gifts`, { data: {} })).status()).toBe(201)

      // Mom leaves the list, so Sue sees none of its recipients.
      expect((await moms.request.delete(`/api/v2/lists/${listId}/recipients/${momId}`)).status()).toBe(204)

      await sues.goto('/giving')
      await expect(h1(sues, 'Giving')).toBeVisible()
      const grill = sues.getByRole('listitem').filter({ has: sues.getByRole('heading', { level: 3, name: 'Grill', exact: true }) })
      await expect(grill.getByText('No longer shared with you.', { exact: true })).toBeVisible()
      await expect(grill.getByText('The recipients changed.', { exact: true })).toBeVisible()
      await expect(grill.getByText('Moved to another occasion.', { exact: true })).toBeVisible()
      // Only the day it is given shows of the occasion.
      await expect(sues.getByRole('region', { name: /, hidden until / })).toBeVisible()
      await grill.getByRole('button', { name: 'Change occasion: Grill', exact: true }).click()
      await expect(grill.getByRole('alert')).toHaveText('You can see none of the other recipients, so the occasion is picked for you.')
      await grill.getByRole('button', { name: 'Got it, no longer shared: Grill', exact: true }).click()
      await expect(grill.getByText('No longer shared with you.', { exact: true })).toHaveCount(0)
    } finally {
      await momDevice.close()
      await dadDevice.close()
      await sueDevice.close()
    }
  })
})
