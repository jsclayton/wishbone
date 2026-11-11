import type { Browser, Page } from '@playwright/test'
import { expect, finishProfile, prepareContext, test } from './support'
import { startServer, type TestServer } from './server'

// A shared occasion reaches the users in the groups it is attached to, or every
// user when the admin marks it everyone. A mark on the shared server would put
// the occasion on every other spec's Home and profile screens, so this spec runs
// a server of its own.
let server: TestServer

test.beforeEach(async () => {
  server = await startServer()
})

test.afterEach(async () => {
  await server.stop()
})

// Birthdays 200 days from now, so none falls in the 60 days that Home looks at.
const away = new Date(Date.now() + 200 * 86_400_000)
const faraway = { month: away.getUTCMonth() + 1, day: away.getUTCDate(), year: null }

const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })

async function setUp(page: Page) {
  await page.goto(server.url)
  await page.getByLabel('Setup code').fill(server.setupCode)
  await page.getByLabel('First name', { exact: true }).fill('Grace')
  await page.getByLabel('Month').selectOption('December')
  await page.getByLabel('Day').fill('9')
  await page.getByRole('button', { name: 'Set up' }).click()
  await finishProfile(page)
}

/** The API of this spec's own server, as the signed-in admin page. */
const api = (page: Page) => ({
  post: (path: string, data: object = {}) => page.request.post(`${server.url}/api/v2${path}`, { data }),
})

async function made(res: Awaited<ReturnType<ReturnType<typeof api>['post']>>) {
  expect(res.status()).toBe(201)
  return (await res.json()) as { id: string; url?: string }
}

/** Signs a user in on a device of their own, with a login link the admin makes, and gets them to Home. */
async function signIn(browser: Browser, admin: Page, userId: string) {
  const link = await made(await api(admin).post(`/users/${userId}/login-links`))
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } })
  await prepareContext(context)
  const page = await context.newPage()
  await page.goto(link.url!)
  await page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await finishProfile(page)
  return { context, page }
}

test('the admin makes a shared occasion, attaches a group, marks it everyone, and deletes it', async ({ page, browser }) => {
  const picnic = 'Family picnic'
  await setUp(page)
  const gia = await made(await api(page).post('/users', { name: 'Gia', birthday: faraway }))
  const ivy = await made(await api(page).post('/users', { name: 'Ivy', birthday: faraway }))
  const hal = await made(await api(page).post('/users', { name: 'Hal', birthday: faraway }))
  await made(await api(page).post('/users', { name: 'Kit', birthday: faraway, parent_ids: [hal.id] }))
  const family = await made(await api(page).post('/groups', { name: 'Family' }))
  await made(await api(page).post(`/groups/${family.id}/users`, { user_id: gia.id }))
  await made(await api(page).post(`/groups/${family.id}/users`, { user_id: ivy.id }))

  // Menu leads to Shared occasions, which has none yet.
  await page.getByRole('link', { name: 'Menu', exact: true }).click()
  await page.getByRole('link', { name: 'Shared occasions', exact: true }).click()
  await expect(h1(page, 'Shared occasions')).toBeVisible()
  await expect(page.getByText('No shared occasions yet.', { exact: true })).toBeVisible()

  // A new occasion 20 days from now opens in place of the form.
  const soon = new Date(Date.now() + 20 * 86_400_000)
  await page.getByRole('link', { name: 'New shared occasion', exact: true }).click()
  await expect(h1(page, 'New shared occasion')).toBeVisible()
  await page.getByLabel("What's it called?", { exact: true }).fill(picnic)
  await page.getByLabel('Month', { exact: true }).selectOption(String(soon.getUTCMonth() + 1))
  await page.getByLabel('Day', { exact: true }).fill(String(soon.getUTCDate()))
  await page.getByRole('button', { name: 'Create this shared occasion', exact: true }).click()
  await expect(h1(page, picnic)).toBeVisible()
  const groups = page.getByRole('region', { name: 'Groups', exact: true })
  await expect(groups.getByText('Not attached to any group.', { exact: true })).toBeVisible()

  // Attach the group from the picker.
  await groups.getByRole('link', { name: 'Attach a group', exact: true }).click()
  await expect(h1(page, `Attach a group to ${picnic}`)).toBeVisible()
  await page.getByRole('button', { name: 'Add Family', exact: true }).click()
  await expect(h1(page, picnic)).toBeVisible()
  await expect(groups.getByRole('listitem').filter({ hasText: 'Family' })).toBeVisible()
  const path = new URL(page.url()).pathname

  const giaDevice = await signIn(browser, page, gia.id)
  const halDevice = await signIn(browser, page, hal.id)
  try {
    // Gia is in the group: the band counts down to it, Coming up lists it, and who is on it opens.
    await expect(giaDevice.page.getByRole('link', { name: "See who's on it", exact: true })).toBeVisible()
    await expect(giaDevice.page.getByText(/^Next up, /)).toBeVisible()
    await expect(giaDevice.page.getByRole('region', { name: 'Coming up', exact: true }).getByRole('link', { name: new RegExp(picnic) })).toBeVisible()
    await giaDevice.page.getByRole('link', { name: "See who's on it", exact: true }).click()
    await expect(h1(giaDevice.page, picnic)).toBeVisible()
    // Who is on it is grouped by Gia's own groups, and leaves Gia out.
    const giaOnIt = giaDevice.page.getByRole('region', { name: "Who's on it", exact: true })
    await expect(giaOnIt.getByRole('region', { name: 'Family', exact: true }).getByRole('link')).toHaveText(['IvyNothing yet'])
    // Gia changes nothing.
    await expect(giaDevice.page.getByRole('link', { name: 'Edit this occasion', exact: true })).toHaveCount(0)
    await expect(giaDevice.page.getByRole('region', { name: 'Groups', exact: true })).toHaveCount(0)

    // Hal is in no group: nothing comes up, and the occasion is a missing page.
    await expect(halDevice.page.getByText('Nothing coming up', { exact: true })).toBeVisible()
    await halDevice.page.goto(`${server.url}${path}`)
    await expect(h1(halDevice.page, "We can't find that page")).toBeVisible()

    // Marked everyone, it reaches Hal too, and the group is detached.
    await page.getByRole('link', { name: 'Edit this occasion', exact: true }).click()
    await expect(h1(page, 'Edit a shared occasion')).toBeVisible()
    await page.getByRole('checkbox', { name: 'Everyone', exact: true }).check()
    await page.getByRole('button', { name: 'Save this shared occasion', exact: true }).click()
    await expect(h1(page, picnic)).toBeVisible()
    await expect(groups.getByText('Everyone is on this occasion, including users who join later.', { exact: true })).toBeVisible()
    await expect(groups.getByRole('link', { name: 'Attach a group', exact: true })).toHaveCount(0)
    await halDevice.page.goto(`${server.url}${path}`)
    await expect(h1(halDevice.page, picnic)).toBeVisible()
    // Hal is in no group, but his child is on it, and Hal sees Kit under Your children.
    const halOnIt = halDevice.page.getByRole('region', { name: "Who's on it", exact: true })
    await expect(halOnIt.getByRole('region', { name: 'Your children', exact: true }).getByRole('link')).toHaveText(['KitNothing yet'])

    // Unmarked, it is attached to no groups, so Gia and Hal both lose it.
    await page.getByRole('link', { name: 'Edit this occasion', exact: true }).click()
    await page.getByRole('checkbox', { name: 'Everyone', exact: true }).uncheck()
    await page.getByRole('button', { name: 'Save this shared occasion', exact: true }).click()
    await expect(groups.getByText('Not attached to any group.', { exact: true })).toBeVisible()
    await halDevice.page.goto(`${server.url}${path}`)
    await expect(h1(halDevice.page, "We can't find that page")).toBeVisible()
    await giaDevice.page.goto(`${server.url}${path}`)
    await expect(h1(giaDevice.page, "We can't find that page")).toBeVisible()

    // Delete asks first.
    await page.getByRole('link', { name: 'Delete this occasion', exact: true }).click()
    await expect(h1(page, `Delete ${picnic}?`)).toBeVisible()
    await page.getByRole('button', { name: 'Delete this occasion', exact: true }).click()
    await expect(h1(page, 'Shared occasions')).toBeVisible()
    await expect(page.getByText('No shared occasions yet.', { exact: true })).toBeVisible()
  } finally {
    await giaDevice.context.close()
    await halDevice.context.close()
  }
})

test('only the admin sees Shared occasions in Menu and may open the screens', async ({ page, browser }) => {
  await setUp(page)
  const gia = await made(await api(page).post('/users', { name: 'Gia', birthday: { month: 5, day: 4, year: null } }))
  const device = await signIn(browser, page, gia.id)
  try {
    await device.page.goto(`${server.url}/menu`)
    await expect(h1(device.page, 'Menu')).toBeVisible()
    await expect(device.page.getByRole('link', { name: 'Shared occasions', exact: true })).toHaveCount(0)
    for (const path of ['/occasions', '/occasions/new']) {
      await device.page.goto(`${server.url}${path}`)
      await expect(h1(device.page, "We can't find that page")).toBeVisible()
    }
    const refused = await device.page.request.post(`${server.url}/api/v2/occasions`, {
      data: { kind: 'shared', name: 'Mine', rule: { kind: 'annual', month: 5, day: 5 } },
    })
    expect(refused.status()).toBe(403)
  } finally {
    await device.context.close()
  }
})
