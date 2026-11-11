import { useSyncExternalStore } from 'react'
import { request } from './http'

const CHECK_EVERY_MS = 60_000

let waiting = false
let bannerVisible = false
const listeners = new Set<() => void>()

function setBanner(visible: boolean) {
  bannerVisible = visible
  listeners.forEach((listener) => listener())
}

/** True once a new version has taken over this page. Stays true until a reload. */
export function isUpdateWaiting() {
  return waiting
}

export function useUpdateBanner() {
  return useSyncExternalStore((listener) => {
    listeners.add(listener)
    return () => listeners.delete(listener)
  }, () => bannerVisible)
}

export function dismissUpdateBanner() {
  setBanner(false)
}

export function isInstalledApp() {
  const standalone = (navigator as Navigator & { standalone?: boolean }).standalone === true
  return standalone || window.matchMedia('(display-mode: standalone)').matches
}

/**
 * Whether this page already runs the build of the worker in control. A reload
 * after a deploy gets the new shell from the network, and only then does the
 * new worker take over. That takeover is not news to this page. The worker
 * answers `/` from its cache, so this needs no server. A page with no controller
 * asks the server instead. Only same-origin files count: a browser extension can
 * add its own, and those are in no shell.
 */
async function runsNewShell() {
  try {
    const shell = await (await request('/')).text()
    const files = document.querySelectorAll<HTMLScriptElement | HTMLLinkElement>('script[src], link[rel="stylesheet"]')
    return [...files]
      .map((file) => new URL('src' in file ? file.src : file.href))
      .filter((url) => url.origin === window.location.origin)
      .every((url) => shell.includes(url.pathname))
  } catch {
    return false
  }
}

type Options = { installed: boolean; reload: () => void }

/**
 * Registers the service worker and watches for a new version. An installed app
 * reloads when it is next hidden, so it can't throw away what someone is
 * typing. A browser tab asks first. Either way, links load the whole page while
 * an update waits (see isInAppClick), so old code can't run for long.
 *
 * Returns a function that stops the checks.
 */
export function watchForUpdates({ installed, reload }: Options = { installed: isInstalledApp(), reload: () => window.location.reload() }) {
  const workers = navigator.serviceWorker
  // The worker doesn't call clients.claim(), so a page with no controller never
  // sees a takeover. It watches for a new worker on the registration instead.
  const hadController = workers.controller !== null
  let check = () => {}
  let reloading = false

  function reloadOnce() {
    if (reloading) return
    reloading = true
    reload()
  }

  async function newWorkerActive() {
    if (waiting || (await runsNewShell())) return
    waiting = true
    if (!installed) setBanner(true)
    else if (document.visibilityState === 'hidden') reloadOnce()
  }

  if (hadController) workers.addEventListener('controllerchange', () => void newWorkerActive())

  function onVisibilityChange() {
    if (document.visibilityState === 'visible') check()
    else if (installed && waiting) reloadOnce()
  }
  document.addEventListener('visibilitychange', onVisibilityChange)
  // Moves between screens no longer load a page, so the browser's own check
  // doesn't run. iOS stops timers in the background, hence the visibility check.
  const timer = setInterval(() => check(), CHECK_EVERY_MS)

  workers
    .register('/sw.js')
    .then((registration) => {
      // Playwright's "block" resolves with nothing.
      if (!registration) return
      check = () => void registration.update().catch(() => {})
      if (hadController) return
      registration.addEventListener('updatefound', () => {
        const found = registration.installing
        // With no active worker this is a first install, not a new version.
        if (!found || !registration.active) return
        found.addEventListener('statechange', () => {
          if (found.state === 'activated') void newWorkerActive()
        })
      })
    })
    // The app works without a worker, so this needs no console error.
    .catch(() => {})

  return () => {
    clearInterval(timer)
    document.removeEventListener('visibilitychange', onVisibilityChange)
  }
}
