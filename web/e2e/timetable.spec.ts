import { expect, test } from './support'

// What the styles (the Timetable design) do on a real page: a test of the CSS source can't
// see a size that a class overrides, or a font that failed to load.

test('text is 18px, the fonts load from Wishbone, and the heading is Atkinson', async ({ page }) => {
  const fontHosts = new Set<string>()
  page.on('request', (r) => {
    if (r.resourceType() === 'font') fontHosts.add(new URL(r.url()).hostname)
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: 'Welcome to Wishbone' })).toBeVisible()

  await expect(page.locator('main')).toHaveCSS('font-size', '18px')
  await expect(page.getByText('Open your login link to sign in')).toHaveCSS('font-family', /Atkinson Hyperlegible Next/)

  await expect
    .poll(() =>
      // A string, because the e2e TypeScript config has no DOM lib.
      page.evaluate<string[]>(`[...document.fonts].filter((f) => f.status === 'loaded').map((f) => f.family.replaceAll('"', ''))`),
    )
    .toEqual(expect.arrayContaining(['Atkinson Hyperlegible Next']))
  expect([...fontHosts]).toEqual([new URL(page.url()).hostname])
})

test('the font licenses are served', async ({ page }) => {
  for (const name of ['next', 'mono']) {
    const res = await page.request.get(`/licenses/atkinson-hyperlegible-${name}.txt`)
    expect(res.status(), name).toBe(200)
    expect(await res.text(), name).toContain('SIL Open Font License')
  }
})
