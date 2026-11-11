import { useId, useState } from 'react'
import { fullName, setParents, type User, type UserSummary } from './api'
import { notice, Picker } from './Picker'
import { Resource } from './Resource'
import { NotFound, Screen } from './Screen'
import { button, buttonLink } from './ui'
import { useCollection } from './useCollection'

/**
 * The admin's Parents part, inside the For part of a user's page (docs/spec.md,
 * Admin and setup, Screens): the user's parents, each with Remove, and Add
 * parent. It shows from the user's `can_edit_parents`. Neither one opens a
 * confirmation screen, because each is easy to undo. A refusal shows the error's
 * `detail`.
 */
export function ParentsPart({ user }: { user: User }) {
  const heading = useId()
  const [parents, setParentList] = useState<UserSummary[]>(user.parents)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<string | null>(null)

  async function remove(parent: UserSummary) {
    setBusy(true)
    setRefused(null)
    try {
      const res = await setParents(
        user.id,
        parents.filter((p) => p.id !== parent.id).map((p) => p.id),
      )
      if (res.ok) setParentList(res.value.parents)
      else setRefused(res.detail)
    } catch {
      setRefused('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <section aria-labelledby={heading} className="mt-4 flex flex-col gap-2">
      <h3 id={heading} className="text-[1.375rem]/normal font-extrabold">
        Parents
      </h3>
      {refused && (
        <p role="alert" className={notice}>
          {refused}
        </p>
      )}
      {parents.length === 0 ? (
        <p className="text-lg">No parents.</p>
      ) : (
        <ul>
          {parents.map((p) => (
            <li key={p.id} className="flex min-h-16 items-center justify-between gap-3 border-t border-hair py-2 last:border-b">
              <span className="text-xl/tight font-semibold wrap-break-word">{p.name}</span>
              <button type="button" className={`${button} shrink-0`} disabled={busy} aria-label={`Remove ${p.name}`} onClick={() => remove(p)}>
                Remove
              </button>
            </li>
          ))}
        </ul>
      )}
      <a href={`/users/${user.id}/parents`} className={buttonLink}>
        Add parent
      </a>
    </section>
  )
}

/**
 * Add parent: every other user who is not already a parent, by name. The list
 * does not leave out a user who would make a loop: the server refuses that, and
 * the screen shows its words. Only the admin changes parents, so anyone else sees
 * the missing-page message.
 */
export function AddParent({ userId }: { userId: string }) {
  return <Resource<User> path={`/api/v2/users/${userId}`}>{(user) => (user.can_edit_parents ? <ParentPicker user={user} /> : <NotFound />)}</Resource>
}

function ParentPicker({ user }: { user: User }) {
  const everyone = useCollection<User>('/api/v2/users')
  const title = `Add a parent for ${user.name}`
  if (everyone.loaded.status === 'failed') {
    return (
      <Screen title={title}>
        <p role="alert">Could not load the users. Reload and try again.</p>
      </Screen>
    )
  }
  if (everyone.loaded.status !== 'ready') return null
  const there = new Set(user.parents.map((p) => p.id))
  const choices = everyone.loaded.rows.filter((u) => u.id !== user.id && !there.has(u.id)).map((u) => ({ id: u.id, name: fullName(u), user: u }))
  return (
    <Picker
      title={title}
      empty={`Every other user is a parent of ${user.name} already.`}
      choices={choices}
      backTo={`/users/${user.id}`}
      onAdd={async (c) => {
        const res = await setParents(user.id, [...user.parents.map((p) => p.id), c.id])
        return res.ok ? null : res.detail
      }}
    />
  )
}
