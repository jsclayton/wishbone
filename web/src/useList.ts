import { useEffect, useState } from 'react'
import type { List } from './api'
import { request } from './http'

/**
 * The list at an address, for a screen that shows where an item is: the list,
 * `'failed'` when it could not be loaded, or `null` while it loads.
 */
export function useListState(id: string): List | 'failed' | null {
  const [state, setState] = useState<{ id: string; list: List | 'failed' } | null>(null)
  useEffect(() => {
    let live = true
    request(`/api/v2/lists/${id}`)
      .then(async (res): Promise<List | 'failed'> => (res.ok ? ((await res.json()) as List) : 'failed'))
      .catch((): 'failed' => 'failed')
      .then((list) => live && setState({ id, list }))
    return () => {
      live = false
    }
  }, [id])
  return state?.id === id ? state.list : null
}

/** The list at an address, or `null` while it loads, and if it fails. */
export function useList(id: string): List | null {
  const state = useListState(id)
  return state === 'failed' ? null : state
}
