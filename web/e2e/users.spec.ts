import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts. The spec makes a user
// with a name of its own, and reads only that user, so other specs can add
// users to the same server.
test.describe('users', () => {
  test.use({ signedIn: true })

  test('the admin creates a user, and the user finishes and edits their profile', async ({ page, browser }) => {
    const first = `Grace${Math.random().toString(36).slice(2, 8)}`

    // Menu's For the admin part leads to the Users screen, which lists everyone.
    await page.goto('/menu')
    await expect(page.getByRole('heading', { level: 2, name: 'For the admin' })).toBeVisible()
    await page.getByRole('link', { name: 'Users' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Users' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Ada Lovelace' })).toBeVisible()

    // New user asks for a name and a birthday, then opens the new user's page.
    await page.getByRole('link', { name: 'New user' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'New user' })).toBeVisible()
    await page.getByLabel('First name').fill(first)
    await page.getByLabel('Month').selectOption('December')
    await page.getByLabel('Day').fill('9')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByRole('heading', { level: 1, name: first })).toBeVisible()
    await expect(page.getByRole('heading', { level: 2, name: `For ${first}` })).toBeVisible()
    await expect(page.getByRole('link', { name: `Edit ${first}'s profile` })).toBeVisible()

    // The admin makes the new user a login link.
    await page.getByRole('link', { name: `Sign ${first} in` }).click()
    await page.getByRole('button', { name: 'Make a login link' }).click()
    await page.getByRole('button', { name: 'Copy the link instead' }).click()
    const url = await page.getByLabel('Login link', { exact: true }).inputValue()

    // The user opens it on their own device. Until they confirm their profile,
    // the finish-profile step comes before everything else.
    const device = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(device)
    const mine = await device.newPage()
    await mine.goto(url)
    await mine.getByRole('button', { name: 'Sign in' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Finish your profile' })).toBeVisible()
    await expect(mine.getByLabel('First name')).toHaveValue(first)
    await expect(mine.getByRole('link', { name: 'Menu' })).toHaveCount(0)
    await mine.getByLabel('Last name (optional)').fill('Hopper')
    await mine.getByRole('button', { name: 'Continue' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Add a photo' })).toBeVisible()
    await mine.getByRole('button', { name: 'Not now' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    await expect(mine.getByText(`Welcome, ${first}.`)).toBeVisible()

    // Reloading does not bring the step back.
    await mine.reload()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()

    // Menu's Your profile opens the profile, where the user edits it.
    await mine.getByRole('link', { name: 'Menu' }).click()
    await expect(mine.getByText(`${first} Hopper`)).toBeVisible()
    await expect(mine.getByRole('heading', { name: 'For the admin' })).toHaveCount(0)
    await mine.getByRole('link', { name: 'Your profile' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Your profile' })).toBeVisible()
    await expect(mine.getByLabel('Last name (optional)')).toHaveValue('Hopper')
    await expect(mine.getByLabel(/time ?zone/i)).toHaveCount(0)
    await mine.getByLabel('Good to know').fill('Likes tea')
    await mine.getByLabel('Last name (optional)').fill('Murray')
    await mine.getByRole('button', { name: 'Save' }).click()
    await expect(mine.getByText('Saved.')).toBeVisible()

    // The admin sees the change, and the Users screen lists the user by name.
    await page.goto('/users')
    await expect(page.getByRole('link', { name: `${first} Murray` })).toBeVisible()

    // The user cannot open the Users screen.
    await mine.goto('/users')
    await expect(mine.getByRole('heading', { level: 1, name: "We can't find that page" })).toBeVisible()
    await device.close()
  })

  test('the admin edits another user, which does not confirm that user’s profile', async ({ page, request }) => {
    const first = `Edith${Math.random().toString(36).slice(2, 8)}`
    const made = await request.post('/api/v2/users', { data: { name: first, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    const { id } = await made.json()

    await page.goto(`/users/${id}/profile`)
    await expect(page.getByRole('heading', { level: 1, name: `${first}'s profile` })).toBeVisible()
    await page.getByLabel('Good to know').fill('Plays the cello')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect(page.getByText('Saved.')).toBeVisible()

    const user = await (await request.get(`/api/v2/users/${id}`)).json()
    expect(user.good_to_know).toBe('Plays the cello')
    expect(user.profile_confirmed_at).toBeNull()
  })

  test('the API pages and versions: the envelope, the ETag, If-Match, and the 422 codes', async ({ request }) => {
    const first = `Hedy${Math.random().toString(36).slice(2, 8)}`
    const made = await request.post('/api/v2/users', { data: { name: first, birthday: { month: 11, day: 9, year: null } } })
    expect(made.status()).toBe(201)
    const { id } = await made.json()
    expect(made.headers().location).toBe(`/api/v2/users/${id}`)

    const list = await (await request.get('/api/v2/users?limit=1')).json()
    expect(list.data).toHaveLength(1)
    expect(typeof list.total_count).toBe('number')
    expect(typeof list.next_cursor).toBe('string')

    const one = await request.get(`/api/v2/users/${id}`)
    const etag = one.headers().etag!
    const all = await (await request.get('/api/v2/users?sort=-created_at')).json()
    expect(all.data.find((u: { id: string }) => u.id === id).version).toBe(etag)

    const stale = await request.patch(`/api/v2/users/${id}`, { data: { name: 'Nope' }, headers: { 'If-Match': '"stale"' } })
    expect(stale.status()).toBe(412)
    expect((await stale.json()).code).toBe('resource_changed')
    const ok = await request.patch(`/api/v2/users/${id}`, {
      data: { last_name: 'Lamarr' },
      headers: { 'If-Match': etag, 'Content-Type': 'application/merge-patch+json' },
    })
    expect(ok.status()).toBe(200)
    expect(ok.headers().etag).not.toBe(etag)

    expect((await (await request.get('/api/v2/users?shade=red')).json()).code).toBe('unknown_filter')
    expect((await (await request.get('/api/v2/users?sort=shade')).json()).code).toBe('unknown_sort')
    const locked = await request.patch(`/api/v2/users/${id}`, { data: { is_admin: true } })
    expect(locked.status()).toBe(403)
    expect((await locked.json()).code).toBe('field_not_writable')
    const noBirthday = await request.patch(`/api/v2/users/${id}`, { data: { birthday: null } })
    expect((await noBirthday.json()).code).toBe('birthday_required')
  })
})
