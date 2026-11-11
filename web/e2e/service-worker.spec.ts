import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import http from 'node:http'
import os from 'node:os'
import path from 'node:path'
import type { AddressInfo } from 'node:net'
import { expectNoPolicyViolations, watchPolicyViolations } from './support'

// These run in the browser. The e2e project has no DOM types, and adding them
// would change how every other spec checks.
declare const caches: any
declare const document: any
declare const getComputedStyle: any
declare const history: any
declare const navigator: any
declare const requestAnimationFrame: any
declare const Navigator: any

// Most tests run against a copy of web/dist behind the small server below,
// because the Go binary embeds its files and can't "deploy" during a test.
test.use({ serviceWorkers: 'allow' })

const webRoot = path.resolve(import.meta.dirname, '..')
const dist = path.join(webRoot, 'dist')
const types: Record<string, string> = {
  '.css': 'text/css',
  '.html': 'text/html',
  '.js': 'text/javascript',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.webmanifest': 'application/manifest+json',
}

// A copy of contentSecurityPolicy in internal/server/server.go, so the worker
// runs under the policy here too. The Go test pins the real one.
const policy =
  "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: http: https:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

type Health = 'up' | 'unreachable' | 'bad-gateway' | 'hanging'

/** Serves a copy of web/dist with the headers the Go server uses. Nobody is signed in, and setup is done. Other API calls get a 404. */
async function deployment() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'wishbone-sw-'))
  fs.cpSync(dist, root, { recursive: true })
  const state = { health: 'up' as Health, build: 1 }

  const server = http.createServer((req, res) => {
    const { pathname } = new URL(req.url!, 'http://localhost')
    res.setHeader('Content-Security-Policy', policy)
    if (state.health === 'unreachable') return void req.socket.destroy()
    if (state.health === 'hanging') return
    if (state.health === 'bad-gateway') return void res.writeHead(502).end()
    if (pathname === '/api/v2/users/me') {
      return void res.writeHead(401, { 'Content-Type': 'application/problem+json' }).end('{"status":401,"code":"unauthorized"}')
    }
    if (pathname === '/api/v2/setup') {
      return void res.writeHead(200, { 'Content-Type': 'application/json' }).end('{"is_needed":false}')
    }
    if (pathname.startsWith('/api/')) return void res.writeHead(404).end()
    // Go's file server does this too.
    if (pathname === '/index.html') return void res.writeHead(301, { Location: '/' }).end()
    const file = path.join(root, pathname)
    const known = pathname !== '/' && fs.existsSync(file) && fs.statSync(file).isFile()
    const served = known ? file : path.join(root, 'index.html')
    res.writeHead(200, {
      'Content-Type': types[path.extname(served)] ?? 'application/octet-stream',
      'Cache-Control': pathname.startsWith('/assets/') ? 'public, max-age=31536000, immutable' : 'no-cache',
    })
    res.end(fs.readFileSync(served))
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))

  return {
    url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
    state,
    /** A new build: a new script name, a new title, and a new sw.js. */
    deploy() {
      state.build += 1
      const name = `build-${state.build}`
      const indexPath = path.join(root, 'index.html')
      const index = fs.readFileSync(indexPath, 'utf8')
      const script = /src="\/assets\/([^"]+\.js)"/.exec(index)![1]!
      fs.cpSync(path.join(root, 'assets', script), path.join(root, 'assets', `${name}.js`))
      fs.writeFileSync(
        indexPath,
        index.replace(script, `${name}.js`).replace(/<title>.*<\/title>/, `<title>${name}</title>`),
      )
      const workerPath = path.join(root, 'sw.js')
      const worker = fs.readFileSync(workerPath, 'utf8')
      fs.writeFileSync(
        workerPath,
        worker.replace(script, `${name}.js`).replace(/const VERSION = '[^']+'/, `const VERSION = '${name}'`),
      )
      return name
    },
    async close() {
      server.closeAllConnections()
      await new Promise((resolve) => server.close(resolve))
      fs.rmSync(root, { recursive: true, force: true })
    },
  }
}

type Deployment = Awaited<ReturnType<typeof deployment>>

let site: Deployment

// This spec keeps the plain `test` (see playwright.config.ts), so the check that
// support.ts's `test` runs by itself is set up here.
test.beforeEach(async ({ context }) => {
  watchPolicyViolations(context)
  site = await deployment()
})

test.afterEach(async () => {
  await site.close()
  expectNoPolicyViolations()
})

/** The first load installs the worker. The reload puts the page under its control. */
async function loadControlled(page: Page, url: string) {
  await page.goto(url)
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
  })
  await page.reload()
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null)).toBe(true)
}

const cacheNames = (page: Page): Promise<string[]> => page.evaluate(() => caches.keys())

/** Waits until the worker for this build has taken over and removed the old cache. */
async function expectOnlyCache(page: Page, build: string) {
  await expect.poll(() => cacheNames(page)).toEqual([`wishbone-shell-${build}`])
}

function countDocuments(page: Page) {
  const urls: string[] = []
  page.on('request', (r) => {
    if (r.isNavigationRequest()) urls.push(r.url())
  })
  return urls
}

/** The placeholder has no links but Wishbone, so a test adds one. App handles every anchor. */
async function addLink(page: Page) {
  await page.evaluate(() => {
    const a = document.createElement('a')
    a.href = '/list'
    a.textContent = 'Test link'
    document.body.prepend(a)
  })
  return page.getByRole('link', { name: 'Test link' })
}

function installedApp(page: Page) {
  return page.addInitScript(() => {
    Object.defineProperty(navigator, 'standalone', { value: true })
  })
}

async function hide(page: Page) {
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    document.dispatchEvent(new Event('visibilitychange'))
  })
}

const banner = (page: Page) => page.getByText('A new version is available')
const placeholder = (page: Page) => page.getByRole('heading', { level: 1, name: 'Welcome to Wishbone' })

test('the server sends /sw.js as JavaScript that is checked on every load', async ({ request }) => {
  const res = await request.get('/sw.js')
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toMatch(/javascript/)
  expect(res.headers()['cache-control']).toBe('no-cache')
})

test('building again with no changes gives a byte-identical sw.js', async ({ browserName }) => {
  test.skip(browserName !== 'chromium', 'A build, not a browser, is under test')
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), 'wishbone-build-'))
  try {
    execFileSync('pnpm', ['exec', 'vite', 'build', '--outDir', outDir, '--emptyOutDir'], { cwd: webRoot, stdio: 'ignore' })
    expect(fs.readFileSync(path.join(outDir, 'sw.js'))).toEqual(fs.readFileSync(path.join(dist, 'sw.js')))
  } finally {
    fs.rmSync(outDir, { recursive: true, force: true })
  }
})

test('the cache holds exactly the generated list, and a first visit sees no update', async ({ page }) => {
  const documents = countDocuments(page)
  await page.goto(site.url)
  await expect(placeholder(page)).toBeVisible()
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
  })
  await expect(banner(page)).toHaveCount(0)
  expect(documents).toHaveLength(1)

  await page.reload()
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller !== null)).toBe(true)
  await expect(banner(page)).toHaveCount(0)

  const worker = fs.readFileSync(path.join(dist, 'sw.js'), 'utf8')
  const listed = /const FILES = \[(.*)\]/.exec(worker)![1]!.split(', ').map((file) => file.replaceAll("'", ''))
  const cached: { names: string[]; paths: string[] } = await page.evaluate(async () => {
    const names = await caches.keys()
    const keys = await (await caches.open(names[0]!)).keys()
    return { names, paths: keys.map((k: { url: string }) => new URL(k.url).pathname) }
  })
  expect(cached.names).toHaveLength(1)
  expect(cached.paths.toSorted()).toEqual(listed.toSorted())
  expect(cached.paths).toContain('/')
  expect(cached.paths.filter((p) => p.startsWith('/api/') || p === '/metrics')).toEqual([])
})

for (const health of ['unreachable', 'bad-gateway', 'hanging'] as const) {
  test(`a reload still shows the shell when the server is ${health}`, async ({ page }) => {
    await loadControlled(page, site.url)
    site.state.health = health
    for (const url of ['/', '/list']) {
      await page.goto(site.url + url)
      await expect(page).toHaveTitle('wishbone')
      // The app starts from the cached shell. With the server down it cannot
      // say who is signed in, so it shows the frame and no screen yet.
      await expect(page.locator('main')).toBeAttached()
    }
  })
}

test('after a deploy the first reload shows the new shell, with no banner', async ({ page }) => {
  await loadControlled(page, site.url)
  const build = site.deploy()
  await page.reload()
  await expect(page).toHaveTitle(build)
  await expect(placeholder(page)).toBeVisible()

  await expectOnlyCache(page, build)
  // The page's own check is a fetch of `/` that waited on the takeover. This one
  // starts after it, and the frame lets a banner render, so a false banner shows.
  await page.evaluate(async () => {
    await (await fetch('/')).text()
    await new Promise(requestAnimationFrame)
  })
  await expect(banner(page)).toHaveCount(0)

  site.state.health = 'unreachable'
  await page.reload()
  await expect(page).toHaveTitle(build)
})

test('a redirected document ends at the server, not in the cached shell', async ({ page }) => {
  await loadControlled(page, site.url)
  const build = site.deploy()
  await page.goto(`${site.url}/index.html`)
  await expect(page).toHaveURL(`${site.url}/`)
  await expect(page).toHaveTitle(build)
})

test.describe('a page open during a deploy', () => {
  test('finds the update within 60 seconds, and Refresh shows the new shell', async ({ page }) => {
    await page.clock.install()
    await loadControlled(page, site.url)
    const build = site.deploy()
    await page.clock.fastForward(60_000)
    await expect(banner(page)).toBeVisible()
    await page.getByRole('button', { name: 'Refresh' }).click()
    await expect(page).toHaveTitle(build)
    await expect(banner(page)).toHaveCount(0)
  })

  test('a page with no controller finds the update too', async ({ page }) => {
    await page.clock.install()
    await page.goto(site.url)
    await page.evaluate(async () => {
      await navigator.serviceWorker.ready
    })
    expect(await page.evaluate(() => navigator.serviceWorker.controller)).toBeNull()
    await expect(banner(page)).toHaveCount(0)

    const build = site.deploy()
    await page.clock.fastForward(60_000)
    await expect(banner(page)).toBeVisible()
    await page.getByRole('button', { name: 'Refresh' }).click()
    await expect(page).toHaveTitle(build)
  })

  test('Dismiss hides the banner, and the next link is a full load', async ({ page }) => {
    await page.clock.install()
    await loadControlled(page, site.url)
    const build = site.deploy()
    await page.clock.fastForward(60_000)
    await page.getByRole('button', { name: 'Dismiss' }).click()
    await expect(banner(page)).toHaveCount(0)
    await expect(page).toHaveTitle('wishbone')

    const documents = countDocuments(page)
    await (await addLink(page)).click()
    await expect(page).toHaveTitle(build)
    expect(documents).toHaveLength(1)
  })

  test('Back is a full load too while an update waits', async ({ page }) => {
    await page.clock.install()
    await loadControlled(page, site.url)
    await (await addLink(page)).click()
    await expect(placeholder(page)).toBeVisible()
    await page.evaluate(() => history.pushState(null, '', '/?second'))
    const build = site.deploy()
    await page.clock.fastForward(60_000)
    await expect(banner(page)).toBeVisible()
    await page.goBack()
    await expect(page).toHaveTitle(build)
  })

  test('the banner buttons are large enough in light and dark mode at phone width', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 700 })
    await page.clock.install()
    await loadControlled(page, site.url)
    site.deploy()
    await page.clock.fastForward(60_000)
    await expect(banner(page)).toBeVisible()

    const backgrounds = []
    for (const colorScheme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme })
      for (const name of ['Refresh', 'Dismiss']) {
        const box = (await page.getByRole('button', { name }).boundingBox())!
        expect(box.height).toBeGreaterThanOrEqual(48)
        expect(box.x + box.width).toBeLessThanOrEqual(390)
      }
      backgrounds.push(await page.locator('output').evaluate((el) => getComputedStyle(el).backgroundColor))
    }
    expect(backgrounds[0]).not.toBe(backgrounds[1])
  })

  test('an installed app reloads once when hidden, and keeps typed text until then', async ({ page }) => {
    await installedApp(page)
    await page.clock.install()
    await loadControlled(page, site.url)
    await page.evaluate(() => document.body.prepend(document.createElement('textarea')))
    await page.locator('textarea').fill('A note')

    const documents = countDocuments(page)
    const build = site.deploy()
    await page.clock.fastForward(60_000)
    await expectOnlyCache(page, build)

    await expect(banner(page)).toHaveCount(0)
    await expect(page.locator('textarea')).toHaveValue('A note')
    expect(documents).toEqual([])

    await hide(page)
    await expect(page).toHaveTitle(build)
    await expect(placeholder(page)).toBeVisible()
    expect(documents).toHaveLength(1)
  })

  test('an installed app reloads on the next link tap', async ({ page }) => {
    await installedApp(page)
    await page.clock.install()
    await loadControlled(page, site.url)
    const build = site.deploy()
    await page.clock.fastForward(60_000)
    await expectOnlyCache(page, build)

    await (await addLink(page)).click()
    await expect(page).toHaveTitle(build)
  })
})

test('a link makes no document request while no update waits', async ({ page }) => {
  await loadControlled(page, site.url)
  const documents = countDocuments(page)
  const link = await addLink(page)
  await link.click()
  await expect(placeholder(page)).toBeVisible()
  expect(documents).toEqual([])
})

test('/metrics and the API answer from the server with the worker active', async ({ page }) => {
  await loadControlled(page, '/')
  const health = await page.evaluate(async () => (await fetch('/api/v2/health')).status)
  expect(health).toBe(200)
  const fromWorker: string[] = []
  page.on('response', (r) => {
    // A font of the page before can still land while /metrics loads, and the worker serves that.
    if (r.fromServiceWorker() && new URL(r.url()).pathname === '/metrics') fromWorker.push(r.url())
  })
  const metrics = await page.goto('/metrics')
  expect(await metrics!.text()).toContain('# HELP')
  expect(fromWorker).toEqual([])
})

test('the app works with no service workers at all', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => {
    // Asking who is signed in gets a 401 from a browser with no session, and
    // the browser logs every failed load. That is the answer, not an error.
    if (m.type() === 'error' && !m.text().includes('401 (Unauthorized)')) errors.push(m.text())
  })
  await page.addInitScript(() => {
    delete Navigator.prototype.serviceWorker
  })
  await page.goto(site.url)
  await expect(placeholder(page)).toBeVisible()
  expect(await page.evaluate(() => 'serviceWorker' in navigator)).toBe(false)
  expect(errors).toEqual([])
})
