import { useState, type FormEvent } from 'react'
import { createList, isDeactivated, type NewList as NewListInput, type User } from './api'
import { replaceTo } from './navigation'
import { notice } from './Picker'
import { Screen } from './Screen'
import { button, field, primary } from './ui'
import { useCollection } from './useCollection'

const radio = 'flex min-h-14 items-center gap-3 rounded-md bg-tile px-4 text-lg font-semibold'

/**
 * New list: a name, an optional description, and, when the user has children,
 * who the list is for: the user (the default) or one of their children. A
 * parent who makes a list for a child is not its recipient. A deactivated child
 * is left out, because nothing can be made for them until the admin reactivates
 * them. The parent learns that from `GET /users`. The new list's page opens in
 * place of this form, so Back goes to My lists (docs/spec.md, Lists,
 * Screens).
 */
export function NewList({ me }: { me: User }) {
  const { loaded } = useCollection<User>(me.children.length > 0 ? '/api/v2/users' : null)
  const deactivated = new Set(loaded.status === 'ready' ? loaded.rows.filter(isDeactivated).map((u) => u.id) : [])
  const children = me.children.filter((c) => !deactivated.has(c.id))

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [forId, setForId] = useState(me.id)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function save(e: FormEvent) {
    e.preventDefault()
    if (name.trim() === '') return setError('Enter a name.')
    const input: NewListInput = { name: name.trim() }
    if (description.trim() !== '') input.description = description.trim()
    // A child that turned out to be deactivated after the choice was made is no choice.
    if (forId !== me.id && children.some((c) => c.id === forId)) input.recipient_id = forId
    setBusy(true)
    setError(null)
    try {
      const res = await createList(input)
      if (res.ok) {
        replaceTo(`/lists/${res.value.id}`)
        return
      }
      setError(res.detail)
    } catch {
      setError('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title="New list">
      <form onSubmit={save} noValidate className="flex max-w-xl flex-col gap-4">
        <label className="flex flex-col gap-1 font-semibold">
          Name
          <input className={`${field} font-normal`} value={name} onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1 font-semibold">
          Description (optional)
          <textarea
            className={`${field} font-normal`}
            rows={4}
            placeholder="Anything that helps the people who give to this list"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
        {children.length > 0 && (
          <fieldset className="flex flex-col gap-2">
            <legend className="pb-1 font-semibold">Who is it for?</legend>
            {[{ id: me.id, name: 'Me' }, ...children].map((who) => (
              <label key={who.id} className={radio}>
                <input type="radio" name="for" className="size-6" checked={forId === who.id} onChange={() => setForId(who.id)} />
                {who.name}
              </label>
            ))}
          </fieldset>
        )}
        {error && (
          <p role="alert" className={notice}>
            {error}
          </p>
        )}
        <div className="flex flex-col gap-2 sm:flex-row">
          <button type="submit" className={`${primary} sm:flex-1`} disabled={busy}>
            Save
          </button>
          <a href="/lists" className={`${button} flex items-center justify-center`}>
            Cancel
          </a>
        </div>
      </form>
    </Screen>
  )
}
