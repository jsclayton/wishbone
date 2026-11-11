import { useState, type FormEvent, type ReactNode } from 'react'
import { claimItem, type Item, type List, type Occasion, type User } from './api'
import { joinNames } from './gifts'
import { HowMany } from './HowMany'
import { OccasionPicker, pickable } from './OccasionPicker'
import { goBack } from './navigation'
import { notice } from './Picker'
import { pagePrice } from './price'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { occasionName } from './Upcoming'
import { useListState } from './useList'
import { useCollection } from './useCollection'
import { button, primary } from './ui'

const itemPath = (id: string) => `/items/${id}`

/** The names of a list's recipients, read as a sentence does. */
export function names(list: List): string {
  return joinNames(list.recipients.map((r) => r.name))
}

/**
 * "I'll get this": which occasion, and how many when more than one is still
 * wanted (docs/spec.md, Gifts, Screens). It is its own history entry, so Back
 * closes it. When someone else gets the last one first it says so without saying
 * who. The occasions are `GET /occasions?list_id=`, in the server's order, so the
 * first one, the default, is the soonest.
 */
export function ClaimItem({ itemId, me }: { itemId: string; me: User }) {
  return <Resource<Item> path={`/api/v2/items/${itemId}`}>{(item) => <ClaimScreen key={item.id} item={item} me={me} />}</Resource>
}

function ClaimScreen({ item, me }: { item: Item; me: User }) {
  const list = useListState(item.list_id)
  const { loaded } = useCollection<Occasion>(`/api/v2/occasions?list_id=${item.list_id}`)
  const [chosen, setChosen] = useState<string | null>(null)
  const [count, setCount] = useState(1)
  const [note, setNote] = useState<ReactNode>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [taken, setTaken] = useState(false)

  if (list === null || loaded.status === 'loading') return null
  const where = list === 'failed' ? 'this list' : `${names(list)}'s list`
  // A recipient's item has no claim state, and is not theirs to claim.
  if (item.still_wanted === undefined) return <NotFound />
  if (!taken && !item.can_claim && item.still_wanted === 0 && (item.my_claimed_quantity ?? 0) > 0) return <AllMine item={item} />
  if (!taken && !item.can_claim && item.still_wanted !== 0) return <CannotClaim item={item} />
  if (taken || !item.can_claim) return <Taken item={item} where={where} />
  if (loaded.status === 'failed') {
    return (
      <Screen title="I'll get this">
        <p role="alert">Something went wrong. Reload and try again.</p>
      </Screen>
    )
  }

  const occasions = pickable(loaded.rows)
  const occasion = chosen ?? occasions[0]?.id ?? ''
  const picked = occasions.find((o) => o.id === occasion)
  const most = item.is_unlimited ? undefined : (item.still_wanted ?? 1)
  const shown = most === undefined ? count : Math.min(count, most)
  const counted = item.is_unlimited || (most ?? 1) > 1
  const price = pagePrice(item)
  const who = list === 'failed' ? 'them' : names(list)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (busy || !picked) return
    setBusy(true)
    setError(null)
    setNote(null)
    try {
      const res = await claimItem(item.id, { quantity: shown, occasion_id: picked.id })
      if (res.ok) {
        goBack(itemPath(item.id))
        return
      }
      switch (res.reason) {
        case 'fully_claimed':
          setTaken(true)
          return
        case 'already_claimed':
          setNote(
            <>
              {`You're already getting this for ${occasionName(picked, me)}. Choose another occasion.`} To change how many, go to{' '}
              <a href="/giving" className="underline">
                Giving
              </a>
              .
            </>,
          )
          break
        default:
          setError(res.detail)
      }
    } catch {
      setError("That didn't work. Try again.")
    }
    setBusy(false)
  }

  return (
    <Screen title="I'll get this">
      <form onSubmit={(e) => void submit(e)} aria-label="I'll get this" className="flex max-w-xl flex-col gap-4">
        <div className="flex items-center gap-4">
          <span className="flex size-20 shrink-0 items-center justify-center overflow-hidden rounded-md bg-tile text-center text-[1rem]/[1.15] text-mute">
            {item.primary_image ? <img src={item.primary_image.url} alt="" className="size-full object-contain" /> : 'No picture'}
          </span>
          <div className="flex min-w-0 flex-col">
            <p className="text-[1.375rem]/[1.2] font-extrabold wrap-break-word">{item.title}</p>
            <p className="text-mute">
              <span>{`For ${who}`}</span>
              {price && `, ${price}`}
            </p>
          </div>
        </div>

        {note && (
          <p role="alert" className={`${notice} font-semibold`}>
            {note}
          </p>
        )}

        {occasions.length === 0 ? (
          <p className="text-xl">{`${who} has no upcoming occasions, so you can't get this yet.`}</p>
        ) : (
          <>
            <OccasionPicker occasions={occasions} value={occasion} me={me} onChange={setChosen} />
            {counted && (
              <section className="flex flex-col gap-2">
                <h2 className="text-[1.375rem] font-extrabold">How many</h2>
                <HowMany value={shown} most={most} onChange={setCount} />
              </section>
            )}
            <p>{`${who} won't know. Nobody else will either.`}</p>
          </>
        )}

        <div className="flex flex-col gap-2">
          {error && (
            <p role="alert" className={notice}>
              {error}
            </p>
          )}
          {occasions.length > 0 && (
            <button type="submit" className={primary} disabled={busy}>
              {"I'll get this"}
            </button>
          )}
          <button type="button" className={button} onClick={() => goBack(itemPath(item.id))}>
            Not now
          </button>
        </div>
      </form>
    </Screen>
  )
}

/** The giver's own gifts took everything that is wanted. */
function AllMine({ item }: { item: Item }) {
  return (
    <Screen title="You have all of this">
      <div className="flex max-w-xl flex-col gap-5.5">
        <p className="text-xl">{`Nothing more of ${item.title} is wanted. You claimed ${item.my_claimed_quantity}.`}</p>
        <a href="/giving" className={`${primary} flex items-center justify-center`}>
          Giving
        </a>
      </div>
    </Screen>
  )
}

/** Some are wanted, but this giver has nobody to give to right now. */
function CannotClaim({ item }: { item: Item }) {
  return (
    <Screen title="You can't get this">
      <div className="flex max-w-xl flex-col gap-5.5">
        <p className="text-xl">{`${item.title} can't be claimed right now.`}</p>
        <a href={`/items/${item.id}`} className={`${primary} flex items-center justify-center`}>
          Back to the item
        </a>
      </div>
    </Screen>
  )
}

/**
 * "Someone else just got this": the screen that replaces "I'll get this" when
 * the item has nothing left to get. It never says who.
 */
function Taken({ item, where }: { item: Item; where: string }) {
  return (
    <Screen title="Someone else just got this">
      <div className="flex max-w-xl flex-col gap-5.5">
        <p className="text-xl">{`${item.title} is no longer on ${where}, so it wasn't added to Giving.`}</p>
        <a href={`/lists/${item.list_id}`} className={`${primary} flex items-center justify-center`}>
          {`Back to ${where}`}
        </a>
      </div>
    </Screen>
  )
}
