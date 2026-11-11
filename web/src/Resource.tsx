import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { pageAfter, type Collection } from './api'
import { request } from './http'
import { CantReach, NotFound } from './Screen'

/**
 * What a screen does when the server says the session is gone: a 401 on a
 * signed-in call. The app provides it, and it asks again who is signed in, so
 * the signed-out page shows. A screen with no provider (a test) does nothing.
 */
export const SignedOutContext = createContext<() => void>(() => {})

type Settled<T> = { status: 'ready'; data: T; etag: string | null } | { status: 'missing' } | { status: 'signed-out' } | { status: 'unreachable' }

/**
 * Loads the resource a screen is about and shows the screen once it is in. The
 * API answers 404 for a resource that does not exist and for one the caller
 * can't see, so both show the same missing-page message (docs/spec.md,
 * API principles, Screens). A 401 means the session ended, for example in
 * another tab, so the app moves to the signed-out page. A server that fails
 * shows "Can't reach Wishbone". Every screen about one resource uses this, so
 * none of them handles those.
 *
 * `path` is the resource's address, such as `/api/v2/items/9`. A new `path`
 * loads the new resource.
 *
 * With `all`, the resource is a collection, and the screen waits for every
 * page: a client always follows `next_cursor` (docs/spec.md, API principles,
 * Collections). The screen gets one collection with all the rows.
 */
export function Resource<T>({
  path,
  all = false,
  children,
}: {
  path: string
  all?: boolean
  children: (data: T, etag: string | null) => ReactNode
}) {
  const onSignedOut = useContext(SignedOutContext)
  // What the last request settled to, and which request it was. A result for
  // another path or attempt is not the current one: the screen shows nothing
  // until the current request is in.
  const [result, setResult] = useState<{ path: string; attempt: number; settled: Settled<T> } | null>(null)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let live = true
    // The answer to one request, as a settled state, or the body when it is ready.
    async function load(url: string): Promise<Settled<Collection<unknown>>> {
      const res = await request(url)
      if (res.status === 401) return { status: 'signed-out' }
      if (res.status === 404) return { status: 'missing' }
      if (!res.ok) return { status: 'unreachable' }
      return { status: 'ready', data: await res.json(), etag: res.headers.get('ETag') }
    }

    async function loadAll(): Promise<Settled<T>> {
      const first = await load(path)
      if (first.status !== 'ready' || !all) return first as Settled<T>
      let body = first.data
      while (body.next_cursor) {
        const next = await load(pageAfter(path, body.next_cursor))
        if (next.status !== 'ready') return next as Settled<T>
        body = { ...next.data, data: [...body.data, ...next.data.data] }
      }
      return { status: 'ready', data: body as T, etag: first.etag }
    }

    loadAll()
      .catch((): Settled<T> => ({ status: 'unreachable' }))
      .then((settled) => live && setResult({ path, attempt, settled }))
    return () => {
      live = false
    }
  }, [path, all, attempt])

  const current = result && result.path === path && result.attempt === attempt ? result.settled : null
  const signedOut = current?.status === 'signed-out'
  useEffect(() => {
    if (signedOut) onSignedOut()
  }, [signedOut, onSignedOut])

  if (!current) return null
  switch (current.status) {
    case 'signed-out':
      return null
    case 'missing':
      return <NotFound />
    case 'unreachable':
      return <CantReach onRetry={() => setAttempt((n) => n + 1)} retrying={false} />
    case 'ready':
      return children(current.data, current.etag)
  }
}
