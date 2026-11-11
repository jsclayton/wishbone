import { expect, test } from './support'

test('serves the app shell with the signed-out page', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: 'Welcome to Wishbone' })).toBeVisible()
  await expect(page.getByText('Open your login link to sign in')).toBeVisible()
})

test('a path that no screen matches opens Home', async ({ page }) => {
  for (const path of ['/nothing/here', '/list', '/people/3']) {
    await page.goto(path)
    await expect(page.getByRole('heading', { level: 1, name: 'Welcome to Wishbone' })).toBeVisible()
  }
})

test('health endpoint reports ok', async ({ request }) => {
  const res = await request.get('/api/v2/health')
  expect(res.ok()).toBe(true)
  expect(await res.json()).toMatchObject({ status: 'ok' })
})

test('the OpenAPI document and the docs page are served', async ({ request }) => {
  const doc = await request.get('/api/v2/openapi')
  expect(doc.ok()).toBe(true)
  expect(await doc.json()).toMatchObject({ openapi: expect.any(String), paths: { '/api/v2/health': expect.anything() } })
  expect((await request.get('/api/v2/docs')).ok()).toBe(true)
})

test('a retired API version answers 410 with api_version_removed', async ({ request }) => {
  for (const path of ['/api/v1/health', '/api/v1/me', '/api/v1/openapi', '/api/v0/x']) {
    const res = await request.get(path)
    expect(res.status(), path).toBe(410)
    expect(res.headers()['content-type'], path).toContain('application/problem+json')
    expect(await res.json(), path).toMatchObject({ status: 410, code: 'api_version_removed' })
  }
})

test('an unknown /api/v2 path is a 404 with code not_found', async ({ request }) => {
  const res = await request.get('/api/v2/nope')
  expect(res.status()).toBe(404)
  expect(res.headers()['content-type']).toContain('application/problem+json')
  expect(await res.json()).toMatchObject({ status: 404, code: 'not_found' })
})

test('a page on another origin loses its handle on a Wishbone window', async ({ page, baseURL }) => {
  // 127.0.0.1 and localhost are different origins. Cross-Origin-Opener-Policy
  // puts the opened window in its own browsing context group, so the opener's
  // handle reads as closed and its location can't be set.
  const other = baseURL!.replace('//localhost:', '//127.0.0.1:')
  await page.goto(`${other}/api/v2/health`)
  const popup = page.waitForEvent('popup')
  await page.evaluate((url) => {
    const w = globalThis as unknown as { opened: unknown; open(url: string): unknown }
    w.opened = w.open(url)
  }, `${baseURL}/`)
  await popup
  await expect
    .poll(() => page.evaluate(() => (globalThis as unknown as { opened: { closed: boolean } }).opened.closed))
    .toBe(true)
})
