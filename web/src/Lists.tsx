import { useId, useState, type FormEvent } from 'react'
import { updateItem, updateList, type Collection, type Invitation, type Item, type List, type Recipient, type User, type UserSummary } from './api'
import { AddForm } from './AddForm'
import { UserAvatar } from './Avatar'
import { DeactivatedLabel } from './DeactivatedLabel'
import { isForMe, MyInvitations, OtherInvitations, PendingForMe } from './Invitations'
import { Markdown } from './Markdown'
import { ownLists, primaryFirst } from './listOrder'
import { stillWantedText } from './gifts'
import { goBack } from './navigation'
import { notice } from './Picker'
import { rowPrice } from './price'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { button, buttonLink, field, link, primary } from './ui'
import { useCollection } from './useCollection'

const listPath = (id: string) => `/lists/${id}`

/** "1 item", "3 items", "No items". */
export function itemCount(n: number): string {
  if (n === 0) return 'No items'
  return n === 1 ? '1 item' : `${n} items`
}

/**
 * The users the signed-in user can see, by ID, from `GET /users`. A recipient's
 * name opens their page only when the user can see them: a summary doesn't make
 * anyone visible (docs/spec.md, Lists, Screens). The user is the one that
 * `GET /users` gives, so `deactivated_at` is there for the viewers who get it.
 * `null` until the answer is in, or if it fails: names then show without links.
 */
export function useSeenUsers(): Map<string, User> | null {
  const { loaded } = useCollection<User>('/api/v2/users')
  return loaded.status === 'ready' ? new Map(loaded.rows.map((u) => [u.id, u])) : null
}

/**
 * A user's avatar and name, with the name a link to their page when `seen` has
 * them, and "Deactivated" beside it for a viewer who gets `deactivated_at`.
 */
export function UserChip({ user, seen }: { user: UserSummary; seen: Map<string, User> | null }) {
  const known = seen?.get(user.id)
  return (
    <span className="flex flex-wrap items-center gap-x-2">
      <UserAvatar user={user} size={32} />
      <span className="flex flex-wrap items-center gap-x-2">
        {known ? (
          <a href={`/users/${user.id}`} className="inline-flex min-h-11 items-center font-semibold underline">
            {user.name}
          </a>
        ) : (
          <span className="font-semibold">{user.name}</span>
        )}
        {known && <DeactivatedLabel user={known} />}
      </span>
    </span>
  )
}

/** A list's recipients, as avatars with names. */
export function Recipients({ list, seen }: { list: List; seen: Map<string, User> | null }) {
  return (
    <p className="flex flex-wrap items-center gap-x-4 gap-y-1">
      {list.recipients.map((r) => (
        <UserChip key={r.id} user={r} seen={seen} />
      ))}
    </p>
  )
}

/** Lists as rows: the recipients' avatars, the name, and how many items, each opening the list's page. */
export function ListRows({ label, lists }: { label: string; lists: List[] }) {
  return (
    <ul aria-label={label} className="flex max-w-xl flex-col">
      {lists.map((l) => (
        <li key={l.id} className="border-t border-hair last:border-b">
          <a href={listPath(l.id)} className="flex min-h-19 items-center gap-3 py-3">
            <span className="flex shrink-0 -space-x-2">
              {l.recipients.map((r) => (
                <UserAvatar key={r.id} user={r} size={40} />
              ))}
            </span>
            <span className="flex min-w-0 flex-col">
              <strong className="text-xl/tight font-extrabold">{l.name}</strong>
              <span className="text-lg text-mute">{itemCount(l.item_count)}</span>
            </span>
          </a>
        </li>
      ))}
    </ul>
  )
}

/**
 * My lists: the pending invitations for the user and their children, with
 * Accept and Decline; the lists the user can edit (`GET /lists?can_edit=true`)
 * that they are a recipient of, their own primary list first; then a part for
 * each of their children. Each list shows its recipients' avatars, its name, and
 * how many items it has, and opens the list's page. New list is the last button.
 * An answer to an invitation loads the screen again, so the accepted list shows
 * in the user's lists, or in the child's part (docs/spec.md, Lists, Screens).
 */
export function MyLists({ me }: { me: User }) {
  const [round, setRound] = useState(0)
  return (
    <Resource<Collection<List>> key={round} path="/api/v2/lists?can_edit=true" all>
      {(lists) => (
        <Screen title="My lists">
          <MyInvitations me={me} onChanged={() => setRound((n) => n + 1)} />
          <ListRows label="Your lists" lists={ownLists(lists.data, me)} />
          {me.children.length > 0 && <ChildParts me={me} />}
          <a href="/lists/new" className={`${primary} flex max-w-md items-center justify-center`}>
            New list
          </a>
        </Screen>
      )}
    </Resource>
  )
}

/**
 * One part for each child, under the child's name. "Deactivated" shows beside
 * the name of a deactivated child, which the parent learns from `GET /users`.
 */
function ChildParts({ me }: { me: User }) {
  const { loaded } = useCollection<User>('/api/v2/users')
  const users = loaded.status === 'ready' ? loaded.rows : []
  return (
    <>
      {me.children.map((child) => (
        <ChildPart key={child.id} me={me} child={child} user={users.find((u) => u.id === child.id)} />
      ))}
    </>
  )
}

/**
 * A child's lists: their primary list first, then the rest by name
 * (`GET /lists?recipient_id=`, because a list the user can edit only through a
 * deactivated child is not in `?can_edit=true`). A list that has the user as a
 * recipient too shows only under the user's own lists.
 */
function ChildPart({ me, child, user }: { me: User; child: UserSummary; user?: User }) {
  const { loaded } = useCollection<List>(`/api/v2/lists?recipient_id=${child.id}`)
  const heading = useId()
  return (
    <section aria-labelledby={heading} className="flex max-w-xl flex-col gap-2">
      <h2 id={heading} className="text-[1.375rem] font-extrabold">
        {child.name}
        {user && <DeactivatedLabel user={user} />}
      </h2>
      {loaded.status === 'failed' && <p role="alert">Could not load {child.name}'s lists. Reload and try again.</p>}
      {loaded.status === 'ready' && (
        <ListRows label={`${child.name}'s lists`} lists={primaryFirst(loaded.rows.filter((l) => !l.recipients.some((r) => r.id === me.id)))} />
      )}
    </section>
  )
}

/**
 * A price, the title and notes under it: one item of a list. A giver reads how
 * many are still wanted and what they claimed themselves, in place of the
 * quantity. A recipient sees neither: their item has no claim state.
 */
function ItemRow({ item }: { item: Item }) {
  const wanted = stillWantedText(item)
  const note = wanted ?? (item.is_unlimited ? 'Unlimited' : item.quantity > 1 ? `Quantity ${item.quantity}` : null)
  const claimed = (item.my_claimed_quantity ?? 0) > 0 ? `You claimed ${item.my_claimed_quantity}` : null
  const price = rowPrice(item)
  return (
    <>
      <span className="flex flex-col items-start gap-0.5">
        <strong className="leading-tight font-semibold">{item.title}</strong>
        {note && <span className="text-mute">{note}</span>}
        {claimed && <span className="font-semibold">{claimed}</span>}
      </span>
      <span className="font-mono">{price}</span>
    </>
  )
}

const grid = 'grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-2.5'

/** Move up and Move down share a line, even on the narrowest phone. */
const moveButton = `${button} flex-1 px-2! whitespace-nowrap`

/**
 * A list's page: a pending invitation for the user or a child, the name,
 * recipients, description, and items in order, then the other pending
 * invitations with Invite, and Leave, Make {child} leave, and Delete list. An
 * answer to an invitation loads the page again, so it shows the list as its
 * recipients see it.
 */
export function ListPage({ listId, me }: { listId: string; me: User }) {
  const [round, setRound] = useState(0)
  return (
    <Resource<List> key={round} path={`/api/v2/lists/${listId}`}>
      {(list) => <ListScreen key={list.id} list={list} me={me} onChanged={() => setRound((n) => n + 1)} />}
    </Resource>
  )
}

/** The pending invitations to the list that the caller sees, and the ones that are for the user or a child. */
function useListInvitations(list: List, me: User) {
  const { loaded, reload } = useCollection<Invitation>(`/api/v2/invitations?list_id=${list.id}`)
  const rows = loaded.status === 'ready' ? loaded.rows : []
  return { failed: loaded.status === 'failed', mine: rows.filter((i) => isForMe(i, me)), others: rows.filter((i) => !isForMe(i, me)), reload }
}

/**
 * Make {child} leave: one link for each child of the user who is a recipient and
 * whose row in `GET /lists/{id}/recipients` has `can_delete` set. The list is
 * asked for only when a child is a recipient.
 */
function useLeavableChildren(list: List, me: User): UserSummary[] {
  const children = me.children.filter((c) => list.recipients.some((r) => r.id === c.id))
  const { loaded } = useCollection<Recipient>(children.length > 0 ? `/api/v2/lists/${list.id}/recipients` : null)
  if (loaded.status !== 'ready') return []
  return children.filter((c) => loaded.rows.some((r) => r.user.id === c.id && r.can_delete))
}

function ListScreen({ list, me, onChanged }: { list: List; me: User; onChanged: () => void }) {
  const seen = useSeenUsers()
  const invitations = useListInvitations(list, me)
  const leavable = useLeavableChildren(list, me)
  const isRecipient = list.recipients.some((r) => r.id === me.id)
  // A parent of a recipient, who isn't one, sees the claim state that their child doesn't. This follows
  // what the parent sees, not what they can do: while the child is deactivated their powers stop
  // (`can_edit` is false), and they still see the claims.
  const seesClaims = !isRecipient && list.recipients.some((r) => me.children.some((c) => c.id === r.id))
  return (
    <Screen title={list.name}>
      <PendingForMe invitations={invitations.mine} me={me} onChanged={onChanged} />
      <Recipients list={list} seen={seen} />
      {list.description && (
        <div className="flex max-w-xl flex-col gap-2">
          <Markdown text={list.description} />
        </div>
      )}
      {seesClaims && <p className="max-w-xl">{"You see what's been claimed here. Fully claimed items don't show."}</p>}
      {list.can_edit && (
        <div className="flex max-w-md flex-col gap-2">
          <a href={`${listPath(list.id)}/edit`} className={buttonLink}>
            Edit list
          </a>
          <a href={`${listPath(list.id)}/add`} className={`${primary} flex items-center justify-center`}>
            Add
          </a>
        </div>
      )}
      <ListItems list={list} />
      {invitations.failed && isRecipient && <p role="alert">Could not load the invitations. Reload and try again.</p>}
      {isRecipient && <OtherInvitations invitations={invitations.others} onChanged={invitations.reload} />}
      {list.can_invite && (
        <a href={`${listPath(list.id)}/invite`} className={`${buttonLink} max-w-md`}>
          Invite
        </a>
      )}
      {list.can_leave && (
        <a href={`${listPath(list.id)}/recipients/${me.id}/leave`} className={`${link} max-w-md justify-center font-semibold`}>
          Leave
        </a>
      )}
      {leavable.map((child) => (
        <a key={child.id} href={`${listPath(list.id)}/recipients/${child.id}/leave`} className={`${link} max-w-md justify-center font-semibold`}>
          Make {child.name} leave
        </a>
      ))}
      {list.can_delete && (
        <a href={`${listPath(list.id)}/delete`} className={`${link} max-w-md justify-center font-semibold`}>
          Delete list
        </a>
      )}
    </Screen>
  )
}

/**
 * The items of a list, in the order its editors set, with Change order when
 * `can_edit` is true. A list's page and a user's page (for the primary list)
 * both show it, and its controls come from the list's `can_*` fields, so the
 * rules are the same on both (docs/spec.md, Lists, Screens).
 */
export function ListItems({ list }: { list: List }) {
  const { loaded, reload } = useCollection<Item>(`/api/v2/lists/${list.id}/items`)
  const [ordering, setOrdering] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  // A move is in flight from the tap until the reloaded items are in: the
  // buttons' indexes come from the rows on screen, so a second tap before then
  // would send the same target twice.
  // `movedFrom` is the items on screen when the user tapped. It is over when
  // other items replace them.
  const [movedFrom, setMovedFrom] = useState<typeof loaded | null>(null)
  const moving = movedFrom === loaded

  async function move(item: Item, position: number) {
    if (moving) return
    setMovedFrom(loaded)
    setFailed(null)
    try {
      const res = await updateItem(item.id, { position })
      if (res.ok) {
        reload()
        return
      }
      setFailed(res.detail)
    } catch {
      setFailed('Could not move that item. Try again.')
    }
    setMovedFrom(null)
  }

  const items = loaded.status === 'ready' ? loaded.rows : []
  return (
    <>
      {loaded.status === 'failed' && <p role="alert">Could not load the items. Reload and try again.</p>}
      {failed && (
        <p role="alert" className={notice}>
          {failed}
        </p>
      )}
      {loaded.status === 'ready' && items.length === 0 && (
        <section className="flex max-w-xl flex-col gap-2">
          <h2 className="text-[1.625rem]/[1.2] font-extrabold">This list is empty</h2>
          {list.can_edit && <p className="text-lg text-mute">Add the first thing it should have.</p>}
        </section>
      )}
      {items.length > 0 && (
        <section className="flex max-w-xl flex-col gap-2">
          <div className="flex items-center justify-between gap-3">
            <h2 className="text-[1.375rem] font-extrabold">{itemCount(items.length)}</h2>
            {list.can_edit && items.length > 1 && (
              <button type="button" className={button} onClick={() => setOrdering(!ordering)}>
                {ordering ? 'Done' : 'Change order'}
              </button>
            )}
          </div>
          <ul aria-label="Items" className="flex flex-col border-b border-hair">
            {items.map((item, i) => (
              <li key={item.id} className="border-t border-hair">
                {ordering ? (
                  <div className={grid}>
                    <ItemRow item={item} />
                    <div className="col-span-full flex gap-2">
                      {/* aria-disabled, not disabled: a pressed button that turns off would drop focus. */}
                      <button
                        type="button"
                        className={moveButton}
                        aria-label={`Move up: ${item.title}`}
                        aria-disabled={moving || i === 0}
                        onClick={() => i > 0 && void move(item, i - 1)}
                      >
                        Move up
                      </button>
                      <button
                        type="button"
                        className={moveButton}
                        aria-label={`Move down: ${item.title}`}
                        aria-disabled={moving || i === items.length - 1}
                        onClick={() => i < items.length - 1 && void move(item, i + 1)}
                      >
                        Move down
                      </button>
                    </div>
                  </div>
                ) : (
                  <a href={`/items/${item.id}`} className={`${grid} no-underline`}>
                    <ItemRow item={item} />
                  </a>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  )
}

/** Edit list: the name and the description. Only a user who can edit the list sees it. */
export function EditList({ listId }: { listId: string }) {
  return (
    <Resource<List> path={`/api/v2/lists/${listId}`}>
      {(list) => (list.can_edit ? <EditListForm key={list.id} list={list} /> : <NotFound />)}
    </Resource>
  )
}

function EditListForm({ list }: { list: List }) {
  const [name, setName] = useState(list.name)
  const [description, setDescription] = useState(list.description ?? '')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await updateList(list.id, { name: name.trim(), description: description.trim() === '' ? null : description.trim() })
      if (res.ok) {
        goBack(listPath(list.id))
        return
      }
      setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title="Edit list">
      <form onSubmit={save} noValidate className="flex max-w-xl flex-col gap-4">
        <label className="flex flex-col gap-1 font-semibold">
          Name
          <input className={`${field} font-normal`} value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1 font-semibold">
          Description
          <textarea
            className={`${field} font-normal`}
            rows={4}
            placeholder="Anything that helps the people who give to this list"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
        {error && (
          <p role="alert" className={notice}>
            {error}
          </p>
        )}
        <div className="flex flex-col gap-2 sm:flex-row">
          <button type="submit" className={`${primary} sm:flex-1`} disabled={busy}>
            Save
          </button>
          <a href={listPath(list.id)} className={`${button} flex items-center justify-center`}>
            Cancel
          </a>
        </div>
      </form>
    </Screen>
  )
}

/** Add an item to a list. Only a user who can edit the list sees it. */
export function AddToList({ listId, me }: { listId: string; me: User }) {
  return (
    <Resource<List> path={`/api/v2/lists/${listId}`}>
      {(list) => (list.can_edit ? <AddForm key={list.id} list={list} me={me} /> : <NotFound />)}
    </Resource>
  )
}

export { useList, useListState } from './useList'
