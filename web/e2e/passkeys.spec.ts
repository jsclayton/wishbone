import type { Browser, BrowserContext, Page } from '@playwright/test'
import { expect, finishProfile, prepareContext, test } from './support'

// The CDP virtual authenticator exists only in Chromium.
test.skip(({ browserName }) => browserName !== 'chromium', 'The CDP virtual authenticator works only in Chromium.')

// The admin only makes the user and looks at the user's passkeys. The user is
// new for each test, so other specs on the shared server are not affected.
test.use({ signedIn: true })

/** Gives the page's browser a platform authenticator that finds discoverable credentials and verifies the user. */
async function virtualAuthenticator(context: BrowserContext, page: Page) {
  const cdp = await context.newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  })
}

/** A new user, whom the admin makes a login link for. Returns the user's ID and the link. */
async function newUser(admin: Page, first: string) {
  const made = await admin.request.post('/api/v2/users', { data: { name: first, birthday: { month: 5, day: 6, year: null } } })
  expect(made.status()).toBe(201)
  const { id } = await made.json()
  const link = await admin.request.post(`/api/v2/users/${id}/login-links`)
  expect(link.status()).toBe(201)
  return { id: id as string, url: (await link.json()).url as string }
}

/** Signs a new browser in with the link and finishes the profile. It has a virtual authenticator. */
async function signInWithLink(browser: Browser, url: string) {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } })
  await prepareContext(context)
  const page = await context.newPage()
  await virtualAuthenticator(context, page)
  await page.goto(url)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await finishProfile(page)
  return { context, page }
}

const passkeysPart = (page: Page) => page.locator('section').filter({ has: page.getByRole('heading', { level: 2, name: 'Passkeys' }) })

test('a user adds a passkey, renames it, signs out and in with it, and the admin deletes it', async ({ page, browser }) => {
  const first = `Passkey${Math.random().toString(36).slice(2, 8)}`
  const { id, url } = await newUser(page, first)
  const { context, page: mine } = await signInWithLink(browser, url)
  try {
    // Menu leads to Your devices, which has the passkeys part.
    await mine.getByRole('link', { name: 'Menu' }).click()
    await mine.getByRole('link', { name: 'Your devices' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Your devices' })).toBeVisible()
    const part = passkeysPart(mine)
    await expect(part.getByText('No passkeys yet.')).toBeVisible()

    await part.getByRole('button', { name: 'Add a passkey' }).click()
    const rows = part.getByRole('listitem')
    await expect(rows).toHaveCount(1)
    await expect(rows.first()).toContainText('Added')
    await expect(rows.first()).toContainText('Never used')

    // Rename. The name sticks after a reload.
    await rows.first().getByRole('button', { name: /^Rename / }).click()
    await rows.first().getByLabel('Name').fill('Kitchen laptop')
    await rows.first().getByRole('button', { name: 'Save' }).click()
    await expect(rows.first().getByText('Kitchen laptop')).toBeVisible()
    await mine.reload()
    await expect(part.getByText('Kitchen laptop')).toBeVisible()

    // The same authenticator can't add a second passkey for this user.
    await part.getByRole('button', { name: 'Add a passkey' }).click()
    await expect(part.getByRole('alert')).toContainText('already has a passkey')
    await expect(rows).toHaveCount(1)

    // Sign out. The signed-out page names no one and offers the passkey.
    await mine.getByRole('link', { name: 'Menu' }).click()
    await mine.getByRole('button', { name: 'Log out' }).click()
    const signIn = mine.getByRole('button', { name: 'Sign in with passkey' })
    await expect(signIn).toBeVisible()
    await expect(mine.getByText(first)).toHaveCount(0)

    await signIn.click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    await expect(mine.getByText(`Welcome, ${first}.`)).toBeVisible()

    // The session survives a reload, and the passkey records its use.
    await mine.reload()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
    await mine.goto(`/users/${id}/sign-in`)
    await expect(part.getByText('Kitchen laptop')).toBeVisible()
    await expect(rows.first()).toContainText('Last used')
    await expect(rows.first()).not.toContainText('Never used')

    // The admin sees the passkey, with Delete and without Rename or Add a passkey.
    await page.goto(`/users/${id}/sign-in`)
    const adminPart = passkeysPart(page)
    await expect(adminPart.getByText('Kitchen laptop')).toBeVisible()
    await expect(adminPart.getByRole('button', { name: /^Rename / })).toHaveCount(0)
    await expect(adminPart.getByRole('button', { name: 'Add a passkey' })).toHaveCount(0)

    // Deleting the last passkey asks first, in words for someone else.
    await adminPart.getByRole('link', { name: 'Delete Kitchen laptop' }).click()
    await expect(page.getByRole('heading', { level: 1, name: `Delete ${first}'s last passkey?` })).toBeVisible()
    await expect(page.getByText(`${first} will need a new link or QR code to sign in on a new device.`)).toBeVisible()
    await page.getByRole('link', { name: 'Keep it' }).click()
    await expect(adminPart.getByText('Kitchen laptop')).toBeVisible()
    await adminPart.getByRole('link', { name: 'Delete Kitchen laptop' }).click()
    await page.getByRole('button', { name: 'Delete this passkey' }).click()
    await expect(page.getByRole('heading', { level: 1, name: new RegExp(`^Sign ${first} in$`) })).toBeVisible()
    await expect(adminPart.getByText('No passkeys yet.')).toBeVisible()

    // The deleted passkey no longer signs in.
    const other = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(other)
    const gone = await other.newPage()
    try {
      await virtualAuthenticator(other, gone)
      await gone.goto('/')
      await gone.getByRole('button', { name: 'Sign in with passkey' }).click()
      await expect(gone.getByRole('heading', { level: 1, name: "That didn't work" })).toBeVisible()
      await gone.getByRole('button', { name: 'Try again' }).click()
      await expect(gone.getByRole('button', { name: 'Sign in with passkey' })).toBeVisible()
    } finally {
      await other.close()
    }
  } finally {
    await context.close()
  }
})

test('a user who deletes their own last passkey is told what happens', async ({ page, browser }) => {
  const first = `Own${Math.random().toString(36).slice(2, 8)}`
  const { id, url } = await newUser(page, first)
  const { context, page: mine } = await signInWithLink(browser, url)
  try {
    await mine.goto(`/users/${id}/sign-in`)
    const part = passkeysPart(mine)
    await part.getByRole('button', { name: 'Add a passkey' }).click()
    await expect(part.getByRole('listitem')).toHaveCount(1)

    await part.getByRole('link', { name: /^Delete / }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Delete your last passkey?' })).toBeVisible()
    await expect(mine.getByText("You'll need a new link or QR code to get back in.")).toBeVisible()
    await mine.getByRole('button', { name: 'Delete this passkey' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Your devices' })).toBeVisible()
    await expect(part.getByText('No passkeys yet.')).toBeVisible()
  } finally {
    await context.close()
  }
})
