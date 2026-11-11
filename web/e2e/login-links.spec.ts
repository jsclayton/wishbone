import { expect, finishProfile, prepareContext, test } from './support'
import { startServer, type TestServer } from './server'

// These specs make and end sessions of the admin, which every spec shares on
// the shared server. Each runs its own server instead.
let server: TestServer

test.beforeEach(async () => {
  server = await startServer()
})

test.afterEach(async () => {
  await server.stop()
})

test('a login link signs another browser in, and the first browser revokes that session', async ({ page, browser }) => {
  // The admin sets up the instance in this browser.
  await page.goto(server.url)
  await page.getByLabel('Setup code').fill(server.setupCode)
  await page.getByLabel('First name', { exact: true }).fill('Grace')
  await page.getByLabel('Month').selectOption('December')
  await page.getByLabel('Day').fill('9')
  await page.getByRole('button', { name: 'Set up' }).click()
  await finishProfile(page)

  // Menu leads to "Your devices", which lists this browser's session.
  await page.getByRole('link', { name: 'Menu' }).click()
  await page.getByRole('link', { name: 'Your devices' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Your devices' })).toBeVisible()
  await expect(page.getByRole('listitem')).toHaveCount(1)
  await expect(page.getByText('This device, in use now')).toBeVisible()

  // A login link shows as a QR code first, and the link once it is copied.
  await page.getByRole('button', { name: 'Make a login link' }).click()
  await expect(page.getByRole('img', { name: 'QR code for the login link' })).toBeVisible()
  await expect(page.getByLabel('Login link', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Copy the link instead' }).click()
  const url = await page.getByLabel('Login link', { exact: true }).inputValue()
  expect(url).toMatch(new RegExp(`^${server.url}/login/.+`))

  // Another browser opens the link, which does not use it up, and taps Sign in.
  const other = await browser.newContext({ storageState: { cookies: [], origins: [] } })
  await prepareContext(other)
  const otherPage = await other.newPage()
  await otherPage.goto(url)
  await expect(otherPage.getByText('Tap the button to sign in. This link works once.')).toBeVisible()
  await otherPage.reload()
  await otherPage.getByRole('button', { name: 'Sign in' }).click()
  await expect(otherPage.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
  await expect(otherPage.getByText('Welcome, Grace.')).toBeVisible()

  // The link worked once.
  const again = await other.newPage()
  await again.goto(url)
  await expect(again.getByRole('heading', { level: 1, name: "This link doesn't work" })).toBeVisible()

  // The first browser now lists two sessions, and revokes the other one.
  await page.reload()
  await expect(page.getByRole('listitem')).toHaveCount(2)
  const elsewhere = page.getByRole('listitem').filter({ hasText: 'Last used' })
  await elsewhere.getByRole('button', { name: 'Revoke' }).click()
  await expect(page.getByRole('listitem')).toHaveCount(1)
  await expect(page.getByText('This device, in use now')).toBeVisible()

  // The other browser is signed out.
  await otherPage.reload()
  await expect(otherPage.getByRole('heading', { level: 1, name: 'Welcome to Wishbone' })).toBeVisible()
  await other.close()
})

test('a native app redeems a link for a bearer token, and the user revokes it from the web', async ({ page, request }) => {
  await page.goto(server.url)
  await page.getByLabel('Setup code').fill(server.setupCode)
  await page.getByLabel('First name', { exact: true }).fill('Grace')
  await page.getByLabel('Month').selectOption('December')
  await page.getByLabel('Day').fill('9')
  await page.getByRole('button', { name: 'Set up' }).click()
  await finishProfile(page)
  await page.getByRole('link', { name: 'Menu' }).click()
  await page.getByRole('link', { name: 'Your devices' }).click()
  await page.getByRole('button', { name: 'Make a login link' }).click()
  await page.getByRole('button', { name: 'Copy the link instead' }).click()
  const url = await page.getByLabel('Login link', { exact: true }).inputValue()
  const token = url.slice(url.lastIndexOf('/') + 1)

  // The app checks the link, then redeems it for a bearer token.
  const check = await request.post(`${server.url}/api/v2/login-links/check`, { data: { token } })
  expect(check.status()).toBe(200)
  expect(await check.json()).toMatchObject({ for_current_user: false })
  const redeem = await request.post(`${server.url}/api/v2/login-links/redeem`, {
    data: { token, session: 'bearer' },
    headers: { 'User-Agent': 'Wishbone iOS/1.0' },
  })
  expect(redeem.status()).toBe(200)
  expect(redeem.headers()['set-cookie']).toBeUndefined()
  const body = await redeem.json()
  expect(body).toMatchObject({ name: 'Grace', initials: 'G', replaced_session: false, switched_user: false })
  const auth = { Authorization: `Bearer ${body.token}` }
  expect((await request.get(`${server.url}/api/v2/users/me`, { headers: auth })).status()).toBe(200)

  // The web app lists it by the name the app gave, and Revoke ends it.
  await page.reload()
  const app = page.getByRole('listitem').filter({ hasText: 'Wishbone iOS' })
  await expect(app).toBeVisible()
  await app.getByRole('button', { name: 'Revoke' }).click()
  await expect(app).toHaveCount(0)
  expect((await request.get(`${server.url}/api/v2/users/me`, { headers: auth })).status()).toBe(401)
})
