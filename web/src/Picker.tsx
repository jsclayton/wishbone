import { useState, type ReactNode } from 'react'
import type { User } from './api'
import { UserAvatar } from './Avatar'
import { goBack } from './navigation'
import { Screen } from './Screen'
import { button } from './ui'

export const notice = 'rounded-md bg-alert p-3.5 text-on-alert'
export const row = 'flex min-h-19 items-center justify-between gap-3 border-t border-hair py-2 last:border-b'

export type Choice = { id: string; name: string; user?: User }

/**
 * A list to add one thing from, such as a user to a group or a parent to a user.
 * A tap on Add sends it. On success the admin goes back to `backTo`. A refusal
 * shows above the list, and the list stays so they can pick another.
 */
export function Picker({
  title,
  verb = 'Add',
  empty,
  choices,
  onAdd,
  backTo,
}: {
  title: string
  /** What the button says, and the start of its name for a screen reader: "Add" or "Invite". */
  verb?: string
  empty: string
  choices: Choice[]
  /** Null on success, or what to say instead. */
  onAdd: (c: Choice) => Promise<ReactNode | null>
  /** Where the admin goes after an add. */
  backTo: string
}) {
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<ReactNode | null>(null)

  async function add(c: Choice) {
    setBusy(true)
    setRefused(null)
    try {
      const said = await onAdd(c)
      if (said === null) {
        goBack(backTo)
        return
      }
      setRefused(said)
    } catch {
      setRefused('Something went wrong. Try again.')
    }
    setBusy(false)
  }

  return (
    <Screen title={title}>
      <div className="flex max-w-2xl flex-col gap-4">
        {refused && (
          <p role="alert" className={notice}>
            {refused}
          </p>
        )}
        {choices.length === 0 ? (
          <p className="text-lg">{empty}</p>
        ) : (
          <ul>
            {choices.map((c) => (
              <li key={c.id} className={row}>
                <span className="flex min-w-0 items-center gap-3">
                  {c.user && <UserAvatar user={c.user} size={40} />}
                  <span className="text-xl/tight font-semibold wrap-break-word">{c.name}</span>
                </span>
                <button type="button" className={`${button} shrink-0`} disabled={busy} aria-label={`${verb} ${c.name}`} onClick={() => add(c)}>
                  {verb}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Screen>
  )
}
