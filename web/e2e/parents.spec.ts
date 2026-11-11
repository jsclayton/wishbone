import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts. Each test makes users
// with names of its own and reads only them, so other specs can add users to the
// same server.
test.describe('parents and children', () => {
  test.use({ signedIn: true })

  test('the admin links a parent, who signs the child in with a link and edits the child’s profile', async ({ page, browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const mom = `Mom${suffix}`
    const emma = `Emma${suffix}`
    const made = await request.post('/api/v2/users', { data: { name: mom, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    const momId = (await made.json()).id as string

    // The New user form takes optional parents. Saving opens the new user's page.
    await page.goto('/users/new')
    await page.getByLabel('First name').fill(emma)
    await page.getByLabel('Month').selectOption('March')
    await page.getByLabel('Day').fill('4')
    await page.getByRole('group', { name: 'Parents (optional)' }).getByLabel(mom).check()
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('heading', { level: 1, name: emma })).toBeVisible()
    const emmaId = page.url().split('/').pop()!

    // The admin's Parents part removes a parent at once, and Add parent lists the others.
    const parents = page.getByRole('region', { name: `For ${emma}` }).getByRole('region', { name: 'Parents' })
    await expect(parents.getByRole('listitem')).toHaveText([`${mom}Remove`])
    await parents.getByRole('button', { name: `Remove ${mom}` }).click()
    await expect(parents.getByText('No parents.')).toBeVisible()
    await parents.getByRole('link', { name: 'Add parent' }).click()
    await expect(page.getByRole('heading', { level: 1, name: `Add a parent for ${emma}` })).toBeVisible()
    await expect(page.getByRole('button', { name: `Add ${mom}` })).toBeVisible()
    await expect(page.getByRole('button', { name: `Add ${emma}` })).toHaveCount(0)
    await page.getByRole('button', { name: `Add ${mom}` }).click()
    await expect(page.getByRole('heading', { level: 1, name: emma })).toBeVisible()
    await expect(parents.getByRole('listitem')).toHaveText([`${mom}Remove`])

    // The parent signs in with a link from the admin. People leads to the child.
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
    await mine.getByRole('region', { name: 'Your children' }).getByRole('link', { name: emma }).click()
    await expect(mine.getByRole('heading', { level: 1, name: emma })).toBeVisible()
    await expect(mine.getByRole('region', { name: `For ${emma}` })).toBeVisible()
    await expect(mine.getByRole('heading', { name: 'Parents' })).toHaveCount(0)

    // The parent edits the child's profile.
    await mine.getByRole('link', { name: `Edit ${emma}'s profile` }).click()
    await expect(mine.getByRole('heading', { level: 1, name: `${emma}'s profile` })).toBeVisible()
    await mine.getByLabel('Good to know').fill('Loves penguins')
    await mine.getByRole('button', { name: 'Save' }).click()
    await expect(mine.getByText('Saved.')).toBeVisible()
    const edited = await (await request.get(`/api/v2/users/${emmaId}`)).json()
    expect(edited.good_to_know).toBe('Loves penguins')
    expect(edited.profile_confirmed_at).toBeNull()

    // The parent makes the child a login link, and the child signs in with it.
    await mine.goto(`/users/${emmaId}`)
    await mine.getByRole('link', { name: `Sign ${emma} in` }).click()
    await mine.getByRole('button', { name: 'Make a login link' }).click()
    await mine.getByRole('button', { name: 'Copy the link instead' }).click()
    const url = await mine.getByLabel('Login link', { exact: true }).inputValue()

    const emmaDevice = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(emmaDevice)
    const hers = await emmaDevice.newPage()
    await hers.goto(url)
    await hers.getByRole('button', { name: 'Sign in' }).click()
    await expect(hers.getByRole('heading', { level: 1, name: 'Finish your profile' })).toBeVisible()
    await expect(hers.getByLabel('First name')).toHaveValue(emma)
    await hers.getByRole('button', { name: 'Continue' }).click()
    await hers.getByRole('button', { name: 'Not now' }).click()
    await expect(hers.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    // Emma sees her parent, and the parent sees Emma's sessions.
    await expect(hers.getByRole('region', { name: 'Your parents' }).getByRole('link', { name: mom })).toBeVisible()
    await mine.reload()
    await expect(mine.getByRole('listitem')).toHaveCount(1)

    // Unlinking takes the parent's powers at once.
    const unlinked = await request.patch(`/api/v2/users/${emmaId}`, { data: { parent_ids: [] } })
    expect(unlinked.status()).toBe(200)
    await mine.goto(`/users/${emmaId}`)
    await expect(mine.getByRole('heading', { level: 1, name: "We can't find that page" })).toBeVisible()

    await momDevice.close()
    await emmaDevice.close()
  })

  test('the API refuses a loop and a parent for the admin, and only the admin sets parents', async ({ request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const create = async (name: string, extra: object = {}) => {
      const res = await request.post('/api/v2/users', { data: { name: `${name}${suffix}`, birthday: { month: 1, day: 2, year: null }, ...extra } })
      expect(res.status()).toBe(201)
      return (await res.json()).id as string
    }
    const grandma = await create('Grandma')
    const dad = await create('Dad', { parent_ids: [grandma] })

    const loop = await request.patch(`/api/v2/users/${grandma}`, { data: { parent_ids: [dad] } })
    expect(loop.status()).toBe(422)
    expect((await loop.json()).code).toBe('parent_loop')

    const me = await (await request.get('/api/v2/users/me')).json()
    const adminChild = await request.patch(`/api/v2/users/${me.id}`, { data: { parent_ids: [dad] } })
    expect(adminChild.status()).toBe(422)
    expect((await adminChild.json()).code).toBe('admin_has_no_parents')

    const missing = await request.patch(`/api/v2/users/${dad}`, { data: { parent_ids: ['999999'] } })
    expect(missing.status()).toBe(422)
    expect((await missing.json()).code).toBe('reference_not_found')
  })
})
