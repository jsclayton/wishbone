import { defineConfig, devices } from '@playwright/test'
import fs from 'node:fs'
import dns from 'node:dns'
import path from 'node:path'

// Node tries "localhost" as ::1 first, and a connect to it can stall 5 s before
// the fallback to 127.0.0.1. The server listens on IPv4. Playwright loads
// this file in every worker, so the setting reaches each test's Node client.
dns.setDefaultResultOrder('ipv4first')

// The e2e suite runs against the real binary (frontend embedded), built by
// `just build-e2e` (which `just test-e2e` runs) into ../bin/wishbone.
const repoRoot = path.resolve(import.meta.dirname, '..')
// Each run gets a fresh data dir, so tests never depend on earlier runs. The
// values go into process.env so test files and workers read the same ones.
process.env.WISHBONE_PORT ??= '8089'
if (!process.env.WISHBONE_DATA_DIR) {
  fs.mkdirSync(path.join(repoRoot, 'tmp'), { recursive: true })
  process.env.WISHBONE_DATA_DIR = fs.mkdtempSync(path.join(repoRoot, 'tmp', 'e2e-'))
}
const port = process.env.WISHBONE_PORT
const dataDir = process.env.WISHBONE_DATA_DIR
const baseURL = `http://localhost:${port}`

export default defineConfig({
  testDir: './e2e',
  // Sets up the server's first user, the admin, before any spec.
  globalSetup: './e2e/global-setup.ts',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never' }]] : 'html',
  use: {
    baseURL,
    // The app reports the browser's timezone to the server. Fixed here, it is the
    // same on every machine, and it is the server's TZ below.
    timezoneId: 'UTC',
    trace: 'on-first-retry',
    // A worker would serve a page from its cache, so a spec that is not about
    // the worker must not have one. Only service-worker.spec.ts turns workers
    // back on.
    serviceWorkers: 'block',
  },
  projects: [
    { name: 'desktop-chromium', use: { ...devices['Desktop Chrome'] } },
    // Locally, both projects share one server and one admin, whom global-setup.ts
    // sets up. CI runs each project alone on its own server (see the `e2e` job).
    { name: 'mobile-safari', use: { ...devices['iPhone 15'] } },
  ],
  webServer: {
    // The server writes its setup code to the log, and global-setup.ts reads it
    // from the file. `exec` makes the server the process Playwright stops.
    command: 'exec ./bin/wishbone serve > "$WISHBONE_DATA_DIR/server.log" 2>&1',
    url: `${baseURL}/api/v2/health`,
    cwd: repoRoot,
    // A reused server would have another data dir than this run's.
    reuseExistingServer: false,
    timeout: 60_000,
    env: {
      WISHBONE_PORT: port,
      WISHBONE_PUBLIC_URL: baseURL,
      WISHBONE_DATA_DIR: dataDir,
      // Specs on the shared server open login links from one client IP, in both
      // projects at once. The default of 10 a minute is for people, and trips
      // them. The rate limit has its own tests in Go.
      WISHBONE_RATE_LIMIT_REDEEM: '1000',
      // The e2e binary's test endpoints: POST /api/e2e/reveal gives the gifts for
      // one recipient as if the clock were ahead (cmd/wishbone/e2ehooks_e2e.go).
      WISHBONE_E2E_HOOKS: 'true',
      // The zone of a user who has no timezone. The browser below reports UTC too,
      // so no spec depends on the machine's zone.
      TZ: 'UTC',
    },
  },
})
