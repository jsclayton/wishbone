import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { DEFAULT_TIMEOUT_MS, LONG_TIMEOUT_MS, request } from './http'

const realFetch = globalThis.fetch
const nativeAny = AbortSignal.any

beforeEach(() => {
  vi.useFakeTimers()
  // The native AbortSignal.timeout ignores fake timers. This one follows them.
  vi.spyOn(AbortSignal, 'timeout').mockImplementation((ms) => {
    const controller = new AbortController()
    setTimeout(() => controller.abort(new DOMException('The operation timed out.', 'TimeoutError')), ms)
    return controller.signal
  })
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  globalThis.fetch = realFetch
})

// A fetch that never answers and gives up when its signal aborts.
function hangingFetch() {
  return vi.fn<(url: string, init?: RequestInit) => Promise<Response>>((_url, init) => {
    return new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(init.signal?.reason))
    })
  })
}

it('rejects once the default limit has passed, and not before', async () => {
  globalThis.fetch = hangingFetch() as typeof fetch
  let rejected = false
  const p = request('/api/v2/me').catch(() => {
    rejected = true
  })
  await vi.advanceTimersByTimeAsync(DEFAULT_TIMEOUT_MS - 1)
  expect(rejected).toBe(false)
  await vi.advanceTimersByTimeAsync(1)
  await p
  expect(rejected).toBe(true)
})

it('lets a long-limit call outlast the default limit', async () => {
  globalThis.fetch = hangingFetch() as typeof fetch
  let rejected = false
  const p = request('/api/v2/me/extract', { method: 'POST' }, LONG_TIMEOUT_MS).catch(() => {
    rejected = true
  })
  await vi.advanceTimersByTimeAsync(DEFAULT_TIMEOUT_MS + 1)
  expect(rejected).toBe(false)
  await vi.advanceTimersByTimeAsync(LONG_TIMEOUT_MS - DEFAULT_TIMEOUT_MS)
  await p
  expect(rejected).toBe(true)
})

it('keeps the limit in force while the body is read', async () => {
  globalThis.fetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>((_url, init) => {
    const body = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('{"na'))
        init?.signal?.addEventListener('abort', () => controller.error(init.signal?.reason))
      },
    })
    return Promise.resolve(new Response(body, { status: 200 }))
  }) as typeof fetch
  const res = await request('/api/v2/me')
  let rejected = false
  const p = res.json().catch(() => {
    rejected = true
  })
  await vi.advanceTimersByTimeAsync(DEFAULT_TIMEOUT_MS)
  await p
  expect(rejected).toBe(true)
})

it('passes the url and init through and adds a signal', async () => {
  const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response('{}'))
  globalThis.fetch = fetchMock as typeof fetch
  await request('/api/v2/x', { method: 'POST', body: 'b' })
  expect(fetchMock).toHaveBeenCalledWith(
    '/api/v2/x',
    expect.objectContaining({ method: 'POST', body: 'b', signal: expect.any(AbortSignal) }),
  )
})

describe('with a signal from the caller', () => {
  // Safari before 17.4 has no AbortSignal.any, so `request` must not need it.
  beforeEach(() => {
    Object.assign(AbortSignal, { any: undefined })
  })
  afterEach(() => {
    Object.assign(AbortSignal, { any: nativeAny })
  })

  const hang = () => {
    const seen: AbortSignal[] = []
    globalThis.fetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          seen.push(init!.signal!)
          init?.signal?.addEventListener('abort', () => reject(init.signal?.reason))
        }),
    ) as typeof fetch
    return seen
  }

  it('stops when the caller aborts, with the reason the caller gave', async () => {
    hang()
    const caller = new AbortController()
    const p = request('/a', { signal: caller.signal })
    caller.abort('stop')
    await expect(p).rejects.toBe('stop')
  })

  it('still stops at the time limit', async () => {
    hang()
    const p = request('/a', { signal: new AbortController().signal })
    const outcome = p.catch((e: unknown) => e)
    await vi.advanceTimersByTimeAsync(DEFAULT_TIMEOUT_MS)
    expect(await outcome).toMatchObject({ name: 'TimeoutError' })
  })

  it('starts out aborted when the caller has already aborted', async () => {
    const seen = hang()
    const caller = new AbortController()
    caller.abort('early')
    request('/a', { signal: caller.signal }).catch(() => {})
    expect(seen[0]?.aborted).toBe(true)
    expect(seen[0]?.reason).toBe('early')
  })

  it('takes its listeners off the caller signal once the call has ended', async () => {
    hang()
    const caller = new AbortController()
    const add = vi.spyOn(caller.signal, 'addEventListener')
    const remove = vi.spyOn(caller.signal, 'removeEventListener')
    const p = request('/a', { signal: caller.signal }).catch(() => {})
    await vi.advanceTimersByTimeAsync(DEFAULT_TIMEOUT_MS)
    await p
    expect(add).toHaveBeenCalledTimes(1)
    expect(remove).toHaveBeenCalledWith('abort', add.mock.calls[0]?.[1])
  })
})

it('reads fetch when it is called, not when the module loads', async () => {
  const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response('{}'))
  globalThis.fetch = fetchMock as typeof fetch
  await request('/a')
  expect(fetchMock).toHaveBeenCalledTimes(1)
})
