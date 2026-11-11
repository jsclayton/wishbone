import { useCallback, useEffect, useState } from 'react'
import { fetchAll } from './api'

/** What a part of a screen knows about a collection it loads for itself. */
export type Loaded<T> = { status: 'loading' } | { status: 'failed' } | { status: 'ready'; rows: T[] }

/**
 * Loads every page of a collection for a part of a screen that sits beside
 * other parts, such as the users of a group. `Resource` is for the one thing a
 * whole screen is about, and replaces the screen when it fails. This shows the
 * failure in its place instead, so one part failing leaves the rest of the
 * screen. `reload` asks again, and the rows on the screen stay until the new ones
 * are in. A `null` path loads nothing, for a part that some users don't have.
 */
export function useCollection<T>(path: string | null): { loaded: Loaded<T>; reload: () => void } {
  const [attempt, setAttempt] = useState(0)
  const [result, setResult] = useState<{ path: string | null; attempt: number; loaded: Loaded<T> }>({ path, attempt: 0, loaded: { status: 'loading' } })

  useEffect(() => {
    if (path === null) return
    let live = true
    fetchAll<T>(path)
      .then((rows): Loaded<T> => ({ status: 'ready', rows }))
      .catch((): Loaded<T> => ({ status: 'failed' }))
      .then((loaded) => live && setResult({ path, attempt, loaded }))
    return () => {
      live = false
    }
  }, [path, attempt])

  const reload = useCallback(() => setAttempt((n) => n + 1), [])
  // Rows for another path are not this path's rows.
  const loaded: Loaded<T> = result.path === path ? result.loaded : { status: 'loading' }
  return { loaded, reload }
}
