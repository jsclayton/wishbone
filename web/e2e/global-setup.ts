import fs from 'node:fs'
import path from 'node:path'
import { waitForSetupCode } from './server'
import { adminStatePath } from './support'

/**
 * Sets up the shared server before any spec runs: it reads the setup code from
 * the server's log, creates the admin, and saves the admin's session as a
 * storage state. A spec that calls `test.use({ signedIn: true })` starts with it.
 * Playwright starts `webServer` before this runs, and runs this once for every
 * `playwright test` run.
 *
 * Specs must not end or replace the admin's session, because they all share it.
 * A spec that needs another state makes its own server (`startServer`).
 */
export default async function globalSetup() {
  const port = process.env.WISHBONE_PORT
  const dataDir = process.env.WISHBONE_DATA_DIR!
  const code = await waitForSetupCode(path.join(dataDir, 'server.log'))

  // Dial 127.0.0.1, as support.ts does, and keep the Host header of the page.
  const res = await fetch(`http://127.0.0.1:${port}/api/v2/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Host: `localhost:${port}` },
    body: JSON.stringify({ setup_code: code, name: 'Ada', last_name: 'Lovelace', birthday: { month: 2, day: 29, year: null } }),
  })
  if (!res.ok) throw new Error(`global setup: setting up the admin failed: ${res.status} ${await res.text()}`)
  const token = res.headers
    .getSetCookie()
    .map((c) => c.split(';')[0]!)
    .find((c) => c.startsWith('wishbone_session='))
    ?.slice('wishbone_session='.length)
  if (!token) throw new Error('global setup: no session cookie')

  // Setup leaves the admin's profile unconfirmed, so the finish-profile step
  // comes before every screen. This call does what that step's Finish button
  // does, so no spec starts at it.
  const confirmed = await fetch(`http://127.0.0.1:${port}/api/v2/users/me/confirm-profile`, {
    method: 'POST',
    headers: { Host: `localhost:${port}`, Cookie: `wishbone_session=${token}` },
  })
  if (!confirmed.ok) throw new Error(`global setup: confirming the admin's profile failed: ${confirmed.status} ${await confirmed.text()}`)

  // The server marks the cookie Secure, and the test browsers store it without
  // that mark (see support.ts), so the saved state does too.
  const state = {
    cookies: [
      {
        name: 'wishbone_session',
        value: token,
        domain: 'localhost',
        path: '/',
        expires: Math.floor(Date.now() / 1000) + 365 * 24 * 60 * 60,
        httpOnly: true,
        secure: false,
        sameSite: 'Lax' as const,
      },
    ],
    origins: [],
  }
  fs.writeFileSync(adminStatePath(), JSON.stringify(state))
}
