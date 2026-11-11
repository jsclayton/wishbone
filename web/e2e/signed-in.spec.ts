import { expect, test } from './support'

// The shared server's admin, set up by global-setup.ts.
test.describe('signed in', () => {
  test.use({ signedIn: true })

  test('Home is the front page, and Menu has Home and Log out', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    await expect(page.getByText('Welcome, Ada.')).toBeVisible()

    await page.getByRole('link', { name: 'Menu' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Menu' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Home' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible()

    await page.getByRole('link', { name: 'Home' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
  })

  test('a path that no screen matches opens Home', async ({ page }) => {
    for (const path of ['/nothing/here', '/list', '/people/3']) {
      await page.goto(path)
      await expect(page.getByRole('heading', { level: 1, name: 'Home' }), path).toBeVisible()
    }
  })

  test('the API knows the caller by the cookie, and me is the caller', async ({ request }) => {
    const me = await request.get('/api/v2/users/me')
    expect(me.status()).toBe(200)
    const user = await me.json()
    expect(user).toMatchObject({ name: 'Ada', last_name: 'Lovelace', is_admin: true, birthday: { month: 2, day: 29, year: null } })
    expect(typeof user.id).toBe('string')

    const byId = await request.get(`/api/v2/users/${user.id}`)
    expect(await byId.json()).toEqual(user)
  })
})

test('the API answers 401 with a code to a caller with no session', async ({ request }) => {
  const res = await request.get('/api/v2/users/me')
  expect(res.status()).toBe(401)
  expect(res.headers()['content-type']).toContain('application/problem+json')
  expect(await res.json()).toMatchObject({ status: 401, code: 'unauthorized' })

  const logOut = await request.delete('/api/v2/users/me/sessions/current')
  expect(logOut.status()).toBe(401)
})

test('the OpenAPI document has both ways to sign in', async ({ request }) => {
  const doc = await (await request.get('/api/v2/openapi')).json()
  expect(Object.keys(doc.components.securitySchemes).sort()).toEqual(['bearerAuth', 'cookieAuth'])
})
