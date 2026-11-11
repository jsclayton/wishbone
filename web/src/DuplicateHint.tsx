import { useEffect, useState } from 'react'
import { fetchAll, type Item } from './api'
import { findDuplicate } from './duplicates'

/**
 * The items on a list, loaded again when the list changes. `null` while they load,
 * and if they can't be loaded: the hint is a courtesy, so a failure shows nothing.
 */
function useListItems(listId: string): Item[] | null {
  const [loaded, setLoaded] = useState<{ listId: string; items: Item[] } | null>(null)
  useEffect(() => {
    let live = true
    fetchAll<Item>(`/api/v2/lists/${listId}/items`)
      .then((items) => live && setLoaded({ listId, items }))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [listId])
  // The items of another list are not this list's.
  return loaded?.listId === listId ? loaded.items : null
}

/**
 * The hint on the add screen about a duplicate item (docs/spec.md, Items,
 * Screens). It checks only the list that the add goes to: it loads
 * `GET /lists/{id}/items`, and loads it again when `listId` changes. An item
 * matches by the address of a store link (`urls`: the pasted link as the search
 * returned it) or by its title, ignoring case and extra spaces. The hint is one
 * line that opens the item. It never blocks the add.
 */
export function DuplicateHint({ listId, title, urls }: { listId: string; title: string; urls: string[] }) {
  const items = useListItems(listId)
  const duplicate = items ? findDuplicate(items, title, urls) : null
  return (
    <div aria-live="polite">
      {duplicate && (
        <a href={`/items/${duplicate.id}`} className="inline-flex min-h-12 items-center font-semibold underline">
          Already on this list: {duplicate.title}
        </a>
      )}
    </div>
  )
}
