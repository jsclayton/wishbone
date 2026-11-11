import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts. Each test makes users
// with names of its own and reads only them, so other specs can add users to the
// same server.
test.describe('deactivating and reactivating a user', () => {
  test.use({ signedIn: true })

  test('the admin pauses a child: her link stops working, her parent sees her but cannot act, and reactivation brings her back', async ({
    page,
    browser,
    request,
  }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const mom = `Mom${suffix}`
    const emma = `Emma${suffix}`
    const create = async (name: string, extra: object = {}) => {
      const res = await request.post('/api/v2/users', { data: { name, birthday: { month: 3, day: 4, year: null }, ...extra } })
      expect(res.status()).toBe(201)
      return (await res.json()).id as string
    }
    const momId = await create(mom)
    const emmaId = await create(emma, { parent_ids: [momId] })
    const linkRes = await request.post(`/api/v2/users/${emmaId}/login-links`)
    expect(linkRes.status()).toBe(201)
    const emmaToken = ((await linkRes.json()).url as string).split('/').pop()!
    const check = async () => (await request.post('/api/v2/login-links/check', { data: { token: emmaToken } })).status()
    expect(await check()).toBe(200)

    // Deactivate opens a confirmation. Keeping her leaves her active.
    await page.goto(`/users/${emmaId}`)
    await page.getByRole('region', { name: `For ${emma}` }).getByRole('link', { name: `Deactivate ${emma}` }).click()
    await expect(page.getByRole('heading', { level: 1, name: `Deactivate ${emma}?` })).toBeVisible()
    await page.getByRole('link', { name: `Keep ${emma} active` }).click()
    await expect(page.getByRole('heading', { level: 1, name: emma })).toBeVisible()
    await expect(page.getByText('Deactivated')).toHaveCount(0)

    await page.getByRole('link', { name: `Deactivate ${emma}` }).click()
    await page.getByRole('button', { name: `Deactivate ${emma}` }).click()
    await expect(page.getByRole('heading', { level: 1, name: emma })).toBeVisible()
    await expect(page.getByText('Deactivated')).toBeVisible()
    const part = page.getByRole('region', { name: `For ${emma}` })
    await expect(part.getByRole('button', { name: `Reactivate ${emma}` })).toBeVisible()
    await expect(part.getByRole('link', { name: `Edit ${emma}'s profile` })).toHaveCount(0)
    await expect(part.getByRole('link', { name: `Deactivate ${emma}` })).toHaveCount(0)

    // Her login link doesn't work while she is deactivated.
    expect(await check()).toBe(401)

    // The Users screen shows the label.
    await page.goto('/users')
    await expect(page.getByRole('listitem').filter({ hasText: emma })).toContainText('Deactivated')

    // Mom still sees Emma, but cannot change anything through her.
    const momLink = await request.post(`/api/v2/users/${momId}/login-links`)
    expect(momLink.status()).toBe(201)
    const momDevice = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(momDevice)
    const mine = await momDevice.newPage()
    await mine.goto((await momLink.json()).url)
    await mine.getByRole('button', { name: 'Sign in' }).click()
    await mine.getByRole('button', { name: 'Continue' }).click()
    await mine.getByRole('button', { name: 'Not now' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    const children = mine.getByRole('region', { name: 'Your children' })
    await expect(children.getByRole('link', { name: `${emma} Deactivated` })).toBeVisible()
    await children.getByRole('link', { name: `${emma} Deactivated` }).click()
    await expect(mine.getByRole('heading', { level: 1, name: emma })).toBeVisible()
    await expect(mine.getByText(`Only the admin can reactivate ${emma}. Until then, you can't change anything for ${emma}.`)).toBeVisible()
    await expect(mine.getByRole('region', { name: `For ${emma}` })).toHaveCount(0)
    const edit = await mine.request.patch(`/api/v2/users/${emmaId}`, { data: { good_to_know: 'x' } })
    expect(edit.status()).toBe(403)
    expect((await edit.json()).code).toBe('user_deactivated')

    // Reactivation asks for nothing, and the link that hadn't expired works again.
    await page.goto(`/users/${emmaId}`)
    await page.getByRole('button', { name: `Reactivate ${emma}` }).click()
    await expect(page.getByRole('link', { name: `Deactivate ${emma}` })).toBeVisible()
    await expect(page.getByText('Deactivated')).toHaveCount(0)
    expect(await check()).toBe(200)
    await mine.reload()
    await expect(mine.getByRole('region', { name: `For ${emma}` })).toBeVisible()

    await momDevice.close()
  })
})
