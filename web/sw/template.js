// The service worker. It caches the app shell and nothing else. The build fills
// in VERSION and FILES and writes the result to dist/sw.js (see shellWorker in
// vite.config.ts). Read "Why the app shell is cached" in docs/rationale.md
// before you add a case here.
//
// There is no cache.put at runtime: the cache holds only FILES, so API data,
// login link tokens, and claims can't enter it.

const VERSION = '__VERSION__'
const FILES = ['__FILES__']

const CACHE = `wishbone-shell-${VERSION}`
const SHELL = '/'
const DOCUMENT_TIMEOUT_MS = 2000

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      // Skip the HTTP cache, so that a stale copy can't enter the new shell.
      .then((cache) => cache.addAll(FILES.map((file) => new Request(file, { cache: 'reload' }))))
      // Without this the new worker waits until every tab closes. A running
      // page needs nothing from the old cache: the build has no dynamic import().
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(keys.filter((key) => key.startsWith('wishbone-shell-') && key !== CACHE).map((key) => caches.delete(key))),
      ),
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  if (url.pathname === '/metrics' || url.pathname === '/api' || url.pathname.startsWith('/api/')) return

  if (request.mode === 'navigate') {
    event.respondWith(networkFirst(request))
    return
  }
  const path = url.pathname + url.search
  if (FILES.includes(path)) event.respondWith(cacheFirst(request, path))
})

/** The network decides which shell a reload gets, so a deploy shows at once. */
async function networkFirst(request) {
  try {
    const response = await withTimeout(fetch(request), DOCUMENT_TIMEOUT_MS)
    // A navigation doesn't follow redirects, so the browser must get this one.
    if (response.ok || response.type === 'opaqueredirect') return response
  } catch {
    // No server, or too slow. The cached shell is the answer.
  }
  const cache = await caches.open(CACHE)
  return (await cache.match(SHELL)) ?? Response.error()
}

async function cacheFirst(request, path) {
  const cache = await caches.open(CACHE)
  return (await cache.match(path)) ?? fetch(request)
}

function withTimeout(promise, ms) {
  let timer
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error('timed out')), ms)
  })
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer))
}
