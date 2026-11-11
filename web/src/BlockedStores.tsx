import { useState, type FormEvent } from 'react'
import { allowStore, blockStore, fetchAll, type BlockedStore, type Collection, type User } from './api'
import { notice } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { field, primary } from './ui'

const path = '/api/v2/blocked-stores'

/**
 * The admin's Blocked stores screen: every blocked store, from
 * `GET /blocked-stores`, with a form to block one and a button to allow one
 * again (docs/spec.md, Admin and setup, Screens). It does not list the items
 * that link to a store: the admin has no access to content. For anyone else the
 * screen shows the missing-page message, and asks the server for nothing.
 */
export function BlockedStores({ me }: { me: User }) {
  if (!me.is_admin) return <NotFound />
  return (
    <Resource<Collection<BlockedStore>> path={path} all>
      {(stores) => <BlockedStoresScreen initial={stores.data} />}
    </Resource>
  )
}

function BlockedStoresScreen({ initial }: { initial: BlockedStore[] }) {
  // The list as the last change left it. A change loads it again, so it stays in the server's order.
  const [stores, setStores] = useState(initial)
  const [draft, setDraft] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function reload() {
    setStores(await fetchAll<BlockedStore>(path))
  }

  async function block(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (draft.trim() === '') {
      setError('Enter a store, such as temu.com.')
      return
    }
    setBusy(true)
    try {
      const res = await blockStore(draft.trim())
      if (res.ok) {
        setDraft('')
        await reload()
      } else {
        setError(res.detail)
      }
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  // Allowing a store is easy to undo (block it again), so it opens no confirmation screen.
  async function allow(s: BlockedStore) {
    setError(null)
    setBusy(true)
    try {
      await allowStore(s.id)
      await reload()
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title="Blocked stores">
      <div className="flex flex-col gap-4 sm:max-w-xl">
        <p>Nobody can add links from these stores or their subdomains. Links already on lists stay.</p>
        <form onSubmit={block} noValidate className="flex flex-col gap-1.5">
          <label htmlFor="block-add" className="font-semibold">
            Block a store
          </label>
          <div className="flex gap-2">
            <input
              id="block-add"
              className={`${field} min-w-0 flex-1`}
              value={draft}
              placeholder="temu.com"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              onChange={(e) => setDraft(e.target.value)}
            />
            <button type="submit" className={`${primary} min-h-14.5 w-22 shrink-0 px-0 py-0 text-lg`} disabled={busy}>
              Block
            </button>
          </div>
        </form>
        {error && (
          <p role="alert" className={notice}>
            {error}
          </p>
        )}
        {stores.length === 0 ? (
          <p className="text-xl">No stores are blocked.</p>
        ) : (
          <ul aria-label="Blocked stores" className="border-b border-hair">
            {stores.map((s) => (
              <li key={s.id} className="grid min-h-15.5 grid-cols-[minmax(0,1fr)_auto] items-center border-t border-hair">
                <strong className="font-semibold wrap-anywhere">{s.store}</strong>
                {s.can_delete && (
                  <button type="button" aria-label={`Allow ${s.store}`} className="h-12 pl-3 font-semibold underline" disabled={busy} onClick={() => void allow(s)}>
                    Allow
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </Screen>
  )
}
