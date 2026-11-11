import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

/** A stand-in for a worker that the browser found and is installing. */
function fakeWorker() {
  const listeners = new Set<() => void>()
  const worker = {
    state: 'installing',
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
  }
  return {
    worker,
    becomes: (state: string) =>
      act(async () => {
        worker.state = state
        listeners.forEach((fn) => fn())
        await new Promise((resolve) => setTimeout(resolve))
      }),
  }
}

/** A stand-in for navigator.serviceWorker and the registration it hands back. */
function fakeWorkers({ controller = true, active = true } = {}) {
  const updates = vi.fn<() => Promise<void>>(async () => {})
  const listeners = new Set<() => void>()
  const found = new Set<() => void>()
  const registration = {
    update: updates,
    active: active ? {} : null,
    installing: null as unknown,
    addEventListener: (_: string, fn: () => void) => found.add(fn),
  }
  const container = {
    controller: controller ? {} : null,
    register: vi.fn<(url: string) => Promise<unknown>>(async () => registration),
    addEventListener: (_: string, fn: () => void) => listeners.add(fn),
  }
  vi.stubGlobal('navigator', Object.assign(Object.create(navigator), { serviceWorker: container }))
  const shell = vi.fn<(url: string) => Promise<Response>>(async () => new Response('<script src="/assets/new.js"></script>'))
  vi.stubGlobal('fetch', shell)
  return {
    updates,
    container,
    registration,
    shell,
    takeOver: () =>
      act(async () => {
        listeners.forEach((fn) => fn())
        await new Promise((resolve) => setTimeout(resolve))
      }),
    /** The browser finds a new worker. Nothing else about it changes yet. */
    find: () => {
      const installing = fakeWorker()
      registration.installing = installing.worker
      found.forEach((fn) => fn())
      return installing
    },
  }
}

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new Event('visibilitychange'))
}

async function start({ installed = false } = {}) {
  vi.resetModules()
  const updates = await import('./updates')
  const { UpdateNotifier } = await import('./UpdateNotifier')
  const reload = vi.fn<() => void>()
  const stop = updates.watchForUpdates({ installed, reload })
  const Notifier = () => <UpdateNotifier reload={reload} />
  // Let register() settle.
  await act(async () => {})
  return { ...updates, UpdateNotifier: Notifier, reload, stop }
}

let stop: (() => void) | undefined

beforeEach(() => {
  document.head.innerHTML = '<script type="module" src="/assets/old.js"></script>'
})

afterEach(() => {
  stop?.()
  stop = undefined
  Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

test('a first visit has nothing to update from, so a takeover is not an update', async () => {
  const workers = fakeWorkers({ controller: false })
  const u = await start()
  stop = u.stop
  render(<u.UpdateNotifier />)
  await workers.takeOver()
  expect(u.isUpdateWaiting()).toBe(false)
  expect(screen.queryByText('A new version is available')).not.toBeInTheDocument()
})

test('a reload that already got the new shell is not an update', async () => {
  const workers = fakeWorkers()
  workers.shell.mockResolvedValue(new Response('<script src="/assets/old.js"></script>'))
  const u = await start()
  stop = u.stop
  render(<u.UpdateNotifier />)
  await workers.takeOver()
  expect(workers.shell).toHaveBeenCalledWith('/', expect.anything())
  expect(u.isUpdateWaiting()).toBe(false)
  expect(screen.queryByText('A new version is available')).not.toBeInTheDocument()
})

test('a browser extension script does not make a page look out of date', async () => {
  const workers = fakeWorkers()
  workers.shell.mockResolvedValue(new Response('<script src="/assets/old.js"></script>'))
  const extension = document.createElement('script')
  extension.src = 'chrome-extension://abc/inject.js'
  document.head.append(extension)
  const u = await start()
  stop = u.stop
  await workers.takeOver()
  expect(u.isUpdateWaiting()).toBe(false)
})

test('a stylesheet that is missing from the new shell is an update', async () => {
  const workers = fakeWorkers()
  document.head.insertAdjacentHTML('beforeend', '<link rel="stylesheet" href="/assets/old.css">')
  workers.shell.mockResolvedValue(new Response('<script src="/assets/old.js"></script><link rel="stylesheet" href="/assets/new.css">'))
  const u = await start()
  stop = u.stop
  await workers.takeOver()
  expect(u.isUpdateWaiting()).toBe(true)
})

test('if the new shell cannot be read, a takeover counts as an update', async () => {
  const workers = fakeWorkers()
  workers.shell.mockRejectedValue(new TypeError('Failed to fetch'))
  const u = await start()
  stop = u.stop
  await workers.takeOver()
  expect(u.isUpdateWaiting()).toBe(true)
})

test('a browser tab shows the banner, and Refresh reloads', async () => {
  const workers = fakeWorkers()
  const u = await start()
  stop = u.stop
  render(<u.UpdateNotifier />)
  expect(screen.queryByText('A new version is available')).not.toBeInTheDocument()
  await workers.takeOver()
  expect(screen.getByText('A new version is available')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(u.reload).toHaveBeenCalledTimes(1)
})

test('Dismiss hides the banner, but the update still waits for the next move', async () => {
  const workers = fakeWorkers()
  const u = await start()
  stop = u.stop
  render(<u.UpdateNotifier />)
  await workers.takeOver()
  await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
  expect(screen.queryByText('A new version is available')).not.toBeInTheDocument()
  expect(u.isUpdateWaiting()).toBe(true)
  expect(u.reload).not.toHaveBeenCalled()
})

test('a browser tab does not reload on its own when it is hidden', async () => {
  const workers = fakeWorkers()
  const u = await start()
  stop = u.stop
  await workers.takeOver()
  setVisibility('hidden')
  expect(u.reload).not.toHaveBeenCalled()
})

test('an installed app has no banner and reloads once when it is next hidden', async () => {
  const workers = fakeWorkers()
  const u = await start({ installed: true })
  stop = u.stop
  render(<u.UpdateNotifier />)
  await workers.takeOver()
  expect(screen.queryByText('A new version is available')).not.toBeInTheDocument()
  expect(u.isUpdateWaiting()).toBe(true)
  expect(u.reload).not.toHaveBeenCalled()
  setVisibility('hidden')
  setVisibility('visible')
  setVisibility('hidden')
  expect(u.reload).toHaveBeenCalledTimes(1)
})

test('an installed app that is already hidden reloads at once', async () => {
  const workers = fakeWorkers()
  const u = await start({ installed: true })
  stop = u.stop
  setVisibility('hidden')
  await workers.takeOver()
  expect(u.reload).toHaveBeenCalledTimes(1)
})

describe('a page that loaded with no controller', () => {
  test('hears about a new worker once it is activated', async () => {
    const workers = fakeWorkers({ controller: false })
    const u = await start()
    stop = u.stop
    render(<u.UpdateNotifier />)
    const found = workers.find()
    await found.becomes('installed')
    await found.becomes('activating')
    expect(u.isUpdateWaiting()).toBe(false)
    await found.becomes('activated')
    expect(u.isUpdateWaiting()).toBe(true)
    expect(screen.getByText('A new version is available')).toBeInTheDocument()
  })

  test('an installed app reloads when it is next hidden', async () => {
    const workers = fakeWorkers({ controller: false })
    const u = await start({ installed: true })
    stop = u.stop
    await workers.find().becomes('activated')
    expect(u.reload).not.toHaveBeenCalled()
    setVisibility('hidden')
    expect(u.reload).toHaveBeenCalledTimes(1)
  })

  test('stays silent through a first install', async () => {
    const workers = fakeWorkers({ controller: false, active: false })
    const u = await start({ installed: true })
    stop = u.stop
    render(<u.UpdateNotifier />)
    await workers.find().becomes('activated')
    setVisibility('hidden')
    expect(u.isUpdateWaiting()).toBe(false)
    expect(u.reload).not.toHaveBeenCalled()
    expect(screen.queryByText('A new version is available')).not.toBeInTheDocument()
  })

  test('hears about the next worker after a first install', async () => {
    const workers = fakeWorkers({ controller: false, active: false })
    const u = await start()
    stop = u.stop
    await workers.find().becomes('activated')
    workers.registration.active = {}
    await workers.find().becomes('activated')
    expect(u.isUpdateWaiting()).toBe(true)
  })

  test('a new worker with the same shell as this page is not an update', async () => {
    const workers = fakeWorkers({ controller: false })
    workers.shell.mockResolvedValue(new Response('<script src="/assets/old.js"></script>'))
    const u = await start()
    stop = u.stop
    await workers.find().becomes('activated')
    expect(workers.shell).toHaveBeenCalledWith('/', expect.anything())
    expect(u.isUpdateWaiting()).toBe(false)
  })

  test('a new worker that fails to install is not an update', async () => {
    const workers = fakeWorkers({ controller: false })
    const u = await start()
    stop = u.stop
    await workers.find().becomes('redundant')
    expect(u.isUpdateWaiting()).toBe(false)
  })
})

test('a page with a controller waits for the takeover, not for a new worker', async () => {
  const workers = fakeWorkers()
  const u = await start()
  stop = u.stop
  await workers.find().becomes('activated')
  expect(u.isUpdateWaiting()).toBe(false)
  await workers.takeOver()
  expect(u.isUpdateWaiting()).toBe(true)
})

test('checks for an update every 60 seconds and each time the page becomes visible', async () => {
  vi.useFakeTimers()
  const workers = fakeWorkers()
  const u = await start()
  stop = u.stop
  expect(workers.updates).not.toHaveBeenCalled()
  await act(async () => vi.advanceTimersByTime(59_999))
  expect(workers.updates).not.toHaveBeenCalled()
  await act(async () => vi.advanceTimersByTime(1))
  expect(workers.updates).toHaveBeenCalledTimes(1)
  setVisibility('hidden')
  expect(workers.updates).toHaveBeenCalledTimes(1)
  setVisibility('visible')
  expect(workers.updates).toHaveBeenCalledTimes(2)
})

test('a failed check or a failed registration stays quiet', async () => {
  vi.useFakeTimers()
  const workers = fakeWorkers()
  workers.updates.mockRejectedValue(new TypeError('Failed to fetch'))
  const first = await start()
  await act(async () => vi.advanceTimersByTime(60_000))
  first.stop()

  expect(workers.updates).toHaveBeenCalledTimes(1)

  workers.container.register.mockRejectedValue(new Error('no'))
  const second = await start()
  stop = second.stop
  await act(async () => vi.advanceTimersByTime(60_000))
  expect(workers.updates).toHaveBeenCalledTimes(1)
})

test('a browser that blocks registration without an error stays quiet', async () => {
  vi.useFakeTimers()
  const workers = fakeWorkers()
  workers.container.register.mockResolvedValue(undefined)
  const u = await start()
  stop = u.stop
  await act(async () => vi.advanceTimersByTime(60_000))
  setVisibility('hidden')
  setVisibility('visible')
  expect(workers.updates).not.toHaveBeenCalled()
})
