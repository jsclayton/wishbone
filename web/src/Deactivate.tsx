import { deactivateUser, type User } from './api'
import { Confirm } from './Confirm'
import { replaceTo } from './navigation'
import { Resource } from './Resource'
import { NotFound } from './Screen'

/**
 * The screen before the admin deactivates a user (docs/spec.md, Deactivation
 * and deletion, Screens). Reactivation has no such screen: it brings the user
 * back as they were. Only the admin gets `can_deactivate`, so anyone else sees
 * the missing-page message.
 */
export function Deactivate({ userId }: { userId: string }) {
  return <Resource<User> path={`/api/v2/users/${userId}`}>{(user) => (user.can_deactivate ? <Ask user={user} /> : <NotFound />)}</Resource>
}

function Ask({ user }: { user: User }) {
  const back = `/users/${user.id}`
  return (
    <Confirm
      heading={`Deactivate ${user.name}?`}
      statement={`${user.name} will be signed out everywhere, and can't sign in until you reactivate them.`}
      details={[`Only you and ${user.name}'s parents will see ${user.name}. You can reactivate ${user.name} later.`]}
      action={`Deactivate ${user.name}`}
      keepHref={back}
      keepLabel={`Keep ${user.name} active`}
      onConfirm={async () => {
        await deactivateUser(user.id)
        replaceTo(back)
      }}
    />
  )
}
