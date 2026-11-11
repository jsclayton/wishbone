import { expect, prepareContext, test } from './support'

// A deactivated child stays a recipient of her lists. Her parent still sees the
// lists, with "Deactivated" beside her name, and can't change anything through
// her (docs/spec.md, Deactivation and deletion). Names carry a random suffix,
// so every name is matched exactly.
test.describe('a deactivated recipient', () => {
  test.use({ signedIn: true })

  test("her parent sees her lists, labelled, with no control, and the server refuses the parent's edits", async ({ browser, request }) => {
    const suffix = Math.random().toString(36).slice(2, 8)
    const mom = `Mom${suffix}`
    const emma = `Emma${suffix}`
    const create = async (name: string, extra: object = {}) => {
      const res = await request.post('/api/v2/users', { data: { name, birthday: { month: 3, day: 4, year: null }, ...extra } })
      expect(res.status()).toBe(201)
      return (await res.json()).id as string
    }
    const momId = await create(mom)
    const emmaId = await create(emma, { parent_ids: [momId] })

    // Mom signs in on a device of her own.
    const momLink = await request.post(`/api/v2/users/${momId}/login-links`)
    expect(momLink.status()).toBe(201)
    const momDevice = await browser.newContext({ storageState: { cookies: [], origins: [] } })
    await prepareContext(momDevice)
    const mine = await momDevice.newPage()
    await mine.goto((await momLink.json()).url)
    await mine.getByRole('button', { name: 'Sign in' }).click()
    await mine.getByRole('button', { name: 'Continue' }).click()
    await mine.getByRole('button', { name: 'Not now' }).click()
    await expect(mine.getByRole('heading', { level: 1, name: 'Home', exact: true })).toBeVisible()

    // Mom makes a list for Emma, and puts one item on it.
    const listRes = await mine.request.post('/api/v2/lists', { data: { name: `Books${suffix}`, recipient_id: emmaId } })
    expect(listRes.status()).toBe(201)
    const listId = (await listRes.json()).id as string
    const itemRes = await mine.request.post(`/api/v2/lists/${listId}/items`, { data: { title: `Atlas${suffix}` } })
    expect(itemRes.status()).toBe(201)

    await mine.goto(`/lists/${listId}`)
    await expect(mine.getByRole('link', { name: 'Add', exact: true })).toBeVisible()
    await expect(mine.getByRole('link', { name: 'Edit list', exact: true })).toBeVisible()
    await expect(mine.getByRole('link', { name: 'Delete list', exact: true })).toBeVisible()

    // The admin deactivates Emma.
    const paused = await request.post(`/api/v2/users/${emmaId}/deactivate`)
    expect(paused.status()).toBe(200)

    // My lists still has Emma's part, with her list and the label.
    await mine.goto('/lists')
    const part = mine.getByRole('region', { name: `${emma} Deactivated`, exact: true })
    const books = part.getByRole('link', { name: `Books${suffix} 1 item`, exact: true })
    await expect(books).toBeVisible()

    // Her list's page shows the label beside her name and no control.
    await books.click()
    await expect(mine.getByRole('heading', { level: 1, name: `Books${suffix}`, exact: true })).toBeVisible()
    await expect(mine.getByRole('link', { name: emma, exact: true })).toBeVisible()
    await expect(mine.getByText('Deactivated', { exact: true })).toBeVisible()
    await expect(mine.getByRole('link', { name: `Atlas${suffix} 1 of 1 still wanted`, exact: true })).toBeVisible()
    // The parent's powers stop, and the claims still show, so the page says so.
    await expect(mine.getByText("You see what's been claimed here. Fully claimed items don't show.", { exact: true })).toBeVisible()
    for (const name of ['Add', 'Edit list', 'Delete list', 'Invite', 'Leave', `Make ${emma} leave`]) {
      await expect(mine.getByRole('link', { name, exact: true })).toHaveCount(0)
    }

    // The server agrees with the page.
    const edit = await mine.request.patch(`/api/v2/lists/${listId}`, { data: { name: 'Nope' } })
    expect(edit.status()).toBe(403)
    expect((await edit.json()).code).toBe('user_deactivated')
    const remove = await mine.request.delete(`/api/v2/lists/${listId}`)
    expect(remove.status()).toBe(403)
    expect((await remove.json()).code).toBe('user_deactivated')

    // Her user page shows the label inline after her name.
    await mine.goto(`/users/${emmaId}`)
    await expect(mine.getByRole('heading', { level: 1, name: `${emma} Deactivated`, exact: true })).toBeVisible()

    // Reactivation gives the parent her controls back.
    expect((await request.post(`/api/v2/users/${emmaId}/reactivate`)).status()).toBe(200)
    await mine.goto(`/lists/${listId}`)
    await expect(mine.getByRole('link', { name: 'Edit list', exact: true })).toBeVisible()
    await expect(mine.getByText('Deactivated', { exact: true })).toHaveCount(0)

    await momDevice.close()
  })
})
