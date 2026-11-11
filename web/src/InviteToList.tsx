import { inviteUser, type List, type User } from './api'
import { Picker } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { useCollection } from './useCollection'

/**
 * Invite: the users the signed-in user can invite to the list
 * (`GET /users?invitable_to=`), by name. A tap invites the user with no
 * confirmation, because a recipient can withdraw the invitation, and goes back to
 * the list (docs/spec.md, Lists, Screens). Only a recipient of an extra list
 * sees it: the list's `can_invite`.
 */
export function InviteToList({ listId }: { listId: string }) {
  return <Resource<List> path={`/api/v2/lists/${listId}`}>{(list) => (list.can_invite ? <InvitePicker key={list.id} list={list} /> : <NotFound />)}</Resource>
}

function InvitePicker({ list }: { list: List }) {
  const { loaded } = useCollection<User>(`/api/v2/users?invitable_to=${list.id}`)
  const title = `Invite someone to ${list.name}`
  if (loaded.status === 'failed') {
    return (
      <Screen title={title}>
        <p role="alert">Could not load the users. Reload and try again.</p>
      </Screen>
    )
  }
  if (loaded.status !== 'ready') return null
  return (
    <Picker
      title={title}
      verb="Invite"
      empty="There is nobody you can invite to this list."
      choices={loaded.rows.map((u) => ({ id: u.id, name: u.name, user: u }))}
      backTo={`/lists/${list.id}`}
      onAdd={async (c) => {
        const res = await inviteUser(list.id, c.id)
        return res.ok ? null : res.detail
      }}
    />
  )
}
