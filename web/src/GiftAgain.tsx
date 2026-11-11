import { useState } from 'react'
import { addItemAgain, restoreGift, type List, type ReceivedGift, type User } from './api'
import { Confirm } from './Confirm'
import { GiftPicture } from './Giving'
import { ListChoice } from './ListChoice'
import { listParts } from './listOrder'
import { replaceTo } from './navigation'
import { notice } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { primary } from './ui'
import { useCollection } from './useCollection'

/**
 * The screen before "I didn't get this" (docs/spec.md, Gifts, Restore), on its
 * own address so it is a history entry. It says the same whatever the giver or the
 * item is now. "Keep it" changes nothing and returns to Received. The gift is a
 * 404 after the restore, so the screen goes back to Received.
 */
export function RestoreGift({ giftId }: { giftId: string }) {
  return (
    <Resource<ReceivedGift> path={`/api/v2/gifts/${giftId}`}>
      {(gift) =>
        gift.can_restore ? (
          <Confirm
            heading="Didn't get this?"
            statement={`${gift.quantity > 1 ? `${gift.title} (${gift.quantity})` : gift.title} leaves Received.`}
            details={['The person who gave it can give it again.']}
            action="I didn't get this"
            keepHref="/received"
            onConfirm={async () => {
              await restoreGift(gift.id)
              replaceTo('/received')
            }}
          />
        ) : (
          <NotFound />
        )
      }
    </Resource>
  )
}

/**
 * The list that Add again starts on (docs/spec.md, Gifts, Screens): the gift's
 * own list while it still exists and the user can edit it. Otherwise the user's
 * own primary list when they are one of the gift's recipients, and when they are
 * not, the primary list of the first of the gift's recipients, by name, who is
 * their child. `lists` are the lists the user can edit. `undefined` when none of
 * those is one the user can edit: the screen then starts on the first list.
 */
export function addAgainList(gift: Pick<ReceivedGift, 'list' | 'recipients'>, lists: List[], me: User): string | undefined {
  if (lists.some((l) => l.id === gift.list.id)) return gift.list.id
  const recipients = gift.recipients ?? []
  const primaryOf = (userId: string) => lists.find((l) => l.is_primary && l.recipients.some((r) => r.id === userId))?.id
  if (recipients.some((r) => r.id === me.id)) return primaryOf(me.id)
  const children = recipients.filter((r) => me.children.some((c) => c.id === r.id)).toSorted((a, b) => a.name.localeCompare(b.name))
  for (const child of children) {
    const id = primaryOf(child.id)
    if (id) return id
  }
  return undefined
}

/**
 * Add again: a new item from a given gift, on a list the user can edit. The item
 * has the gift's title, image and price. The screen says nothing of the gift's
 * item, which may be gone or claimed.
 */
export function AddAgain({ giftId, me }: { giftId: string; me: User }) {
  return (
    <Resource<ReceivedGift> path={`/api/v2/gifts/${giftId}`}>
      {(gift) => (gift.can_add_again ? <AddAgainScreen key={gift.id} gift={gift} me={me} /> : <NotFound />)}
    </Resource>
  )
}

function AddAgainScreen({ gift, me }: { gift: ReceivedGift; me: User }) {
  const { loaded } = useCollection<List>('/api/v2/lists?can_edit=true')
  const [picked, setPicked] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  if (loaded.status === 'loading') return null
  if (loaded.status === 'failed') {
    return (
      <Screen title="Add again">
        <p role="alert">Could not load your lists. Reload and try again.</p>
      </Screen>
    )
  }
  const parts = listParts(loaded.rows, me)
  const all = parts.flatMap((p) => p.lists)
  if (all.length === 0) {
    return (
      <Screen title="Add again">
        <p className="text-xl">You have no list to add it to.</p>
        <a href="/received" className="font-semibold underline">
          Back to Received
        </a>
      </Screen>
    )
  }
  const target = picked ?? addAgainList(gift, loaded.rows, me) ?? all[0]!.id
  const targetName = all.find((l) => l.id === target)?.name ?? ''

  async function add() {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      const res = await addItemAgain(target, gift.id)
      if (res.ok) {
        replaceTo(`/lists/${target}`)
        return
      }
      setError(res.detail)
    } catch {
      setError("That didn't work. Try again.")
    }
    setBusy(false)
  }

  return (
    <Screen title="Add again">
      <div className="flex max-w-xl flex-col gap-4">
        <div className="flex items-center gap-4">
          <GiftPicture gift={gift} />
          <p className="min-w-0 text-[1.375rem]/[1.2] font-extrabold wrap-break-word">{gift.title}</p>
        </div>
        <ListChoice me={me} value={target} onChange={setPicked} fallback={<p className="text-lg text-mute">To {targetName}</p>} />
        {error && (
          <p role="alert" className={notice}>
            {error}
          </p>
        )}
        <div className="flex flex-col gap-2 sm:max-w-md">
          <button type="button" className={primary} disabled={busy} onClick={() => void add()}>
            Add again
          </button>
          <a href="/received" className="flex min-h-12 items-center justify-center font-semibold underline">
            Cancel
          </a>
        </div>
      </div>
    </Screen>
  )
}
