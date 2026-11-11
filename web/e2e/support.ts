import { test as base, expect, request, type APIRequestContext, type BrowserContext, type Page, type Route } from '@playwright/test'
import path from 'node:path'

/** Where global-setup.ts leaves the admin's session, as a Playwright storage state. */
export const adminStatePath = () => path.join(process.env.WISHBONE_DATA_DIR!, 'admin-state.json')


let sharedApi: Promise<APIRequestContext> | undefined
function apiContext() {
  // Playwright gives new request contexts the test's storage state. Start empty,
  // or a signedInAs session would ride along on every request.
  sharedApi ??= request.newContext({ storageState: { cookies: [], origins: [] } })
  return sharedApi
}

/**
 * The server marks the session cookie Secure, as the spec requires. WebKit on
 * Linux (CI) does not send a Secure cookie to http://localhost, so a sign-in
 * never sticks there. Drop the Secure attribute from API responses in the test
 * browser only.
 *
 * route.fetch() would store the original cookie, still Secure, in the browser
 * context's own jar. A separate request context keeps that jar out of it. The
 * browser's session cookie is forwarded by hand.
 *
 * Only a page's own calls come through this route. page.request and
 * context.request share the jar and skip the route, so a call that gets a
 * Set-Cookie (a request that renews the cookie can, once a day) marks the session cookie Secure
 * again. An http page can't then replace it, so a later sign-in in that context
 * doesn't stick. Keep those calls away from a context that signs in again.
 *
 * Use ONE request context for the whole worker, and dial 127.0.0.1 instead of
 * "localhost". A request context per call opened a new TCP connection
 * each time. Some of those connects stalled for 5 s, the wait Playwright's Node
 * client allows before it tries the next address for "localhost" (::1, then
 * 127.0.0.1). The server saw the request 5 s late and answered in under 1 ms,
 * so some API calls (and so some pages) took 5 s or 10 s. One context reuses its
 * connection, and a fixed IPv4 address has no second address to wait for.
 *
 * Nothing disposes that context. A context disposed when its browser context
 * closes would break a retry: tracing is on, and the trace recorder stops while
 * that dispose runs. The stop then fails with "Target page, context or browser
 * has been closed", which cuts the trace short.
 * The jar is never sent, because `forward` always sets the Cookie header and
 * follows no redirect (`maxRedirects: 0`; a hop would not carry that header, so
 * the jar's cookies would go with it), so sharing the context between browser
 * contexts is safe. The worker's exit ends it.
 */
async function allowSecureCookieOverHttp(context: BrowserContext) {
  // A page can still have a call in flight when its context closes at the end
  // of a test. Ignore the errors of those late calls.
  let closed = false
  context.on('close', () => {
    closed = true
  })

  await context.route('**/api/v2/**', (route) =>
    forward(route).catch((error) => {
      if (!closed) throw error
    }),
  )

  async function forward(route: Route) {
    const api = await apiContext()
    // Routed requests carry no Cookie header yet, so add the context's cookies.
    // Always set the header, even empty: the request context would otherwise
    // send cookies from its own jar, which stores every Set-Cookie it sees.
    const cookies = await context.cookies(route.request().url())
    const cookie = cookies.map((c) => `${c.name}=${c.value}`).join('; ')
    const req = route.request()
    // Dial 127.0.0.1, not "localhost" (see above). Keep the Host header.
    // Follow no redirect, so a hop can't send the shared jar's cookies. No API route
    // redirects today. If one does, Chromium follows the 3xx itself, outside this
    // route, and in WebKit route.fulfill throws on a 3xx.
    const response = await api.fetch(req.url().replace('//localhost:', '//127.0.0.1:'), {
      method: req.method(),
      maxRedirects: 0,
      data: req.postDataBuffer() ?? undefined,
      headers: { ...req.headers(), host: new URL(req.url()).host, cookie },
    })
    const headers = { ...response.headers() }
    if (headers['set-cookie']) {
      headers['set-cookie'] = headers['set-cookie'].replace(/;\s*Secure/gi, '')
    }
    await route.fulfill({ response, headers })
  }
}

// WebKit logs this for any <select>, even one with no app code, and nothing
// changes on screen. The same text also covers a real `style` attribute and an
// inline <style>, so ignoring it hides those in WebKit too, and only Chromium
// catches them. Ignore exactly this text, in WebKit only.
const webkitSelectViolation =
  "Refused to apply a stylesheet because its hash, its nonce, or 'unsafe-inline' does not appear in the style-src directive of the Content Security Policy."

// A worker runs one test at a time, so one list holds the current test's
// violations, from every context it opens, even one it has closed.
const policyViolations: string[] = []

/**
 * The server sends a Content-Security-Policy. A browser logs every
 * violation to the console and carries on, so a broken feature could pass its
 * test. Chromium and WebKit both put "Content Security Policy" in the message.
 * `prepareContext` calls this. Call it yourself only in a spec that can't use
 * the `test` below, and call `expectNoPolicyViolations` after each test.
 */
export function watchPolicyViolations(context: BrowserContext) {
  const engine = context.browser()?.browserType().name()
  context.on('console', (message) => {
    const text = message.text()
    if (!text.includes('Content Security Policy')) return
    if (engine === 'webkit' && text === webkitSelectViolation) return
    policyViolations.push(text)
  })
}

/** Fails on the violations seen so far and empties the list for the next test. */
export function expectNoPolicyViolations() {
  expect(policyViolations.splice(0), 'the page logged Content-Security-Policy violations').toEqual([])
}

/**
 * Sets up a context for this suite: the Secure cookie fix and the check for
 * policy violations. The `context` fixture below does this. Call it yourself for a context you create
 * with `browser.newContext()`.
 */
export async function prepareContext(context: BrowserContext) {
  watchPolicyViolations(context)
  await allowSecureCookieOverHttp(context)
}

type Options = {
  /**
   * Start signed in as the admin that global-setup.ts made. Set it with
   * `test.use({ signedIn: true })` in a spec that is not about signing in. Unset
   * means signed out. Every test shares the admin's one session, so a test that
   * uses it must not end or replace it: a test that logs out runs its own server
   * (see `startServer`). A context from `browser.newContext()` starts signed in
   * too. Pass an empty `storageState` (`{ cookies: [], origins: [] }`) for a
   * signed-out device.
   */
  signedIn: boolean
}

type TestFixtures = {
  /** Runs for every test, so it also covers a test that never asks for `context`. */
  policyCheck: void
}

// Use this `test` instead of the one from @playwright/test. Its `context`, and
// so its `page`, fail the test on a Content-Security-Policy violation.
export const test = base.extend<Options & TestFixtures>({
  signedIn: [false, { option: true }],

  storageState: async ({ storageState, signedIn }, provide) => {
    await provide(signedIn ? adminStatePath() : storageState)
  },

  context: async ({ context }, provide) => {
    await prepareContext(context)
    await provide(context)
  },

  policyCheck: [
    // Playwright requires a destructuring pattern here, even an empty one.
    // oxlint-disable-next-line no-empty-pattern
    async ({}, provide) => {
      await provide()
      expectNoPolicyViolations()
    },
    { auto: true },
  ],
})

/**
 * Gets through the finish-profile steps that follow setup, with the name and
 * birthday that setup took and no photo, and waits for Home. The admin that
 * setup makes has not confirmed their profile, so the steps come before every
 * screen.
 */
export async function finishProfile(page: Page) {
  await expect(page.getByRole('heading', { level: 1, name: 'Finish your profile' })).toBeVisible()
  await page.getByRole('button', { name: 'Continue' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Add a photo' })).toBeVisible()
  await page.getByRole('button', { name: 'Not now' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Home' })).toBeVisible()
}

export { expect }
