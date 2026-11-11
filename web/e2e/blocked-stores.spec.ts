import { expect, finishProfile, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts. The store is unique to
// each run, so the spec doesn't touch any other spec, and it allows the store
// again at the end.
test.describe('blocked stores', () => {
  test.use({ signedIn: true })

  test('the admin blocks a store and allows it again, and another user cannot reach it', async ({ page, browser, request }) => {
    const tag = Math.random().toString(36).slice(2, 8)
    const store = `scam-${tag}.example`

    // A user who is not the admin, on a device of their own.
    const made = await request.post('/api/v2/users', { data: { name: `Gia${tag}`, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    const link = await request.post(`/api/v2/users/${(await made.json()).id}/login-links`)
    expect(link.status()).toBe(201)
    const context = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(context)
    const other = await context.newPage()
    let blockedId: string | undefined
    try {
      await other.goto((await link.json()).url)
      await other.getByRole('button', { name: 'Sign in' }).click()
      await finishProfile(other)

      // The admin finds the screen in Menu's part for the admin, and blocks the store from a pasted address.
      await page.goto('/menu')
      await page.getByRole('link', { name: 'Blocked stores' }).click()
      await expect(page.getByRole('heading', { level: 1, name: 'Blocked stores' })).toBeVisible()
      await page.getByLabel('Block a store').fill(`https://www.${store.toUpperCase()}/p/1`)
      await page.getByRole('button', { name: 'Block' }).click()
      await expect(page.getByRole('button', { name: `Allow ${store}` })).toBeVisible()
      await expect(page.getByLabel('Block a store')).toHaveValue('')
      const listed = await (await request.get('/api/v2/blocked-stores')).json()
      blockedId = listed.data.find((s: { store: string }) => s.store === store).id

      // Blocking it again changes nothing. The API says so, and the screen takes it as done.
      const again = await request.post('/api/v2/blocked-stores', { data: { store } })
      expect(again.status()).toBe(409)
      expect((await again.json()).code).toBe('already_blocked')
      await page.getByLabel('Block a store').fill(store)
      await page.getByRole('button', { name: 'Block' }).click()
      await expect(page.getByLabel('Block a store')).toHaveValue('')
      await expect(page.getByRole('alert')).toHaveCount(0)

      // Text that is not a store is refused with the server's words.
      await page.getByLabel('Block a store').fill('nonsense')
      await page.getByRole('button', { name: 'Block' }).click()
      await expect(page.getByRole('alert')).toContainText('such as temu.com')

      // The other user gets the missing page and a 403 from the API.
      await other.goto('/blocked-stores')
      await expect(other.getByRole('heading', { level: 1, name: "We can't find that page" })).toBeVisible()
      expect((await other.request.get('/api/v2/blocked-stores')).status()).toBe(403)

      // The admin allows the store again.
      await page.reload()
      await page.getByRole('button', { name: `Allow ${store}` }).click()
      await expect(page.getByRole('button', { name: `Allow ${store}` })).toHaveCount(0)
      blockedId = undefined
    } finally {
      if (blockedId !== undefined) await request.delete(`/api/v2/blocked-stores/${blockedId}`)
      await context.close()
    }
  })
})
