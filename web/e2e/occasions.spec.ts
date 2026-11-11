import type { Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server.
test.describe('occasions on the profile screen', () => {
  test.use({ signedIn: true })

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })
  const rows = (page: Page) => page.getByRole('list', { name: 'Occasions', exact: true }).getByRole('listitem')

  test('a parent adds, edits and deletes custom occasions for themselves and their child, and the admin sees only birthdays', async ({
    page,
    browser,
    request,
  }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, emma] = [`Mom${suffix}`, `Emma${suffix}`]
    const made = await request.post('/api/v2/users', { data: { name: mom, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    const momId = (await made.json()).id as string
    const child = await request.post('/api/v2/users', { data: { name: emma, birthday: { month: 6, day: 7, year: null }, parent_ids: [momId] } })
    expect(child.status()).toBe(201)
    const emmaId = (await child.json()).id as string

    // The admin's access gives Mom's birthday, and no way to add an occasion for her.
    await page.goto(`/users/${momId}/profile`)
    await expect(h1(page, `${mom}'s profile`)).toBeVisible()
    await expect(rows(page)).toHaveCount(1)
    await expect(rows(page).first()).toContainText('Birthday')
    await expect(page.getByRole('link', { name: 'Add an occasion', exact: true })).toHaveCount(0)

    const link = await request.post(`/api/v2/users/${momId}/login-links`)
    expect(link.status()).toBe(201)
    const device = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(device)
    const mine = await device.newPage()
    try {
      await mine.goto((await link.json()).url)
      await mine.getByRole('button', { name: 'Sign in', exact: true }).click()
      await mine.getByRole('button', { name: 'Continue', exact: true }).click()
      await mine.getByRole('button', { name: 'Not now', exact: true }).click()
      await expect(h1(mine, 'Home')).toBeVisible()

      // Mom's own profile starts with her birthday, from the birthday above.
      await mine.goto(`/users/${momId}/profile`)
      await expect(h1(mine, 'Your profile')).toBeVisible()
      await expect(rows(mine)).toHaveCount(1)
      await expect(rows(mine).first()).toContainText('From the birthday above')
      await expect(mine.getByRole('link', { name: /^Edit /})).toHaveCount(0)

      // Add an occasion, and the profile shows it.
      await mine.getByRole('link', { name: 'Add an occasion', exact: true }).click()
      await expect(h1(mine, 'Add an occasion')).toBeVisible()
      await mine.getByLabel("What's it called?", { exact: true }).fill('Wedding day')
      await mine.getByLabel('Month', { exact: true }).selectOption('June')
      await mine.getByLabel('Day', { exact: true }).fill('1')
      await mine.getByRole('button', { name: 'Add this occasion', exact: true }).click()
      await expect(h1(mine, 'Your profile')).toBeVisible()
      await expect(rows(mine)).toHaveCount(2)
      await expect(rows(mine).filter({ hasText: 'Wedding day' })).toContainText('Every year on June 1')

      // Edit it: a new name and a one-time date. Delete asks first.
      await mine.getByRole('link', { name: 'Edit Wedding day', exact: true }).click()
      await expect(h1(mine, 'Edit an occasion')).toBeVisible()
      await expect(mine.getByLabel("What's it called?", { exact: true })).toHaveValue('Wedding day')
      await mine.getByLabel("What's it called?", { exact: true }).fill('Anniversary')
      await mine.getByRole('button', { name: /^One time only/ }).click()
      await mine.getByLabel('Date', { exact: true }).fill('2041-02-03')
      await mine.getByRole('button', { name: 'Save this occasion', exact: true }).click()
      await expect(h1(mine, 'Your profile')).toBeVisible()
      await expect(rows(mine).filter({ hasText: 'Wedding day' })).toHaveCount(0)
      await expect(rows(mine).filter({ hasText: 'Anniversary' })).toContainText('Feb 3, 2041')

      await mine.getByRole('link', { name: 'Edit Anniversary', exact: true }).click()
      await mine.getByRole('link', { name: 'Delete this occasion', exact: true }).click()
      await expect(h1(mine, 'Delete this occasion?')).toBeVisible()
      await mine.getByRole('link', { name: 'Keep it', exact: true }).click()
      await expect(h1(mine, 'Edit an occasion')).toBeVisible()
      await mine.getByRole('link', { name: 'Delete this occasion', exact: true }).click()
      await mine.getByRole('button', { name: 'Delete this occasion', exact: true }).click()
      await expect(h1(mine, 'Your profile')).toBeVisible()
      await expect(rows(mine)).toHaveCount(1)

      // A parent adds one for the child, on the child's profile.
      await mine.goto(`/users/${emmaId}/profile`)
      await expect(h1(mine, `${emma}'s profile`)).toBeVisible()
      await mine.getByRole('link', { name: 'Add an occasion', exact: true }).click()
      await expect(h1(mine, `Add an occasion for ${emma}`)).toBeVisible()
      await mine.getByLabel("What's it called?", { exact: true }).fill('Recital')
      await mine.getByRole('button', { name: /^A set weekday every year/ }).click()
      await mine.getByLabel('Which', { exact: true }).selectOption('2nd')
      await mine.getByLabel('Day', { exact: true }).selectOption('Sunday')
      await mine.getByLabel('Month', { exact: true }).selectOption('May')
      await mine.getByRole('button', { name: 'Add this occasion', exact: true }).click()
      await expect(h1(mine, `${emma}'s profile`)).toBeVisible()
      await expect(rows(mine).filter({ hasText: 'Recital' })).toContainText('2nd Sunday of May, every year')

      // The admin still sees only Emma's birthday.
      await page.goto(`/users/${emmaId}/profile`)
      await expect(h1(page, `${emma}'s profile`)).toBeVisible()
      await expect(rows(page)).toHaveCount(1)
      await expect(rows(page).first()).toContainText('Birthday')
    } finally {
      await device.close()
    }
  })
})
