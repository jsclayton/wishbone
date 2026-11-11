import { useEffect, useState } from 'react'
import { fullName, isDeactivated, reactivateUser, type List, type User } from './api'
import { UserAvatar } from './Avatar'
import { DeactivatedLabel } from './DeactivatedLabel'
import { ListItems, ListRows, useListState } from './Lists'
import { Markdown } from './Markdown'
import { replaceTo } from './navigation'
import { ParentsPart } from './Parents'
import { Resource } from './Resource'
import { Screen } from './Screen'
import { button, buttonLink, primary } from './ui'
import { useCollection } from './useCollection'

const heading = 'text-2xl font-extrabold'

/**
 * A user's page: their good to know, then the items of their primary list, then
 * Other lists, then the For {name} part (docs/spec.md, Lists, Screens, and
 * Users and parents, Screens). The user's own page opens My lists.
 *
 * The primary list's items show only when the server gives the viewer
 * `primary_list`: the admin, who sees a user only through the admin's access,
 * gets none. Its controls come from the list's `can_*` fields, as on a list's
 * page, so a parent sees the edit controls on a child's page, and a giver
 * doesn't. Other lists are the user's extra lists that the viewer can see.
 *
 * The For part has a button for each `can_*` field that is true, and shows
 * nothing when none is. A parent gets it on each child's page. The admin's
 * Parents part is in it, from `can_edit_parents`, and so are Deactivate {name}
 * (from `can_deactivate`, which opens a confirmation screen) and Reactivate
 * {name} (from `can_reactivate`, which does not). "Deactivated" shows beside the
 * name for a viewer who gets `deactivated_at`. A parent who isn't the admin sees
 * who can reactivate, and no For part, because each `can_*` through a
 * deactivated child is false.
 */
export function UserPage({ userId, me }: { userId: string; me: User }) {
  // The signed-in user's own page is the My lists screen. The path's ID is the
  // real one: the web app doesn't use `me` in paths.
  const own = userId === me.id
  useEffect(() => {
    if (own) replaceTo('/lists')
  }, [own])
  if (own) return null
  return <Resource<User> path={`/api/v2/users/${userId}`}>{(user) => <Page key={user.id} loaded={user} />}</Resource>
}

function Page({ loaded }: { loaded: User }) {
  const [user, setUser] = useState(loaded)
  return (
    <Screen title={fullName(user)} badge={<DeactivatedLabel user={user} className="text-base font-normal" />}>
      <UserAvatar user={user} size={104} />
      {isDeactivated(user) && !user.can_reactivate && (
        <p className="max-w-xl text-lg">
          Only the admin can reactivate {user.name}. Until then, you can't change anything for {user.name}.
        </p>
      )}
      {user.good_to_know && (
        <section aria-label="Good to know" className="flex max-w-xl flex-col rounded-md bg-tile px-4 py-3.5">
          <h2 className="text-base font-extrabold">Good to know</h2>
          <Markdown text={user.good_to_know} />
        </section>
      )}
      {user.primary_list && <PrimaryList user={user} listId={user.primary_list.id} />}
      <OtherLists user={user} />
      <For user={user} onChange={setUser} />
    </Screen>
  )
}

/** The items of the user's primary list, with Add for a viewer who can edit it. */
function PrimaryList({ user, listId }: { user: User; listId: string }) {
  const list = useListState(listId)
  if (list === 'failed') return <p role="alert">Could not load {user.name}'s list. Reload and try again.</p>
  if (!list) return null
  return (
    <>
      {list.can_edit && (
        <a href={`/lists/${list.id}/add`} className={`${primary} flex max-w-md items-center justify-center`}>
          Add
        </a>
      )}
      <ListItems list={list} />
    </>
  )
}

/** The extra lists that the user is a recipient of and the viewer can see. The part is left out when there are none. */
function OtherLists({ user }: { user: User }) {
  const { loaded } = useCollection<List>(`/api/v2/lists?recipient_id=${user.id}&is_primary=false`)
  if (loaded.status === 'failed') return <p role="alert">Could not load {user.name}'s other lists. Reload and try again.</p>
  if (loaded.status !== 'ready' || loaded.rows.length === 0) return null
  return (
    <section aria-labelledby="other-lists" className="flex max-w-xl flex-col gap-2">
      <h2 id="other-lists" className={heading}>
        Other lists
      </h2>
      <ListRows label="Other lists" lists={loaded.rows.toSorted((a, b) => a.name.localeCompare(b.name))} />
    </section>
  )
}

function For({ user, onChange }: { user: User; onChange: (user: User) => void }) {
  const signIn = user.can_make_login_link || user.can_manage_sessions
  if (!user.can_edit && !signIn && !user.can_edit_parents && !user.can_deactivate && !user.can_reactivate) return null
  return (
    <section aria-labelledby="for-user" className="flex max-w-xl flex-col gap-2">
      <h2 id="for-user" className={heading}>
        For {user.name}
      </h2>
      {user.can_edit && (
        <a href={`/users/${user.id}/profile`} className={buttonLink}>
          Edit {user.name}'s profile
        </a>
      )}
      {signIn && (
        <a href={`/users/${user.id}/sign-in`} className={buttonLink}>
          Sign {user.name} in
        </a>
      )}
      {user.can_edit_parents && <ParentsPart user={user} />}
      {user.can_deactivate && (
        <a href={`/users/${user.id}/deactivate`} className={buttonLink}>
          Deactivate {user.name}
        </a>
      )}
      {user.can_reactivate && <Reactivate user={user} onChange={onChange} />}
    </section>
  )
}

/** Reactivation brings the user back as they were, so it asks for no confirmation. */
function Reactivate({ user, onChange }: { user: User; onChange: (user: User) => void }) {
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)

  async function reactivate() {
    setBusy(true)
    setFailed(false)
    try {
      onChange(await reactivateUser(user.id))
    } catch {
      setFailed(true)
      setBusy(false)
    }
  }

  return (
    <>
      <button type="button" className={button} onClick={reactivate} disabled={busy}>
        Reactivate {user.name}
      </button>
      {failed && <p role="alert">Could not reactivate {user.name}. Try again.</p>}
    </>
  )
}
