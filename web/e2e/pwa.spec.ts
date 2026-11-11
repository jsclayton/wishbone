import { expect, test } from './support'

type Manifest = {
  display: string
  theme_color: string
  background_color: string
  icons: { src: string; sizes: string; type: string; purpose?: string }[]
}

test('serves the manifest and every icon it lists with the right content type', async ({ request }) => {
  const res = await request.get('/manifest.webmanifest')
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toMatch(/^application\/manifest\+json/)
  const manifest = (await res.json()) as Manifest

  expect(manifest.display).toBe('standalone')
  expect(manifest.theme_color).toMatch(/^#[0-9a-f]{6}$/i)
  expect(manifest.background_color).toMatch(/^#[0-9a-f]{6}$/i)
  expect(manifest.icons).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ sizes: '192x192', type: 'image/png' }),
      expect.objectContaining({ sizes: '512x512', type: 'image/png', purpose: 'any' }),
      expect.objectContaining({ sizes: '512x512', type: 'image/png', purpose: 'maskable' }),
    ]),
  )

  for (const icon of manifest.icons) {
    const iconRes = await request.get(icon.src)
    expect(iconRes.status(), icon.src).toBe(200)
    expect(iconRes.headers()['content-type'], icon.src).toBe(icon.type)
  }
})

test('the page links the apple touch icon and a theme color for each scheme', async ({ page, request }) => {
  await page.goto('/')
  const touch = await page.locator('link[rel="apple-touch-icon"]').getAttribute('href')
  expect(touch).toBeTruthy()
  const res = await request.get(touch!)
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toBe('image/png')

  await expect(page.locator('meta[name="theme-color"][media="(prefers-color-scheme: light)"]')).toHaveCount(1)
  await expect(page.locator('meta[name="theme-color"][media="(prefers-color-scheme: dark)"]')).toHaveCount(1)
  await expect(page.locator('meta[name="apple-mobile-web-app-capable"]')).toHaveAttribute('content', 'yes')
})
