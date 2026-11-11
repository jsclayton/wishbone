/** How long an API call may take. These are quick reads and writes; the margin is for poor mobile signal. */
export const DEFAULT_TIMEOUT_MS = 15_000

/**
 * For calls the server may spend longer on: extraction (up to 60 s), a price
 * refresh (up to 75 s), saving an item (its picture downloads share 30 s), and
 * uploads of up to 10 MB. It is above the longest server bound.
 */
export const LONG_TIMEOUT_MS = 90_000

/**
 * A signal that aborts when either one does. `AbortSignal.any` would do this,
 * but Safari only has it from 17.4. The timeout always fires in the end, so the
 * listeners on the caller's signal never outlive it.
 */
function firstOf(caller: AbortSignal, timeout: AbortSignal): AbortSignal {
  if (caller.aborted) return caller
  const first = new AbortController()
  const onCaller = () => first.abort(caller.reason)
  const onTimeout = () => first.abort(timeout.reason)
  caller.addEventListener('abort', onCaller)
  timeout.addEventListener('abort', onTimeout)
  first.signal.addEventListener('abort', () => {
    caller.removeEventListener('abort', onCaller)
    timeout.removeEventListener('abort', onTimeout)
  })
  return first.signal
}

/**
 * Every API call goes through here, so a server that accepts a connection and
 * never answers can't leave a screen waiting for good. The signal also stops a
 * body that hangs part way through `res.json()`. A timeout is a rejected
 * promise, like a dropped connection. Nothing retries, because a retry could
 * repeat a write. A caller that wants to stop waiting sooner passes its own
 * `signal` in `init`: the call ends at whichever comes first.
 */
export function request(
  url: string,
  init: RequestInit = {},
  timeoutMs: number = DEFAULT_TIMEOUT_MS,
): Promise<Response> {
  // Read fetch now, not at load: tests replace it after the modules load.
  const timeout = AbortSignal.timeout(timeoutMs)
  const signal = init.signal ? firstOf(init.signal, timeout) : timeout
  return globalThis.fetch(url, { ...init, signal })
}
