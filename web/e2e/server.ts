import { spawn, type ChildProcess } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import path from 'node:path'

const repoRoot = path.resolve(import.meta.dirname, '../..')

/**
 * The setup code in a server's log. The server writes it when it starts with no
 * users, as `setup_code=ABCD-EFGH-JKMN`. Null while the log has none.
 */
export function setupCodeIn(log: string): string | null {
  return /setup_code="?([A-Z0-9-]+)/.exec(log)?.[1] ?? null
}

/** Waits for the server's log to show its setup code. */
export async function waitForSetupCode(logFile: string, timeoutMs = 20_000): Promise<string> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    const log = fs.existsSync(logFile) ? fs.readFileSync(logFile, 'utf8') : ''
    const code = setupCodeIn(log)
    if (code) return code
    if (Date.now() > deadline) throw new Error(`no setup code in ${logFile} after ${timeoutMs} ms. Log:\n${log}`)
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
}

/** A port nothing listens on, right now. */
export async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const probe = net.createServer()
    probe.once('error', reject)
    probe.listen(0, '127.0.0.1', () => {
      const { port } = probe.address() as net.AddressInfo
      probe.close(() => resolve(port))
    })
  })
}

export type TestServer = {
  /** `http://localhost:<port>`, the address a browser uses. */
  url: string
  /** The setup code from the server's log. */
  setupCode: string
  stop(): Promise<void>
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

/**
 * Whether something on the port answers the health check. The probe gives up
 * after a second, and that counts as "no": a process that holds the port may
 * accept a connection and never answer.
 */
export async function healthy(port: number): Promise<boolean> {
  return fetch(`http://127.0.0.1:${port}/api/v2/health`, { signal: AbortSignal.timeout(1000) }).then(
    (r) => r.ok,
    () => false,
  )
}

/**
 * Starts a server on `port` with an empty data dir. It returns `null` when the
 * server exits before it listens, which is what a port that someone else took
 * does: `freePort` frees its port before the server binds it, so another process
 * can take it in between. It throws, at once and with the server's log, when the
 * server exits for any other reason, or does not start in 20 s. It needs
 * `bin/wishbone`, which `just test-e2e` builds. `env` adds to the server's
 * environment.
 */
export async function launchServer(port: number, env: Record<string, string> = {}): Promise<TestServer | null> {
  const url = `http://localhost:${port}`
  fs.mkdirSync(path.join(repoRoot, 'tmp'), { recursive: true })
  const dataDir = fs.mkdtempSync(path.join(repoRoot, 'tmp', 'e2e-own-'))
  const logFile = path.join(dataDir, 'server.log')
  const log = fs.openSync(logFile, 'w')

  const child: ChildProcess = spawn('./bin/wishbone', ['serve'], {
    cwd: repoRoot,
    stdio: ['ignore', log, log],
    // TZ is the zone of a user with no timezone, and the browsers report UTC (playwright.config.ts).
    env: { ...process.env, TZ: 'UTC', WISHBONE_PORT: String(port), WISHBONE_PUBLIC_URL: url, WISHBONE_DATA_DIR: dataDir, ...env },
  })
  fs.closeSync(log)
  const exited = new Promise<void>((resolve) => child.once('exit', () => resolve()))
  const stop = async () => {
    child.kill('SIGTERM')
    await exited
    fs.rmSync(dataDir, { recursive: true, force: true })
  }
  const logText = () => (fs.existsSync(logFile) ? fs.readFileSync(logFile, 'utf8') : '')

  try {
    const deadline = Date.now() + 20_000
    let setupCode: string | null = null
    for (;;) {
      // A server that exited will never answer: say so now, not after the wait.
      if (child.exitCode !== null || child.signalCode !== null) {
        const text = logText()
        if (/address already in use|bind: /i.test(text)) {
          fs.rmSync(dataDir, { recursive: true, force: true })
          return null
        }
        throw new Error(`the server exited before it was listening. Log:
${text}`)
      }
      setupCode ??= setupCodeIn(logText())
      // The code is logged before the server listens.
      // Look at the exit again just before the probe.
      const exitedNow = child.exitCode !== null || child.signalCode !== null
      const up = setupCode !== null && !exitedNow && (await healthy(port))
      if (up) {
        // Another process on the port would answer too. A server that could not
        // bind it exits within a moment, so see that this one stays.
        await sleep(300)
        if (child.exitCode === null && child.signalCode === null) return { url, setupCode: setupCode!, stop }
        continue
      }
      if (Date.now() > deadline) throw new Error(`the server did not start listening. Log:
${logText()}`)
      await sleep(100)
    }
  } catch (error) {
    await stop()
    throw error
  }
}

/**
 * Starts a Wishbone server of its own, on a random port with an empty data
 * dir, so a test can do what the shared server can't take: set up an instance,
 * or end the session that every other test uses. If the port was taken between
 * picking it and binding it, it picks another and starts again. The caller
 * stops the server. `pickPort` is for the test of this function. `env` adds to
 * the server's environment.
 */
export async function startServer(pickPort: () => Promise<number> = freePort, env: Record<string, string> = {}): Promise<TestServer> {
  const attempts = 5
  for (let attempt = 1; attempt <= attempts; attempt++) {
    const server = await launchServer(await pickPort(), env)
    if (server) return server
  }
  throw new Error(`no server started: the port was taken ${attempts} times`)
}
