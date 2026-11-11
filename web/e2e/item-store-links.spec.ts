import http from 'node:http'
import type { AddressInfo } from 'node:net'
import type { Page } from '@playwright/test'
import { expect, finishProfile, test } from './support'
import { startServer, type TestServer } from './server'

// These specs manage the store links and the images of an item, refresh the
// prices of its store links, and check the duplicate hint of the add screen. No
// call leaves this machine: the "shop" is a fixture page served from here, and
// the server runs with the e2e build's switch for private addresses
// (WISHBONE_EXTRACT_ALLOW_PRIVATE). Each spec runs a server of its own.

// A 1x1 PNG.
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')

let fixture: http.Server
let origin: string
let server: TestServer
/** The price the fixture shop shows for the kettle. A test changes it. */
let kettlePrice = '49.99'

test.beforeEach(async () => {
  kettlePrice = '49.99'
  fixture = http.createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://fixture')
    if (url.pathname === '/kettle') {
      res.setHeader('Content-Type', 'text/html')
      res.end(`<!doctype html><html><head><title>Copper kettle</title>
        <meta property="og:title" content="Copper kettle">
        <meta property="product:price:amount" content="${kettlePrice}">
        <meta property="product:price:currency" content="USD">
      </head><body>A copper kettle</body></html>`)
    } else {
      res.statusCode = 404
      res.end()
    }
  })
  await new Promise<void>((resolve) => fixture.listen(0, '127.0.0.1', resolve))
  origin = `http://127.0.0.1:${(fixture.address() as AddressInfo).port}`
  server = await startServer(undefined, { WISHBONE_EXTRACT_ALLOW_PRIVATE: 'true' })
})

test.afterEach(async () => {
  await server.stop()
  await new Promise((resolve) => fixture.close(resolve))
})

async function setUp(page: Page) {
  await page.goto(server.url)
  await page.getByLabel('Setup code').fill(server.setupCode)
  await page.getByLabel('First name', { exact: true }).fill('Grace')
  await page.getByLabel('Month').selectOption('December')
  await page.getByLabel('Day').fill('9')
  await page.getByRole('button', { name: 'Set up' }).click()
  await finishProfile(page)
}

/** The API of this spec's own server, as the signed-in page. The relative addresses of `page.request` would reach the shared server. */
const api = (page: Page) => ({
  get: (path: string) => page.request.get(`${server.url}/api/v2${path}`),
  post: (path: string, options: Parameters<Page['request']['post']>[1]) => page.request.post(`${server.url}/api/v2${path}`, options),
})

const h1 = (page: Page, name: string) => page.getByRole('heading', { level: 1, name })

/** Adds an item to the user's primary list through the API, and returns it. */
async function addItem(page: Page, title: string, more: object = {}) {
  const lists = await (await api(page).get('/lists?is_primary=true')).json()
  const res = await api(page).post(`/lists/${lists.data[0].id}/items`, { data: { title, ...more } })
  expect(res.status()).toBe(201)
  return (await res.json()) as { id: string; list_id: string; price_minor: number | null; store_links: { id: string; price_minor: number | null }[]; images: { id: string }[]; primary_image: { id: string } | null }
}

const itemNow = async (page: Page, id: string) => (await (await api(page).get(`/items/${id}`)).json()) as Awaited<ReturnType<typeof addItem>>

test('a store link is added with the price read from its page, the prices refresh, and a link is removed', async ({ page }) => {
  await setUp(page)
  const item = await addItem(page, 'Kettle', { price_minor: 3000 })
  await page.goto(`${server.url}/items/${item.id}`)
  await expect(h1(page, 'Kettle')).toBeVisible()

  await page.getByRole('button', { name: 'Add link' }).click()
  await page.getByLabel('Link', { exact: true }).fill(`${origin}/kettle`)
  await page.getByRole('button', { name: 'Save link' }).click()

  const where = page.getByRole('region', { name: 'Where to buy' })
  await expect(where.getByRole('link', { name: '127.0.0.1' })).toHaveAttribute('href', `${origin}/kettle`)
  await expect(where.getByText('$49.99')).toBeVisible()

  // The shop changes its price. A refresh reads the page again, and the item price stays.
  kettlePrice = '39.00'
  await page.getByRole('button', { name: 'Refresh prices' }).click()
  await expect(where.getByText('$39.00')).toBeVisible()
  await expect(page.getByRole('status')).toContainText('1 of 1 price changed.')
  const refreshed = await itemNow(page, item.id)
  expect(refreshed.store_links[0]!.price_minor).toBe(3900)
  expect(refreshed.price_minor).toBe(3000)

  // A price typed by hand, then cleared.
  await page.getByRole('button', { name: 'Edit price of 127.0.0.1' }).click()
  await page.getByLabel('Price', { exact: true }).fill('35.50')
  await page.getByRole('button', { name: 'Save price' }).click()
  await expect(where.getByText('$35.50')).toBeVisible()
  await page.getByRole('button', { name: 'Edit price of 127.0.0.1' }).click()
  await page.getByLabel('Price', { exact: true }).fill('')
  await page.getByRole('button', { name: 'Save price' }).click()
  await expect(where.getByText('$35.50')).toHaveCount(0)
  expect((await itemNow(page, item.id)).store_links[0]!.price_minor).toBeNull()

  await page.getByRole('button', { name: 'Remove link 127.0.0.1' }).click()
  await expect(where.getByRole('link', { name: '127.0.0.1' })).toHaveCount(0)
  expect((await itemNow(page, item.id)).store_links).toHaveLength(0)
})

test('a link from a blocked store is refused with the reason', async ({ page }) => {
  await setUp(page)
  expect((await api(page).post('/blocked-stores', { data: { store: '127.0.0.1' } })).status()).toBe(201)
  const item = await addItem(page, 'Kettle')
  await page.goto(`${server.url}/items/${item.id}`)

  await page.getByRole('button', { name: 'Add link' }).click()
  await page.getByLabel('Link', { exact: true }).fill(`${origin}/kettle`)
  await page.getByRole('button', { name: 'Save link' }).click()

  await expect(page.getByRole('alert')).toContainText('127.0.0.1')
  await expect(page.getByLabel('Link', { exact: true })).toHaveValue(`${origin}/kettle`)
})

test('images are added, the primary image is picked, and one is removed, with no way to reorder', async ({ page }) => {
  await setUp(page)
  const item = await addItem(page, 'Kettle')
  await page.goto(`${server.url}/items/${item.id}`)
  await page.getByRole('link', { name: 'Add an image' }).click()
  await expect(h1(page, 'Images')).toBeVisible()

  for (const name of ['a.png', 'b.png', 'c.png']) {
    await page.getByLabel('Choose an image to add').setInputFiles({ name, mimeType: 'image/png', buffer: png })
    await expect(page.getByRole('list', { name: 'Images' }).getByRole('img')).toHaveCount(['a.png', 'b.png', 'c.png'].indexOf(name) + 1)
  }
  const added = await itemNow(page, item.id)
  expect(added.images).toHaveLength(3)
  expect(added.primary_image!.id).toBe(added.images[0]!.id)

  await page.getByRole('button', { name: 'Make image 3 the primary image' }).click()
  await expect(page.getByText('Primary image', { exact: true })).toBeVisible()
  const picked = await itemNow(page, item.id)
  expect(picked.primary_image!.id).toBe(added.images[2]!.id)
  expect(picked.images.map((i) => i.id)).toEqual(added.images.map((i) => i.id))

  await page.getByRole('button', { name: 'Remove image 1' }).click()
  await expect(page.getByRole('list', { name: 'Images' }).getByRole('img')).toHaveCount(2)
  const removed = await itemNow(page, item.id)
  expect(removed.images.map((i) => i.id)).toEqual([added.images[1]!.id, added.images[2]!.id])
  expect(removed.primary_image!.id).toBe(added.images[2]!.id)
  await expect(page.getByRole('button', { name: /reorder|move up|move down/i })).toHaveCount(0)

  // The item page shows the images, the primary image first.
  await page.getByRole('button', { name: 'Done' }).click()
  await expect(h1(page, 'Kettle')).toBeVisible()
  const shown = page.getByRole('region', { name: 'Images' }).getByRole('img')
  await expect(shown).toHaveCount(2)
  await expect(shown.first()).toHaveAttribute('src', `/api/v2/images/${added.images[2]!.id}`)
})

test('the add screen hints, in one line, at an item already on the list, and still adds', async ({ page }) => {
  await setUp(page)
  const kettle = await addItem(page, 'Copper kettle')
  const lists = await (await api(page).get('/lists?is_primary=true')).json()

  await page.goto(`${server.url}/lists/${lists.data[0].id}/add`)
  await expect(h1(page, 'Add an item')).toBeVisible()
  await page.getByLabel('Title').fill('  copper   KETTLE ')

  const hint = page.getByRole('link', { name: 'Already on this list: Copper kettle' })
  await expect(hint).toHaveAttribute('href', `/items/${kettle.id}`)

  // It never blocks the add: one click adds a second item.
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Grace')).toBeVisible()
  const items = await (await api(page).get(`/lists/${lists.data[0].id}/items`)).json()
  expect(items.data).toHaveLength(2)

  // A pasted link that is already a store link of an item gives the same hint.
  const second = await addItem(page, 'Another')
  expect((await api(page).post(`/items/${second.id}/store-links`, { data: { url: `${origin}/kettle` } })).status()).toBe(201)
  await page.goto(server.url)
  await page.getByRole('textbox', { name: 'Add to my list' }).fill(`${origin}/kettle`)
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Add an item')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Already on this list: Another' })).toHaveAttribute('href', `/items/${second.id}`)
})
