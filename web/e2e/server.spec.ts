import { expect, test } from '@playwright/test'
import net from 'node:net'
import { freePort, healthy, launchServer, startServer } from './server'

// `startServer` picks a port, frees it, and starts the server on it. Another
// process can take the port in between. These tests take the port
// on purpose, the way that process would.
test.describe('startServer', () => {
  /** Listens on every address of a port, as the server does. */
  async function occupy(): Promise<{ port: number; release: () => Promise<void> }> {
    // It accepts connections and says nothing. Release closes them, or close() would wait for them.
    const open = new Set<net.Socket>()
    const blocker = net.createServer((socket) => {
      open.add(socket)
      socket.on('close', () => open.delete(socket))
      socket.on('error', () => {})
    })
    await new Promise<void>((resolve, reject) => {
      blocker.once('error', reject)
      blocker.listen({ port: 0, host: '::' }, resolve)
    })
    const { port } = blocker.address() as net.AddressInfo
    return {
      port,
      release: () =>
        new Promise((resolve) => {
          blocker.close(() => resolve())
          for (const socket of open) socket.destroy()
        }),
    }
  }

  test('a port that is taken makes the server exit, and launchServer says so at once', async () => {
    const taken = await occupy()
    try {
      const started = Date.now()
      expect(await launchServer(taken.port)).toBeNull()
      expect(Date.now() - started, 'it did not wait out the 20 s start-up').toBeLessThan(10_000)
    } finally {
      await taken.release()
    }
  })

  test('a process that holds the port and never answers cannot hang the health check', async () => {
    // The blocker accepts connections and says nothing, as a process that is not a server might.
    const silent = await occupy()
    try {
      const started = Date.now()
      expect(await healthy(silent.port)).toBe(false)
      expect(Date.now() - started, 'it gave up after about a second').toBeLessThan(5_000)
    } finally {
      await silent.release()
    }
  })

  test('picks another port and starts again when the first one was taken', async () => {
    const taken = await occupy()
    const picks = [taken.port]
    const server = await startServer(async () => picks.shift() ?? freePort())
    try {
      expect(picks).toEqual([])
      expect(server.url).not.toContain(`:${taken.port}`)
      expect((await fetch(`${server.url.replace('localhost', '127.0.0.1')}/api/v2/health`)).ok).toBe(true)
      expect(server.setupCode).toMatch(/^[A-Z0-9]{4}(-[A-Z0-9]{4}){2}$/)
    } finally {
      await server.stop()
      await taken.release()
    }
  })

  test('a server that stops for another reason is an error with its log, and not a wait', async () => {
    // The server refuses a port above 65535 when it reads its settings, before it listens.
    const started = Date.now()
    await expect(launchServer(70_000)).rejects.toThrow(/exited before it was listening/)
    expect(Date.now() - started).toBeLessThan(10_000)
  })
})
