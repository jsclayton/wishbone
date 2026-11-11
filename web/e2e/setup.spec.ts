import { expect, finishProfile, test } from './support'
import { startServer, type TestServer } from './server'

// A new instance, set up in the browser with the code from its log, and then
// signed out again. It needs a server of its own: the shared one is already set
// up, and signing out would end the session that every other spec uses.
let server: TestServer

test.beforeEach(async () => {
  server = await startServer()
})

test.afterEach(async () => {
  await server.stop()
})

test('the operator sets up a new instance, signs in as the admin, and logs out', async ({ page }) => {
  await page.goto(server.url)
  await expect(page.getByRole('heading', { level: 1, name: 'Set up Wishbone' })).toBeVisible()

  // A wrong code is refused and nothing is made.
  await page.getByLabel('Setup code').fill('AAAA-AAAA-AAAA')
  await page.getByLabel('First name', { exact: true }).fill('Grace')
  await page.getByLabel('Month').selectOption('December')
  await page.getByLabel('Day').fill('9')
  await page.getByRole('button', { name: 'Set up' }).click()
  await expect(page.getByRole('alert')).toContainText("That setup code isn't right")

  // The right code makes the admin and signs them in.
  await page.getByLabel('Setup code').fill(server.setupCode)
  await page.getByLabel('Last name (optional)').fill('Hopper')
  await page.getByRole('button', { name: 'Set up' }).click()

  // The admin has not confirmed their profile yet, so the finish-profile step
  // comes first, filled in with what setup took. It stays until they finish.
  await expect(page.getByRole('heading', { level: 1, name: 'Finish your profile' })).toBeVisible()
  await expect(page.getByLabel('First name')).toHaveValue('Grace')
  await expect(page.getByLabel('Last name (optional)')).toHaveValue('Hopper')
  await page.reload()
  await expect(page.getByRole('heading', { level: 1, name: 'Finish your profile' })).toBeVisible()
  await finishProfile(page)
  await expect(page.getByText('Welcome, Grace.')).toBeVisible()

  // The session lasts: a reload is still Home.
  await page.reload()
  await expect(page.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()

  await page.getByRole('link', { name: 'Menu' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Menu' })).toBeVisible()
  await expect(page.getByText('Grace Hopper')).toBeVisible()
  await page.getByRole('button', { name: 'Log out' }).click()

  await expect(page.getByRole('heading', { level: 1, name: 'Welcome to Wishbone' })).toBeVisible()
  await expect(page).toHaveURL(`${server.url}/`)
  await page.reload()
  await expect(page.getByRole('heading', { level: 1, name: 'Welcome to Wishbone' })).toBeVisible()
})

test('setup cannot run twice', async ({ page, request }) => {
  await page.goto(server.url)
  await page.getByLabel('Setup code').fill(server.setupCode)
  await page.getByLabel('First name', { exact: true }).fill('Grace')
  await page.getByLabel('Month').selectOption('December')
  await page.getByLabel('Day').fill('9')
  await page.getByRole('button', { name: 'Set up' }).click()
  await finishProfile(page)

  const again = await request.post(`${server.url}/api/v2/setup`, {
    data: { setup_code: server.setupCode, name: 'Eve', birthday: { month: 1, day: 1 } },
  })
  expect(again.status()).toBe(409)
  expect(await again.json()).toMatchObject({ code: 'setup_complete' })
  expect(await (await request.get(`${server.url}/api/v2/setup`)).json()).toMatchObject({ is_needed: false })
})
