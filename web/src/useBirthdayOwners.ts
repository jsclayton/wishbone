import { useEffect, useState } from 'react'
import { fetchAll, type Gift, type Occasion } from './api'

/** A gift's birthday occasion has no user in its summary. On a list with one recipient the recipient is the user. */
const needsLookup = (g: Gift) => g.occasion?.kind === 'birthday' && (g.recipients?.length ?? 0) !== 1

/**
 * The names of whose birthday each birthday occasion of the gifts is, by the
 * occasion's ID, for the gifts on a list with several recipients. The user comes
 * from `GET /occasions?list_id=`, which has each occasion's `user`. It is empty
 * until the lookup is in, and when it fails: the words then say "a birthday".
 */
export function useBirthdayOwners(gifts: Gift[]): Map<string, string> {
  const listIds = [...new Set(gifts.filter(needsLookup).map((g) => g.list.id))].sort()
  const key = listIds.join(',')
  const [found, setFound] = useState<{ key: string; owners: Map<string, string> }>({ key: '', owners: new Map() })

  useEffect(() => {
    if (key === '') return
    let live = true
    Promise.all(key.split(',').map((id) => fetchAll<Occasion>(`/api/v2/occasions?list_id=${id}`)))
      .then((lists) => {
        const owners = new Map<string, string>()
        for (const o of lists.flat()) if (o.kind === 'birthday' && o.user) owners.set(o.id, o.user.name)
        if (live) setFound({ key, owners })
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [key])

  return found.key === key ? found.owners : new Map()
}
