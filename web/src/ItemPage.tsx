import { useState } from 'react'
import { deleteItem, updateItem, type Gift, type Item, type ItemChanges, type User } from './api'
import { Confirm } from './Confirm'
import { ItemForm } from './ItemForm'
import { claimedText, stillWantedText } from './gifts'
import { useBirthdayOwners } from './useBirthdayOwners'
import { ItemImages } from './ItemImages'
import { StoreLinks } from './ItemStoreLinks'
import { ListChoice } from './ListChoice'
import { Recipients, useList, useSeenUsers } from './Lists'
import { replaceTo } from './navigation'
import { notice } from './Picker'
import { pagePrice } from './price'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { useCollection } from './useCollection'
import { button, link, primary } from './ui'

const itemPath = (id: string) => `/items/${id}`

const toggle = (on: boolean) =>
  `flex min-h-15 w-full items-center justify-between gap-3 rounded-md px-3.5 py-3 text-left text-lg ${on ? 'bg-ink text-bg' : 'bg-tile text-ink'}`

/** An item's page: the item, the list it is on, and the controls of a user who can edit the list. */
export function ItemPage({ itemId, me }: { itemId: string; me: User }) {
  return <Resource<Item> path={`/api/v2/items/${itemId}`}>{(item) => <ItemScreen key={item.id} loaded={item} me={me} />}</Resource>
}

/**
 * The edit form of an item. When the user can edit more than one list it has a
 * List choice, which starts on the item's own list. Choosing another list moves
 * the item to the end of it (`list_id`). The screen warns about nothing before a
 * move, because a warning would show a recipient whether the item is claimed
 * (docs/spec.md, Items, Screens). A refusal, such as a list the user can't
 * edit, shows as the form's error.
 */
function EditItem({ item, me, onCancel, onSaved }: { item: Item; me: User; onCancel: () => void; onSaved: (item: Item) => void }) {
  const [target, setTarget] = useState(item.list_id)
  return (
    <ItemForm
      legend={`Edit ${item.title}`}
      submit="Save"
      initial={item}
      onCancel={onCancel}
      onSave={async (input) => {
        const res = await updateItem(item.id, target === item.list_id ? input : { ...input, list_id: target })
        if (!res.ok) return res.detail
        onSaved(res.value)
        return null
      }}
    >
      <ListChoice me={me} value={target} onChange={setTarget} />
    </ItemForm>
  )
}

/**
 * A giver's part of an item's page: how many are still wanted, each of the
 * giver's own open gifts on the item ("You claimed 1 for Christmas") with a way
 * to drop it, and a way to claim more. A recipient's item has no claim state, so
 * this shows them nothing and asks for nothing (docs/spec.md, Items, Screens).
 */
function GiverPart({ item }: { item: Item }) {
  const isGiver = item.still_wanted !== undefined
  const { loaded } = useCollection<Gift>(isGiver ? `/api/v2/gifts?item_id=${item.id}&giver_id=me&state=open` : null)
  if (!isGiver) return null
  const wanted = stillWantedText(item)
  const gifts = loaded.status === 'ready' ? loaded.rows : []
  return <GiverGifts item={item} wanted={wanted} gifts={gifts} failed={loaded.status === 'failed'} />
}

function GiverGifts({ item, wanted, gifts, failed }: { item: Item; wanted: string | null; gifts: Gift[]; failed: boolean }) {
  const owners = useBirthdayOwners(gifts)
  return (
    <section aria-labelledby="giving-heading" className="flex max-w-md flex-col gap-3">
      <h2 id="giving-heading" className="text-[1.375rem] font-extrabold">
        Giving
      </h2>
      {wanted && <p className="text-lg">{wanted}</p>}
      {failed && <p role="alert">Could not load your gifts. Reload and try again.</p>}
      {gifts.map((g) => (
        <div key={g.id} className="flex flex-wrap items-center justify-between gap-2">
          <p>{claimedText(g, owners)}</p>
          {/* Dropping a gift goes through its own screen, so a tap never does it at once. */}
          <a href={`/gifts/${g.id}/unclaim?from=item`} className={`${button} flex items-center justify-center`} aria-label={`I won't get this: ${g.occasion_name ?? 'hidden occasion'}`}>
            {"I won't get this"}
          </a>
        </div>
      ))}
      {item.can_claim && (
        <a href={`${itemPath(item.id)}/claim`} className={`${primary} flex items-center justify-center`}>
          {"I'll get this"}
        </a>
      )}
    </section>
  )
}

function ItemScreen({ loaded, me }: { loaded: Item; me: User }) {
  const [item, setItem] = useState(loaded)
  const [editing, setEditing] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState<string | null>(null)
  const list = useList(item.list_id)
  const seen = useSeenUsers()

  /** Saves a change from the counter and the toggle. One runs at a time. */
  async function save(changes: ItemChanges) {
    if (busy) return
    setBusy(true)
    setProblem(null)
    try {
      const res = await updateItem(item.id, changes)
      if (res.ok) setItem(res.value)
      else setProblem(res.detail)
    } catch {
      setProblem('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  // "Added by" says nothing new when the one who added it is the list's only recipient.
  const adder = item.added_by
  const showAdder = adder !== null && list !== null && !(list.recipients.length === 1 && list.recipients[0]!.id === adder.id)

  if (editing) {
    return (
      <Screen title={item.title}>
        <EditItem
          item={item}
          me={me}
          onCancel={() => setEditing(false)}
          onSaved={(saved) => {
            setItem(saved)
            setEditing(false)
          }}
        />
      </Screen>
    )
  }

  const price = pagePrice(item)
  const noFewer = item.is_unlimited || item.quantity <= 1
  return (
    <Screen title={item.title}>
      {list && (
        <div className="flex max-w-xl flex-col gap-1">
          <p>
            On{' '}
            <a href={`/lists/${list.id}`} className="font-semibold underline">
              {list.name}
            </a>
          </p>
          <Recipients list={list} seen={seen} />
        </div>
      )}
      <ItemImages item={item} />
      {price && <p className="font-mono">{price}</p>}
      {item.notes && <p className="max-w-xl whitespace-pre-wrap text-mute">{item.notes}</p>}
      <StoreLinks item={item} onChange={setItem} />
      <GiverPart item={item} />
      {adder && showAdder && (
        <p>
          Added by{' '}
          {seen?.has(adder.id) ? (
            <a href={`/users/${adder.id}`} className="font-semibold underline">
              {adder.name}
            </a>
          ) : (
            adder.name
          )}
        </p>
      )}
      {problem && (
        <p role="alert" className={notice}>
          {problem}
        </p>
      )}

      <section aria-labelledby="how-many" className="flex max-w-md flex-col gap-2">
        <h2 id="how-many" className="text-[1.375rem] font-extrabold">
          How many
        </h2>
        {item.can_edit ? (
          <>
            {/* aria-disabled, not disabled, on these: a pressed button that turns off would drop focus. */}
            <div className="grid grid-cols-[4rem_minmax(0,1fr)_4rem] items-center gap-2">
              <button
                type="button"
                className={`${button} text-[1.625rem]`}
                aria-label="One fewer"
                aria-disabled={busy || noFewer}
                onClick={() => !noFewer && void save({ quantity: item.quantity - 1 })}
              >
                -
              </button>
              <span className="text-center font-mono text-[1.625rem] font-semibold">{item.quantity}</span>
              <button
                type="button"
                className={`${button} text-[1.625rem]`}
                aria-label="One more"
                aria-disabled={busy || item.is_unlimited}
                onClick={() => !item.is_unlimited && void save({ quantity: item.quantity + 1 })}
              >
                +
              </button>
            </div>
            <button
              type="button"
              aria-pressed={item.is_unlimited}
              aria-disabled={busy}
              className={toggle(item.is_unlimited)}
              onClick={() => void save({ is_unlimited: !item.is_unlimited })}
            >
              <span className="flex flex-col">
                <strong className="font-extrabold">Unlimited</strong>
                <span>Stays on the list however many come</span>
              </span>
              <span className="font-semibold">{item.is_unlimited ? 'On' : 'Off'}</span>
            </button>
          </>
        ) : (
          <p className="text-lg">{item.is_unlimited ? 'Unlimited' : item.quantity}</p>
        )}
      </section>

      {item.can_edit && (
        <div className="flex max-w-md flex-col gap-2">
          <button type="button" className={primary} onClick={() => setEditing(true)}>
            Edit
          </button>
        </div>
      )}
      {item.can_delete && (
        <a href={`${itemPath(item.id)}/delete`} className={`${link} max-w-md justify-center font-semibold`}>
          Delete this item
        </a>
      )}
    </Screen>
  )
}

/**
 * The screen before deleting an item. The words are the same for every item,
 * claimed or not, so the screen shows a recipient nothing about claims
 * (docs/spec.md, Items, Screens).
 */
export function DeleteItem({ itemId }: { itemId: string }) {
  return (
    <Resource<Item> path={`/api/v2/items/${itemId}`}>
      {(item) =>
        item.can_delete ? (
          <Confirm
            heading="Delete this item?"
            statement={`${item.title} comes off the list.`}
            details={["People won't see it any more.", 'You can add it again later.']}
            action="Delete this item"
            keepHref={itemPath(item.id)}
            onConfirm={async () => {
              await deleteItem(item.id)
              replaceTo(`/lists/${item.list_id}`)
            }}
          />
        ) : (
          <NotFound />
        )
      }
    </Resource>
  )
}
