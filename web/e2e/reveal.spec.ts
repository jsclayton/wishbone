import type { APIRequestContext, Browser, Page } from '@playwright/test'
import { expect, prepareContext, test } from './support'

// The shared server's admin, set up by global-setup.ts, makes users with names
// of their own and reads only them, so other specs can share the server. The
// e2e build gives a user's gifts as if the clock were ahead
// (POST /api/e2e/reveal), and moves no clock that other specs read.
test.describe('reveal', () => {
  test.use({ signedIn: true })

  const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name, exact: true })

  async function createUser(request: APIRequestContext, name: string) {
    const made = await request.post('/api/v2/users', { data: { name, birthday: { month: 5, day: 4, year: null } } })
    expect(made.status()).toBe(201)
    return (await made.json()).id as string
  }

  /** Signs a user in on a device of their own, through a login link from the admin, and finishes their profile. */
  async function signInAs(browser: Browser, request: APIRequestContext, userId: string) {
    const link = await request.post(`/api/v2/users/${userId}/login-links`)
    expect(link.status()).toBe(201)
    const device = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(device)
    const page = await device.newPage()
    await page.goto((await link.json()).url)
    await page.getByRole('button', { name: 'Sign in', exact: true }).click()
    await page.getByRole('button', { name: 'Continue', exact: true }).click()
    await page.getByRole('button', { name: 'Not now', exact: true }).click()
    await expect(h1(page, 'Home')).toBeVisible()
    return { device, page }
  }

  /** "May 4, 2027": the date as Received and Given write it. */
  const written = (iso: string) => new Date(`${iso}T00:00:00Z`).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })

  test('a gift is claimed, given at reveal, and then in the recipient’s Received and the giver’s Given', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, sue] = [`Mom${suffix}`, `Sue${suffix}`]
    const momId = await createUser(request, mom)
    const sueId = await createUser(request, sue)
    const group = await request.post('/api/v2/groups', { data: { name: `Cousins ${suffix}` } })
    expect(group.status()).toBe(201)
    const groupId = (await group.json()).id as string
    for (const id of [momId, sueId]) {
      expect((await request.post(`/api/v2/groups/${groupId}/users`, { data: { user_id: id } })).status()).toBe(201)
    }

    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: sueDevice, page: sues } = await signInAs(browser, request, sueId)
    try {
      // Mom adds a kettle. Sue claims it for Mom's birthday, from Emma, with a private note.
      await moms.getByRole('textbox', { name: 'Add to my list', exact: true }).fill('A kettle')
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, 'Add an item')).toBeVisible()
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, mom)).toBeVisible()

      await sues.getByRole('region', { name: `Cousins ${suffix}`, exact: true }).getByRole('link', { name: mom, exact: true }).click()
      await sues.getByRole('list', { name: 'Items', exact: true }).getByText('A kettle', { exact: true }).click()
      await sues.getByRole('link', { name: "I'll get this", exact: true }).click()
      await sues.getByRole('button', { name: "I'll get this", exact: true }).click()
      await expect(sues.getByText(`You claimed 1 for ${mom}'s birthday`, { exact: true })).toBeVisible()
      await sues.goto('/giving')
      const giving = sues.getByRole('region', { name: `${mom}, birthday`, exact: true })
      await giving.getByRole('button', { name: 'Add a note: A kettle', exact: true }).click()
      await giving.getByRole('textbox', { name: 'Private note: A kettle', exact: true }).fill('The blue one')
      await giving.getByRole('button', { name: 'Save note: A kettle', exact: true }).click()
      await expect(giving.getByText('The blue one', { exact: true })).toBeVisible()

      // Before reveal, Mom has nothing in Received.
      await moms.goto('/received')
      await expect(h1(moms, 'Received')).toBeVisible()
      await expect(moms.getByRole('heading', { level: 2, name: 'Nothing here yet', exact: true })).toBeVisible()

      // Reveal, as if a year had passed.
      const open = await sues.evaluate(async () => (await (await fetch('/api/v2/gifts?giver_id=me&state=open')).json()) as { data: { occasion_date: string }[] })
      expect(open.data).toHaveLength(1)
      const reveal = await request.post('/api/e2e/reveal', { data: { recipient: Number(momId), advance: '10000h' } })
      expect(reveal.status()).toBe(200)
      expect(await reveal.json()).toEqual({ revealed: 1 })
      const date = written(open.data[0]!.occasion_date)

      // Received: what was given and who gave it, for Mom, who is the only recipient.
      await moms.goto('/menu')
      await moms.getByRole('link', { name: 'Received', exact: true }).click()
      await expect(h1(moms, 'Received')).toBeVisible()
      const received = moms.getByRole('region', { name: `Your birthday, ${date}`, exact: true })
      await expect(received.getByRole('heading', { level: 3, name: 'A kettle', exact: true })).toBeVisible()
      await expect(received.getByText(`From ${sue}`, { exact: true })).toBeVisible()
      await expect(moms.getByText('The blue one')).toHaveCount(0)
      // The server leaves out what is the giver's own.
      const seen = await moms.evaluate(async () => (await (await fetch('/api/v2/gifts?recipient_id=me&sort=-given_at')).json()) as { data: Record<string, unknown>[] })
      expect(seen.data).toHaveLength(1)
      for (const key of ['note', 'is_bought', 'flags', 'needs_attention', 'created_at']) expect(seen.data[0]).not.toHaveProperty(key)
      // The whole quantity was given, so the kettle left Mom's list.
      const lists = await moms.evaluate(
        async (id) => (await (await fetch(`/api/v2/lists?is_primary=true&recipient_id=${id}`)).json()) as { data: { id: string }[] },
        momId,
      )
      await moms.goto(`/lists/${lists.data[0]!.id}`)
      await expect(h1(moms, mom)).toBeVisible()
      await expect(moms.getByText('A kettle', { exact: true })).toHaveCount(0)

      // Given: the giver's view of the same gift, with their note.
      await sues.goto('/giving')
      await expect(sues.getByRole('heading', { level: 2, name: 'Nothing yet', exact: true })).toBeVisible()
      await sues.getByRole('link', { name: 'Menu', exact: true }).click()
      await sues.getByRole('link', { name: 'Given', exact: true }).click()
      await expect(h1(sues, 'Given')).toBeVisible()
      const given = sues.getByRole('region', { name: `Birthday, ${date}`, exact: true })
      await expect(given.getByRole('heading', { level: 3, name: 'A kettle', exact: true })).toBeVisible()
      await expect(given.getByText(`To ${mom}`, { exact: true })).toBeVisible()
      await expect(given.getByText('The blue one', { exact: true })).toBeVisible()
    } finally {
      await momDevice.close()
      await sueDevice.close()
    }
  })

  test('a recipient says they didn’t get a gift and it opens again for its giver, and Add again puts it on a list', async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const [mom, sue] = [`Mom${suffix}`, `Sue${suffix}`]
    const momId = await createUser(request, mom)
    const sueId = await createUser(request, sue)
    const group = await request.post('/api/v2/groups', { data: { name: `Cousins ${suffix}` } })
    expect(group.status()).toBe(201)
    const groupId = (await group.json()).id as string
    for (const id of [momId, sueId]) {
      expect((await request.post(`/api/v2/groups/${groupId}/users`, { data: { user_id: id } })).status()).toBe(201)
    }

    const { device: momDevice, page: moms } = await signInAs(browser, request, momId)
    const { device: sueDevice, page: sues } = await signInAs(browser, request, sueId)
    try {
      await moms.getByRole('textbox', { name: 'Add to my list', exact: true }).fill('A lamp')
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, 'Add an item')).toBeVisible()
      await moms.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(h1(moms, mom)).toBeVisible()
      await sues.getByRole('region', { name: `Cousins ${suffix}`, exact: true }).getByRole('link', { name: mom, exact: true }).click()
      await sues.getByRole('list', { name: 'Items', exact: true }).getByText('A lamp', { exact: true }).click()
      await sues.getByRole('link', { name: "I'll get this", exact: true }).click()
      await sues.getByRole('button', { name: "I'll get this", exact: true }).click()
      await expect(sues.getByText(`You claimed 1 for ${mom}'s birthday`, { exact: true })).toBeVisible()
      const reveal = await request.post('/api/e2e/reveal', { data: { recipient: Number(momId), advance: '10000h' } })
      expect(await reveal.json()).toEqual({ revealed: 1 })

      // Mom adds the lamp to her list again from Received. It left her list when it was given.
      await moms.goto('/received')
      await moms.getByRole('link', { name: 'Add again: A lamp', exact: true }).click()
      await expect(h1(moms, 'Add again')).toBeVisible()
      await moms.getByRole('button', { name: 'Add again', exact: true }).click()
      await expect(h1(moms, mom)).toBeVisible()
      const listUrl = moms.url()
      await expect(moms.getByRole('list', { name: 'Items', exact: true }).getByText('A lamp', { exact: true })).toHaveCount(1)

      // She didn't get it. The screen before says what happens, and Keep it leaves it as it is.
      await moms.goto('/received')
      await moms.getByRole('link', { name: "I didn't get this: A lamp", exact: true }).click()
      await expect(h1(moms, "Didn't get this?")).toBeVisible()
      await moms.getByRole('link', { name: 'Keep it', exact: true }).click()
      await expect(h1(moms, 'Received')).toBeVisible()
      await expect(moms.getByRole('heading', { level: 3, name: 'A lamp', exact: true })).toBeVisible()
      await moms.getByRole('link', { name: "I didn't get this: A lamp", exact: true }).click()
      await moms.getByRole('button', { name: "I didn't get this", exact: true }).click()
      await expect(h1(moms, 'Received')).toBeVisible()
      await expect(moms.getByRole('heading', { level: 2, name: 'Nothing here yet', exact: true })).toBeVisible()
      // The gift is hidden from her again, as one that was never given.
      const gone = await moms.evaluate(async () => (await fetch('/api/v2/gifts?recipient_id=me')).json() as Promise<{ data: unknown[] }>)
      expect(gone.data).toHaveLength(0)
      // Her list has the lamp twice: the one she added again, and the one that came back.
      await moms.goto(listUrl)
      await expect(h1(moms, mom)).toBeVisible()
      await expect(moms.getByRole('list', { name: 'Items', exact: true }).getByText('A lamp', { exact: true })).toHaveCount(2)

      // Sue has the gift open again, and the flag says who didn't get it.
      await sues.goto('/giving')
      await expect(sues.getByText(`${mom} says they didn't get this.`, { exact: true })).toBeVisible()
      await sues.getByRole('button', { name: 'Got it, restored: A lamp', exact: true }).click()
      await expect(sues.getByText(`${mom} says they didn't get this.`, { exact: true })).toHaveCount(0)
      await expect(sues.getByRole('heading', { level: 3, name: 'A lamp', exact: true })).toBeVisible()
    } finally {
      await momDevice.close()
      await sueDevice.close()
    }
  })
})
