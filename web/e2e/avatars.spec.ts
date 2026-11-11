import type { APIRequestContext, Browser, Locator } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts. Each test makes a user
// of its own and signs that user in on a device of its own, so it never touches
// the admin's session or another spec's users.
test.use({ signedIn: true })

// A 2x2 red PNG.
const redPng = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAG0lEQVR4nAAOAPH/BP8AAAAAAAIAAAAAAAADAA1HAQZvRqE8AAAAAElFTkSuQmCC',
  'base64',
)
// A 1x1 PNG.
const pixel = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')

const loaded = (img: Locator) => img.evaluate((el: { complete: boolean; naturalWidth: number }) => el.complete && el.naturalWidth > 0)

/** Makes a user as the admin, and opens their login link on a new device, signed out. */
async function newUserOnANewDevice(request: APIRequestContext, browser: Browser, name: string) {
  const made = await request.post('/api/v2/users', { data: { name, birthday: { month: 5, day: 6, year: null } } })
  expect(made.status()).toBe(201)
  const { id } = await made.json()
  const link = await request.post(`/api/v2/users/${id}/login-links`)
  expect(link.status()).toBe(201)
  const { url } = await link.json()

  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } })
  await prepareContext(context)
  const page = await context.newPage()
  await page.goto(url)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Finish your profile' })).toBeVisible()
  return { id: id as string, context, page }
}

test('a new user adds a photo at first sign-in, then changes and removes it on the profile', async ({ page, request, browser }) => {
  const first = `Photo${Math.random().toString(36).slice(2, 8)}`
  const { id, context, page: mine } = await newUserOnANewDevice(request, browser, first)
  try {
    // The profile step comes first. The photo step has no top row, so nobody
    // wanders off before the profile is done.
    const topRow = mine.getByRole('navigation', { name: 'Top' })
    await expect(topRow).toHaveCount(0)
    await mine.getByRole('button', { name: 'Continue' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Add a photo' })).toBeVisible()
    await expect(mine.getByText('Help people recognize you')).toBeVisible()
    await expect(topRow).toHaveCount(0)
    await expect(mine.locator('main [data-initials]')).toHaveAttribute('data-initials', first[0]!.toUpperCase())

    await mine.locator('input[type="file"]').setInputFiles({ name: 'me.png', mimeType: 'image/png', buffer: redPng })
    await expect(mine.getByRole('button', { name: 'Use this photo' })).toBeVisible()
    await expect.poll(() => loaded(mine.locator('main img'))).toBe(true)
    await mine.getByRole('button', { name: 'Use this photo' }).click()

    // Home greets the user with the photo.
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    await expect(mine.getByText(`Welcome, ${first}.`)).toBeVisible()
    await expect.poll(() => loaded(mine.locator('main img'))).toBe(true)

    // The admin sees it next to the user's name in Users.
    await page.goto('/users')
    const row = page.getByRole('link', { name: first })
    await expect(row).toBeVisible()
    await expect.poll(() => loaded(row.locator('img'))).toBe(true)

    // The profile changes the photo, and removes it. The initials come back.
    await mine.goto(`/users/${id}/profile`)
    await expect(mine.getByRole('heading', { level: 1, name: 'Your profile' })).toBeVisible()
    await expect(mine.getByRole('button', { name: 'Change photo' })).toBeVisible()
    await mine.getByLabel('Change photo').setInputFiles({ name: 'other.png', mimeType: 'image/png', buffer: pixel })
    await expect(mine.getByRole('button', { name: 'Remove photo' })).toBeVisible()
    await expect.poll(() => loaded(mine.locator('main img'))).toBe(true)

    await mine.getByRole('button', { name: 'Remove photo' }).click()
    await expect(mine.getByRole('button', { name: 'Add a photo' })).toBeVisible()
    await expect(mine.locator('main img')).toHaveCount(0)
    await expect(mine.locator('main [data-initials]')).toHaveAttribute('data-initials', first[0]!.toUpperCase())

    await page.reload()
    await expect(page.getByRole('link', { name: first }).locator('img')).toHaveCount(0)
    const user = await (await request.get(`/api/v2/users/${id}`)).json()
    expect(user.avatar).toBeNull()
  } finally {
    await context.close()
  }
})

test('Not now skips the photo, and the step fits the narrowest phone', async ({ request, browser }) => {
  const first = `Skip${Math.random().toString(36).slice(2, 8)}`
  const { id, context, page: mine } = await newUserOnANewDevice(request, browser, first)
  try {
    await mine.setViewportSize({ width: 320, height: 700 })
    await mine.getByRole('button', { name: 'Continue' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Add a photo' })).toBeVisible()

    // Each step is its own history entry, so Back goes to the step before.
    await mine.goBack()
    await expect(mine.getByRole('heading', { level: 1, name: 'Finish your profile' })).toBeVisible()
    await expect(mine.getByLabel('First name')).toHaveValue(first)
    await mine.goForward()
    await expect(mine.getByRole('heading', { level: 1, name: 'Add a photo' })).toBeVisible()

    // A 180px circle with the initials, in both colour schemes, and no sideways scroll.
    for (const scheme of ['light', 'dark'] as const) {
      await mine.emulateMedia({ colorScheme: scheme })
      const circle = mine.locator('main [data-initials]')
      await expect.poll(async () => (await circle.boundingBox())?.width).toBe(180)
      expect((await circle.boundingBox())?.height).toBe(180)
      await expect.poll(() => mine.locator('html').evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    }

    await mine.getByRole('button', { name: 'Not now' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    await expect(mine.locator('main img')).toHaveCount(0)
    await mine.reload()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
  } finally {
    await context.close()
  }
  expect(id).toBeTruthy()
})

test('the API refuses a file that is not an image, and keeps nobody waiting for it', async ({ request }) => {
  const bad = await request.post('/api/v2/images', { data: Buffer.from('not an image'), headers: { 'Content-Type': 'image/png' } })
  expect(bad.status()).toBe(422)
  expect((await bad.json()).code).toBe('image_invalid')

  const ok = await request.post('/api/v2/images', { data: redPng, headers: { 'Content-Type': 'image/png' } })
  expect(ok.status()).toBe(201)
  const image = await ok.json()
  expect(ok.headers().location).toBe(image.url)
  expect(image).toMatchObject({ width: 2, height: 2, content_type: 'image/jpeg' })
  const bytes = await request.get(image.url)
  expect(bytes.status()).toBe(200)
  expect(bytes.headers()['content-type']).toBe('image/jpeg')
  expect(bytes.headers()['cache-control']).toBe('private, max-age=31536000, immutable')
})
