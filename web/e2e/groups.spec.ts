import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect, finishProfile, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts. The spec makes users and
// groups with names of its own, and reads only those, so other specs can add to
// the same server.
test.describe('groups', () => {
  test.use({ signedIn: true })

  const tag = () => Math.random().toString(36).slice(2, 8)

  async function newUser(request: APIRequestContext, name: string): Promise<string> {
    const made = await request.post('/api/v2/users', { data: { name, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    return (await made.json()).id
  }

  /** Signs a user in on a device of their own, with a login link the admin makes, and gets them to Home. */
  async function signIn(browser: Browser, request: APIRequestContext, userId: string) {
    const link = await request.post(`/api/v2/users/${userId}/login-links`)
    expect(link.status()).toBe(201)
    const context = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(context)
    const page = await context.newPage()
    await page.goto((await link.json()).url)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await finishProfile(page)
    return { context, page }
  }

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })

  test('the admin builds a group, two users in it see each other, and a user outside it gets the missing page', async ({ page, browser, request }) => {
    const id = tag()
    const gia = `Gia${id}`
    const hal = `Hal${id}`
    const outsider = `Out${id}`
    const family = `Family ${id}`
    const giaId = await newUser(request, gia)
    const halId = await newUser(request, hal)
    const outsiderId = await newUser(request, outsider)

    // Menu leads to Groups. The admin creates a group, which opens in place of the form.
    await page.goto('/menu')
    await page.getByRole('link', { name: 'Groups', exact: true }).click()
    await expect(h1(page, 'Groups')).toBeVisible()
    await page.getByRole('link', { name: 'Create a group' }).click()
    await page.getByLabel('Group name').fill(family)
    await page.getByRole('button', { name: 'Create a group' }).click()
    await expect(h1(page, family)).toBeVisible()

    // Users are added from a list, and the admin goes back to the group.
    for (const name of [gia, hal]) {
      await page.getByRole('link', { name: 'Add a user' }).click()
      await expect(h1(page, `Add a user to ${family}`)).toBeVisible()
      await page.getByRole('button', { name: `Add ${name}` }).click()
      await expect(h1(page, family)).toBeVisible()
    }
    const users = page.getByRole('region', { name: 'Users', exact: true })
    await expect(users.getByRole('listitem').filter({ hasText: gia })).toBeVisible()
    await expect(users.getByRole('listitem').filter({ hasText: hal })).toBeVisible()
    await expect(users.getByRole('listitem').filter({ hasText: outsider })).toHaveCount(0)

    const first = await signIn(browser, request, giaId)
    const second = await signIn(browser, request, halId)
    const third = await signIn(browser, request, outsiderId)
    try {
      // Gia sees Hal in People on Home, and Hal's page opens.
      const people = first.page.getByRole('region', { name: 'People', exact: true })
      await expect(people.getByRole('region', { name: family, exact: true }).getByRole('link', { name: hal })).toBeVisible()
      await people.getByRole('link', { name: hal }).click()
      await expect(h1(first.page, hal)).toBeVisible()

      // Hal sees Gia the same way.
      await expect(second.page.getByRole('region', { name: 'People', exact: true }).getByRole('link', { name: gia })).toBeVisible()

      // A user outside the group has no People, and Gia's page is the missing-page message.
      await expect(third.page.getByRole('region', { name: 'People', exact: true })).toHaveCount(0)
      await third.page.goto(`/users/${giaId}`)
      await expect(h1(third.page, "We can't find that page")).toBeVisible()
      await third.page.goto(`/groups/${(await groupId(request, family))}`)
      await expect(h1(third.page, "We can't find that page")).toBeVisible()

      // A user in the group sees its users and has nothing to change.
      await first.page.goto('/menu')
      await first.page.getByRole('link', { name: 'Groups', exact: true }).click()
      await first.page.getByRole('link', { name: family }).click()
      await expect(h1(first.page, family)).toBeVisible()
      await expect(first.page.getByRole('region', { name: 'Users', exact: true }).getByRole('link', { name: hal })).toBeVisible()
      await expect(first.page.getByRole('button', { name: 'Rename' })).toHaveCount(0)
      await expect(first.page.getByRole('link', { name: 'Add a user' })).toHaveCount(0)
      expect((await first.page.request.patch(`/api/v2/groups/${await groupId(request, family)}`, { data: { name: 'Nope' } })).status()).toBe(403)

      // The admin renames the group, and its users see the new name.
      const renamed = `Clan ${id}`
      await page.getByRole('button', { name: 'Rename' }).click()
      await page.getByLabel('Group name').fill(renamed)
      await page.getByRole('button', { name: 'Save' }).click()
      await expect(h1(page, renamed)).toBeVisible()
      await first.page.goto('/')
      await expect(first.page.getByRole('region', { name: 'People', exact: true }).getByRole('region', { name: renamed, exact: true })).toBeVisible()

      // The admin takes Hal out, and Gia can no longer see Hal.
      await page.getByRole('button', { name: `Remove ${hal}` }).click()
      await expect(users.getByRole('listitem').filter({ hasText: hal })).toHaveCount(0)
      await first.page.goto(`/users/${halId}`)
      await expect(h1(first.page, "We can't find that page")).toBeVisible()
    } finally {
      await first.context.close()
      await second.context.close()
      await third.context.close()
    }
  })

  test('the admin nests a group, a loop is refused, and a group is deleted', async ({ page, browser, request }) => {
    const id = tag()
    const gia = `Gia${id}`
    const inner = `Inner ${id}`
    const outer = `Outer ${id}`
    const giaId = await newUser(request, gia)
    const outerId = await groupMade(request, outer)
    const innerId = await groupMade(request, inner)
    await request.post(`/api/v2/groups/${innerId}/users`, { data: { user_id: giaId } })

    // Inner goes inside Outer from the picker.
    await page.goto(`/groups/${outerId}`)
    await page.getByRole('link', { name: 'Add a group' }).click()
    await expect(h1(page, `Add a group to ${outer}`)).toBeVisible()
    await page.getByRole('button', { name: `Add ${inner}` }).click()
    await expect(h1(page, outer)).toBeVisible()
    await expect(page.getByRole('region', { name: 'Groups inside this one' }).getByRole('listitem').filter({ hasText: inner })).toBeVisible()

    // Outer inside Inner would be a loop.
    await page.goto(`/groups/${innerId}/groups`)
    await page.getByRole('button', { name: `Add ${outer}` }).click()
    await expect(page.getByRole('alert')).toHaveText(`Can't add ${outer}. ${inner} is already inside ${outer}, and a group can't end up inside itself.`)
    await expect(h1(page, `Add a group to ${inner}`)).toBeVisible()

    // Gia is in Inner, and so in Outer, through Inner.
    const device = await signIn(browser, request, giaId)
    try {
      await device.page.goto('/groups')
      await expect(device.page.getByRole('link', { name: new RegExp(`^${outer}`) })).toContainText(`You're in this group through ${inner}.`)
      await expect(device.page.getByRole('link', { name: new RegExp(`^${inner}`) })).toContainText("You're in this group.")
    } finally {
      await device.context.close()
    }

    // Deleting a group asks first. Keep it goes back.
    await page.goto(`/groups/${innerId}`)
    await page.getByRole('link', { name: 'Delete this group' }).click()
    await expect(h1(page, `Delete ${inner}?`)).toBeVisible()
    await page.getByRole('link', { name: 'Keep it' }).click()
    await expect(h1(page, inner)).toBeVisible()
    await page.getByRole('link', { name: 'Delete this group' }).click()
    await page.getByRole('button', { name: 'Delete this group' }).click()
    await expect(h1(page, 'Groups')).toBeVisible()
    await expect(page.getByRole('link', { name: new RegExp(`^${inner}`) })).toHaveCount(0)
    await page.goto(`/groups/${innerId}`)
    await expect(h1(page, "We can't find that page")).toBeVisible()
  })

  test('People shows the users in the groups inside a group, with through', async ({ browser, request }) => {
    const id = tag()
    const gia = `Gia${id}`
    const hal = `Hal${id}`
    const inner = `Inner ${id}`
    const outer = `Outer ${id}`
    const giaId = await newUser(request, gia)
    const halId = await newUser(request, hal)
    const outerId = await groupMade(request, outer)
    const innerId = await groupMade(request, inner)
    expect((await request.post(`/api/v2/groups/${outerId}/groups`, { data: { group_id: innerId } })).status()).toBe(201)
    expect((await request.post(`/api/v2/groups/${outerId}/users`, { data: { user_id: giaId } })).status()).toBe(201)
    expect((await request.post(`/api/v2/groups/${innerId}/users`, { data: { user_id: halId } })).status()).toBe(201)

    const first = await signIn(browser, request, giaId)
    const second = await signIn(browser, request, halId)
    try {
      // Gia is directly in Outer, and sees Hal there, who is in Inner inside it.
      const giaPeople = first.page.getByRole('region', { name: 'People', exact: true })
      await expect(giaPeople.getByRole('region', { name: outer, exact: true }).getByRole('link', { name: hal })).toBeVisible()
      // Gia is not in Inner, so Inner is not hers.
      await expect(giaPeople.getByRole('region', { name: inner, exact: true })).toHaveCount(0)

      // Hal is in Inner, and in Outer through Inner. Outer shows Gia, with through.
      const halPeople = second.page.getByRole('region', { name: 'People', exact: true })
      const outerPart = halPeople.getByRole('region', { name: outer, exact: true })
      await expect(outerPart.getByRole('link', { name: gia })).toBeVisible()
      await expect(outerPart.getByText(`through ${inner}`)).toBeVisible()
      // Inner has nobody but Hal, so it is left out.
      await expect(halPeople.getByRole('region', { name: inner, exact: true })).toHaveCount(0)
    } finally {
      await first.context.close()
      await second.context.close()
    }
  })

  async function groupMade(request: APIRequestContext, name: string): Promise<string> {
    const made = await request.post('/api/v2/groups', { data: { name } })
    expect(made.status()).toBe(201)
    return (await made.json()).id
  }

  async function groupId(request: APIRequestContext, name: string): Promise<string> {
    const list = await (await request.get('/api/v2/groups?limit=200')).json()
    return list.data.find((g: { name: string; id: string }) => g.name === name).id
  }

  test('the API pages groups, versions them, and refuses what it should', async ({ request }) => {
    const id = tag()
    const made = await request.post('/api/v2/groups', { data: { name: `Zed ${id}` } })
    expect(made.status()).toBe(201)
    const group = await made.json()
    expect(made.headers().location).toBe(`/api/v2/groups/${group.id}`)

    const one = await request.get(`/api/v2/groups/${group.id}`)
    const etag = one.headers().etag!
    const list = await (await request.get('/api/v2/groups?sort=-name')).json()
    expect(list.data.find((g: { id: string }) => g.id === group.id).version).toBe(etag)
    expect((await (await request.get('/api/v2/groups?shade=red')).json()).code).toBe('unknown_filter')

    const stale = await request.patch(`/api/v2/groups/${group.id}`, { data: { name: 'Nope' }, headers: { 'If-Match': '"stale"' } })
    expect(stale.status()).toBe(412)
    const ok = await request.patch(`/api/v2/groups/${group.id}`, { data: { name: `Zee ${id}` }, headers: { 'If-Match': etag } })
    expect(ok.status()).toBe(200)

    const loop = await request.post(`/api/v2/groups/${group.id}/groups`, { data: { group_id: group.id } })
    expect(loop.status()).toBe(422)
    expect((await loop.json()).code).toBe('group_loop')
    const noSuchUser = await request.post(`/api/v2/groups/${group.id}/users`, { data: { user_id: '999999' } })
    expect((await noSuchUser.json()).code).toBe('reference_not_found')

    expect((await request.delete(`/api/v2/groups/${group.id}`, { headers: { 'If-Match': etag } })).status()).toBe(412)
    expect((await request.delete(`/api/v2/groups/${group.id}`)).status()).toBe(204)
    expect((await request.delete(`/api/v2/groups/${group.id}`)).status()).toBe(404)
  })
})
