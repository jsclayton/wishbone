import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server.
test.describe('gift flags', () => {
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

  test('a giver sees what a recipient changed, keeps a removed gift, and drops a gift whose list was deleted', async ({ browser, request }) => {
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
      // Mom has two items and an extra list with one, and Sue claims all three.
      const primary = (await (await moms.request.get('/api/v2/lists?is_primary=true&can_edit=true')).json()).data[0].id as string
      const garden = await moms.request.post('/api/v2/lists', { data: { name: `Garden ${suffix}` } })
      expect(garden.status()).toBe(201)
      const gardenId = (await garden.json()).id as string
      const add = async (listId: string, title: string) => {
        const made = await moms.request.post(`/api/v2/lists/${listId}/items`, { data: { title } })
        expect(made.status()).toBe(201)
        return (await made.json()).id as string
      }
      const [kettle, mug, rake] = [await add(primary, 'Kettle'), await add(primary, 'Mug'), await add(gardenId, 'Rake')]
      for (const id of [kettle, mug, rake]) {
        expect((await sues.request.post(`/api/v2/items/${id}/gifts`, { data: {} })).status()).toBe(201)
      }

      // Mom renames the kettle and deletes the mug and the garden.
      expect((await moms.request.patch(`/api/v2/items/${kettle}`, { data: { title: 'Teapot' } })).status()).toBe(200)
      expect((await moms.request.delete(`/api/v2/items/${mug}`)).status()).toBe(204)
      expect((await moms.request.delete(`/api/v2/lists/${gardenId}`)).status()).toBe(204)

      // Home names the gifts that need attention, and what happened to them.
      await sues.goto('/')
      const attention = sues.getByRole('region', { name: 'Not bought yet', exact: true })
      await expect(attention.getByRole('link', { name: /^Teapot for / })).toContainText('Changed since you picked it')
      await expect(attention.getByRole('link', { name: /^Mug for / })).toContainText('Removed from the list')
      await expect(attention.getByRole('link', { name: /^Rake for / })).toContainText('Removed from the list')

      // Giving shows each flag with its words and its buttons.
      await sues.getByRole('link', { name: 'Giving', exact: true }).click()
      await expect(h1(sues, 'Giving')).toBeVisible()
      const teapot = sues.getByRole('listitem').filter({ has: sues.getByRole('heading', { level: 3, name: 'Teapot', exact: true }) })
      await expect(teapot.getByText('Title went from “Kettle” to “Teapot”.', { exact: false })).toBeVisible()
      await teapot.getByRole('button', { name: 'Got it, changes: Teapot', exact: true }).click()
      await expect(teapot.getByText('Changed since you picked it.', { exact: true })).toHaveCount(0)

      // Keeping a removed gift answers the flag, and the gift stays.
      const kept = sues.getByRole('listitem').filter({ has: sues.getByRole('heading', { level: 3, name: 'Mug', exact: true }) })
      await expect(kept.getByText('Removed from the list.', { exact: true })).toBeVisible()
      await kept.getByRole('button', { name: 'Keep it: Mug', exact: true }).click()
      await expect(kept.getByText('Removed from the list.', { exact: true })).toHaveCount(0)
      await expect(sues.getByRole('heading', { level: 3, name: 'Mug', exact: true })).toBeVisible()

      // The gift on the deleted list is flagged the same way, and dropping it unclaims it.
      const dropped = sues.getByRole('listitem').filter({ has: sues.getByRole('heading', { level: 3, name: 'Rake', exact: true }) })
      await expect(dropped.getByText('Removed from the list.', { exact: true })).toBeVisible()
      await dropped.getByRole('link', { name: 'Drop it: Rake', exact: true }).click()
      await expect(h1(sues, 'Not getting this?')).toBeVisible()
      await sues.getByRole('button', { name: "I won't get this", exact: true }).click()
      await expect(h1(sues, 'Giving')).toBeVisible()
      await expect(sues.getByRole('heading', { level: 3, name: 'Rake', exact: true })).toHaveCount(0)
      await expect(sues.getByRole('heading', { level: 3, name: 'Mug', exact: true })).toBeVisible()

      // Mom sees none of it.
      await moms.goto('/giving')
      await expect(moms.getByText(/Changed since you picked it|Removed from the list/)).toHaveCount(0)
    } finally {
      await momDevice.close()
      await sueDevice.close()
    }
  })
})
