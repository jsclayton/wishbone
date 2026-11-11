import http from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, finishProfile, test } from './support'
import { startServer, type TestServer } from './server'

// These specs search a link, typed words, and a photo, and add the result. No
// call leaves this machine: the "shop" is a fixture page served from here, the
// "LLM" is a fixture endpoint served from here, and the server runs with the e2e
// build's switch for private addresses (WISHBONE_EXTRACT_ALLOW_PRIVATE), which
// the release build does not have. Each spec runs a server of its own: it sets
// the instance up, and it counts toward the hourly limit of its own admin.

// A 1x1 PNG.
const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')

// A hoodie that comes in sizes and colors: one variant for each pair.
const variants = [
  ['101', 'S', 'Black'],
  ['102', 'M', 'Black'],
  ['103', 'S', 'Navy'],
  ['104', 'M', 'Navy'],
].map(([id, size, color]) => ({ '@type': 'Product', sku: `H-${id}`, size, color, url: `/hoodie?variant=${id}` }))

const hoodie = (data: object) =>
  `<!doctype html><html><head><title>Club fleece hoodie</title><script type="application/ld+json">${JSON.stringify({
    '@context': 'https://schema.org',
    name: 'Club fleece hoodie',
    offers: { '@type': 'Offer', price: '65.00', priceCurrency: 'USD' },
    ...data,
  })}</script></head><body></body></html>`

let fixture: http.Server
let origin: string
let server: TestServer
const hits: string[] = []

test.beforeEach(async () => {
  hits.length = 0
  fixture = http.createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://fixture')
    hits.push(`${req.method} ${url.pathname}`)
    if (url.pathname === '/kettle') {
      res.setHeader('Content-Type', 'text/html')
      res.end(`<!doctype html><html><head><title>Copper kettle</title>
        <meta property="og:title" content="Copper kettle">
        <meta property="og:image" content="${origin}/img/a.png">
        <meta property="og:image" content="${origin}/img/b.png">
        <meta property="product:price:amount" content="49.99">
        <meta property="product:price:currency" content="USD">
      </head><body>A copper kettle</body></html>`)
    } else if (url.pathname === '/hoodie') {
      res.setHeader('Content-Type', 'text/html')
      res.end(hoodie({ '@type': 'ProductGroup', variesBy: ['https://schema.org/size', 'https://schema.org/color'], hasVariant: variants }))
    } else if (url.pathname === '/bare-hoodie') {
      res.setHeader('Content-Type', 'text/html')
      res.end(hoodie({ '@type': 'ProductGroup', variesBy: ['https://schema.org/size', 'https://schema.org/color'] }))
    } else if (url.pathname.startsWith('/img/')) {
      res.setHeader('Content-Type', 'image/png')
      res.end(png)
    } else if (url.pathname === '/chat/completions') {
      // The LLM, for a photo: it reads the photo and answers with a title and a price.
      req.resume()
      res.setHeader('Content-Type', 'application/json')
      res.end(JSON.stringify({ choices: [{ message: { content: '{"title": "Blue enamel mug", "price": "12.50", "currency": "USD"}' } }] }))
    } else {
      res.statusCode = 404
      res.end()
    }
  })
  await new Promise<void>((resolve) => fixture.listen(0, '127.0.0.1', resolve))
  origin = `http://127.0.0.1:${(fixture.address() as AddressInfo).port}`
  server = await startServer(undefined, {
    WISHBONE_EXTRACT_ALLOW_PRIVATE: 'true',
    WISHBONE_LLM_PROVIDER: 'openai',
    WISHBONE_LLM_BASE_URL: origin,
    WISHBONE_LLM_MODEL: 'fixture',
  })
})

test.afterEach(async () => {
  await server.stop()
  await new Promise((resolve) => fixture.close(resolve))
})

async function setUp(page: import('@playwright/test').Page) {
  await page.goto(server.url)
  await page.getByLabel('Setup code').fill(server.setupCode)
  await page.getByLabel('First name', { exact: true }).fill('Grace')
  await page.getByLabel('Month').selectOption('December')
  await page.getByLabel('Day').fill('9')
  await page.getByRole('button', { name: 'Set up' }).click()
  await finishProfile(page)
}

/** The API of this spec's own server, as the signed-in page. The relative addresses of `page.request` would reach the shared server. */
const api = (page: import('@playwright/test').Page) => ({
  get: (path: string) => page.request.get(`${server.url}/api/v2${path}`),
  post: (path: string, options: Parameters<typeof page.request.post>[1]) => page.request.post(`${server.url}/api/v2${path}`, options),
})

const h1 = (page: import('@playwright/test').Page, name: string) => page.getByRole('heading', { level: 1, name })

test('a pasted link is read, the form opens with the page, and adding saves the store link and the images', async ({ page }) => {
  await setUp(page)

  await page.getByRole('textbox', { name: 'Add to my list' }).fill(`${origin}/kettle?utm_source=mail`)
  await page.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(h1(page, 'Add an item')).toBeVisible()
  await expect(page.getByLabel('Title')).toHaveValue('Copper kettle')
  await expect(page.getByLabel('Price')).toHaveValue('49.99')
  await expect(page.getByText('127.0.0.1', { exact: true })).toBeVisible()
  await expect(page.getByRole('checkbox', { name: /Use image/ })).toHaveCount(2)
  await page.getByRole('checkbox', { name: 'Use image 2' }).uncheck()
  await page.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(h1(page, 'Grace')).toBeVisible()
  await expect(page.getByRole('list', { name: 'Items' }).getByText('Copper kettle')).toBeVisible()
  // The server saved the store link, and downloaded the one picked image.
  const lists = await (await api(page).get('/lists?is_primary=true')).json()
  const items = await (await api(page).get(`/lists/${lists.data[0].id}/items`)).json()
  expect(items.data).toHaveLength(1)
  const [item] = items.data
  expect(item.price_minor).toBe(4999)
  expect(item.store_links).toHaveLength(1)
  expect(item.store_links[0]).toMatchObject({ url: `${origin}/kettle`, store: '127.0.0.1', price_minor: 4999, currency: 'USD' })
  expect(item.images).toHaveLength(1)
  expect(item.primary_image.id).toBe(item.images[0].id)
  // The image loads for its user.
  const bytes = await page.request.get(`${server.url}${item.primary_image.url}`)
  expect(bytes.status()).toBe(200)
  expect(bytes.headers()['content-type']).toBe('image/jpeg')
})

test('typed words open the form as a free-text item', async ({ page }) => {
  await setUp(page)

  await page.getByRole('textbox', { name: 'Add to my list' }).fill('a book about sharks')
  await page.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(h1(page, 'Add an item')).toBeVisible()
  await expect(page.getByLabel('Title')).toHaveValue('a book about sharks')
  await expect(page.getByLabel('Notes')).toHaveValue('a book about sharks')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Grace')).toBeVisible()
  await expect(page.getByRole('list', { name: 'Items' }).getByText('a book about sharks')).toBeVisible()
  expect(hits).toEqual([])
})

test('a photo is read by the LLM, nothing is saved until Add, and then the photo is the primary image', async ({ page }) => {
  await setUp(page)
  const before = (await (await api(page).get('/lists?is_primary=true')).json()).data[0]

  await page.getByLabel('Photo file').setInputFiles({ name: 'mug.png', mimeType: 'image/png', buffer: png })

  await expect(h1(page, 'Add an item')).toBeVisible()
  await expect(page.getByLabel('Title')).toHaveValue('Blue enamel mug')
  await expect(page.getByLabel('Price')).toHaveValue('12.50')
  await expect(page.getByRole('img', { name: 'What you picked' })).toBeVisible()
  expect(hits.filter((h) => h === 'POST /chat/completions')).toHaveLength(1)

  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Grace')).toBeVisible()
  const items = await (await api(page).get(`/lists/${before.id}/items`)).json()
  expect(items.data).toHaveLength(1)
  expect(items.data[0]).toMatchObject({ title: 'Blue enamel mug', price_minor: 1250 })
  expect(items.data[0].images).toHaveLength(1)
  expect(items.data[0].primary_image.id).toBe(items.data[0].images[0].id)
})

test('a link from a blocked store stays in the box with the reason', async ({ page }) => {
  await setUp(page)
  expect((await api(page).post('/blocked-stores', { data: { store: '127.0.0.1' } })).status()).toBe(201)

  await page.getByRole('textbox', { name: 'Add to my list' }).fill(`${origin}/kettle`)
  await page.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(page.getByRole('alert')).toContainText('127.0.0.1')
  await expect(page.getByRole('textbox', { name: 'Add to my list' })).toHaveValue(`${origin}/kettle`)
  await expect(h1(page, 'Home')).toBeVisible()
  expect(hits).toEqual([])
})

test('past the hourly limit the form keeps the link and says it was not read', async ({ page }) => {
  await setUp(page)
  for (let i = 0; i < 30; i++) {
    const res = await api(page).post('/search', {
      multipart: { text: `${origin}/kettle?n=${i}` },
    })
    expect(res.status()).toBe(200)
  }

  await page.getByRole('textbox', { name: 'Add to my list' }).fill(`${origin}/kettle?n=31`)
  await page.getByRole('button', { name: 'Add', exact: true }).click()

  await expect(h1(page, 'Add an item')).toBeVisible()
  await expect(page.getByRole('status')).toContainText("We didn't read this one")
  await expect(page.getByLabel('Title')).toHaveValue('')
  await expect(page.getByText('127.0.0.1', { exact: true })).toBeVisible()
  expect(hits.filter((h) => h === 'GET /kettle')).toHaveLength(30)
})

test('the options a page offers are dropdowns, and the choices are saved into the notes', async ({ page }) => {
  await setUp(page)
  const notesOf = async () => {
    const list = (await (await api(page).get('/lists?is_primary=true')).json()).data[0]
    return ((await (await api(page).get(`/lists/${list.id}/items`)).json()).data as { notes: string | null }[]).map((i) => i.notes)
  }

  // The link picks a variant: the dropdowns start on it. The user changes one and adds a note.
  await page.getByRole('textbox', { name: 'Add to my list' }).fill(`${origin}/hoodie?variant=104`)
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Add an item')).toBeVisible()
  const choices = page.getByRole('group', { name: 'Which one do you want?' })
  await expect(choices.getByLabel('Size')).toHaveValue('M')
  await expect(choices.getByLabel('Color')).toHaveValue('Navy')
  await choices.getByLabel('Color').selectOption('Black')
  await page.getByLabel('Notes').fill('The grey trim, please')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Grace')).toBeVisible()
  expect(await notesOf()).toEqual(['Size: M\nColor: Black\nThe grey trim, please'])

  // A page that names the options and lists no values starts the notes with a line for each, and an unchanged line is dropped.
  await page.goto(server.url)
  await page.getByRole('textbox', { name: 'Add to my list' }).fill(`${origin}/bare-hoodie`)
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Add an item')).toBeVisible()
  await expect(page.getByRole('combobox')).toHaveCount(0)
  await expect(page.getByLabel('Notes')).toHaveValue('Size:\nColor:')
  await page.getByLabel('Notes').fill('Size: L\nColor:')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(h1(page, 'Grace')).toBeVisible()
  expect(await notesOf()).toEqual(['Size: M\nColor: Black\nThe grey trim, please', 'Size: L'])
})
