import { deletePasskey, type Collection, type Passkey, type User } from './api'
import { Confirm } from './Confirm'
import { replaceTo } from './navigation'
import { Resource } from './Resource'
import { NotFound } from './Screen'

/**
 * The screen before the user's last passkey is deleted (docs/spec.md, Users
 * and parents, Screens). It says what the user loses, in the words of whoever
 * reads it: the user, a user with no parents, the admin, or someone else.
 */
export function DeletePasskey({ userId, passkeyId, me }: { userId: string; passkeyId: string; me: User }) {
  return (
    <Resource<User> path={`/api/v2/users/${userId}`}>
      {(user) => (
        <Resource<Collection<Passkey>> path={`/api/v2/users/${userId}/passkeys`} all>
          {(passkeys) => {
            const passkey = passkeys.data.find((p) => p.id === passkeyId)
            if (!passkey || !passkey.can_delete) return <NotFound />
            return <Ask user={user} me={me} passkey={passkey} isLast={passkeys.data.length === 1} />
          }}
        </Resource>
      )}
    </Resource>
  )
}

function Ask({ user, me, passkey, isLast }: { user: User; me: User; passkey: Passkey; isLast: boolean }) {
  const isMe = user.id === me.id
  const back = `/users/${user.id}/sign-in`

  let heading = `Delete ${passkey.name}?`
  let statement = `${passkey.name} will no longer sign ${isMe ? 'you' : user.name} in.`
  const details: string[] = []
  if (isLast) {
    heading = isMe ? 'Delete your last passkey?' : `Delete ${user.name}'s last passkey?`
    if (isMe) {
      statement = "You'll need a new link or QR code to get back in."
      // Who can make this user a new link: the admin, or for the admin, only the
      // person who runs Wishbone. A user with a parent has one more way.
      if (me.is_admin) details.push('Only the person who runs Wishbone can make you a new link.')
      else if ((me.parents ?? []).length === 0) details.push('Only the admin can make you a new link.')
    } else {
      statement = `${user.name} will need a new link or QR code to sign in on a new device.`
    }
  }

  return (
    <Confirm
      heading={heading}
      statement={statement}
      details={details}
      action="Delete this passkey"
      keepHref={back}
      onConfirm={async () => {
        await deletePasskey(user.id, passkey.id)
        replaceTo(back)
      }}
    />
  )
}
