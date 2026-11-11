import { useState, type FormEvent } from 'react'
import { createUser, fullName, type Collection, type User } from './api'
import { UserAvatar } from './Avatar'
import { BirthdayFields, emptyBirthday, toPartialDate } from './BirthdayFields'
import { DeactivatedLabel } from './DeactivatedLabel'
import { NameFields } from './NameFields'
import { replaceTo } from './navigation'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { buttonLink, primary } from './ui'
import { useCollection } from './useCollection'

/**
 * The admin's Users screen: every user the caller can see, by name, from
 * `GET /users` (docs/spec.md, Admin and setup, Screens). For anyone else the
 * screen shows the missing-page message, and asks the server for nothing.
 */
export function Users({ me }: { me: User }) {
  if (!me.is_admin) return <NotFound />
  return (
    <Resource<Collection<User>> path="/api/v2/users" all>
      {(users) => (
        <Screen
          title="Users"
          action={
            <a href="/users/new" className={buttonLink}>
              New user
            </a>
          }
        >
          <ul className="flex max-w-xl flex-col border-b border-hair">
            {users.data.map((user) => (
              <li key={user.id} className="border-t border-hair">
                <a href={`/users/${user.id}`} className="flex min-h-15.5 items-center gap-3 text-xl font-semibold">
                  <UserAvatar user={user} size={40} />
                  <span>{fullName(user)}</span>
                  <DeactivatedLabel user={user} />
                </a>
              </li>
            ))}
          </ul>
        </Screen>
      )}
    </Resource>
  )
}

/**
 * The New user form: a name, a birthday, an optional last name, and optional
 * parents, picked from every user. Saving opens the new user's page. Only the
 * admin can make a user, so anyone else sees the missing-page message, and the
 * form asks for no users.
 */
export function NewUser({ me }: { me: User }) {
  const [first, setFirst] = useState('')
  const [last, setLast] = useState('')
  const [birthday, setBirthday] = useState(emptyBirthday)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [parentIds, setParentIds] = useState<string[]>([])
  const everyone = useCollection<User>(me.is_admin ? '/api/v2/users' : null)

  if (!me.is_admin) return <NotFound />

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    const date = toPartialDate(birthday)
    if (!first.trim() || !date) {
      setError('Enter a first name and a birthday month and day.')
      return
    }
    setBusy(true)
    try {
      const res = await createUser({
        name: first.trim(),
        ...(last.trim() ? { last_name: last.trim() } : {}),
        birthday: date,
        ...(parentIds.length > 0 ? { parent_ids: parentIds } : {}),
      })
      if (res.ok) {
        // The form is not a screen to go back to: Back from the new user's page leads to Users.
        replaceTo(`/users/${res.value.id}`)
        return
      }
      setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title="New user">
      <form onSubmit={submit} className="flex max-w-xl flex-col gap-4" noValidate>
        <NameFields first={first} last={last} onFirst={setFirst} onLast={setLast} />
        <BirthdayFields value={birthday} onChange={setBirthday} />
        {everyone.loaded.status === 'failed' && <p>Could not load the users to pick parents from. You can add parents on the new user's page.</p>}
        {everyone.loaded.status === 'ready' && everyone.loaded.rows.length > 0 && (
          <fieldset className="flex flex-col">
            <legend className="mb-1 font-semibold">Parents (optional)</legend>
            {everyone.loaded.rows.map((u) => (
              <label key={u.id} className="flex min-h-12 items-center gap-3 text-lg">
                <input
                  type="checkbox"
                  className="size-6 shrink-0"
                  checked={parentIds.includes(u.id)}
                  onChange={(e) => setParentIds((ids) => (e.target.checked ? [...ids, u.id] : ids.filter((id) => id !== u.id)))}
                />
                {fullName(u)}
              </label>
            ))}
          </fieldset>
        )}
        {error && <p role="alert">{error}</p>}
        <button type="submit" className={primary} disabled={busy}>
          Save
        </button>
      </form>
    </Screen>
  )
}
