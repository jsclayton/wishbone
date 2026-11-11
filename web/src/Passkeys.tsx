import { useState, type FormEvent } from 'react'
import { deletePasskey, renamePasskey, type Passkey, type User } from './api'
import { deviceName } from './deviceName'
import { addPasskey, PasskeyError } from './passkeyBrowser'
import { button, buttonLink, field } from './ui'

const heading = 'text-2xl font-extrabold'

function day(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
}

/** The path of the screen that asks before the user's last passkey is deleted. */
export function deletePasskeyPath(userId: string, passkeyId: string): string {
  return `/users/${userId}/passkeys/${passkeyId}/delete`
}

/**
 * The passkeys part of a user's sign-in screen (docs/spec.md, Users and
 * parents, Screens). Each button shows from the passkey's `can_*` field: Rename
 * from `can_edit` and Delete from `can_delete`. Add a passkey shows only on the
 * signed-in user's own screen.
 */
export function Passkeys({ user, isMe, initial }: { user: User; isMe: boolean; initial: Passkey[] }) {
  const [passkeys, setPasskeys] = useState(initial)
  // Remounts the add button after each add, so it resets.
  const [added, setAdded] = useState(0)

  return (
    <section className="flex flex-col gap-1">
      <h2 className={heading}>Passkeys</h2>
      {isMe && <p className="mb-2">A passkey lets you sign in from a signed-out browser without a login link.</p>}
      {passkeys.length === 0 ? (
        <p className="border-y border-hair py-3.5 text-mute">No passkeys yet.</p>
      ) : (
        <ul className="flex flex-col border-b border-hair">
          {passkeys.map((p) => (
            <PasskeyRow
              key={p.id}
              userId={user.id}
              passkey={p}
              isLast={passkeys.length === 1}
              onRenamed={(renamed) => setPasskeys((all) => all.map((x) => (x.id === renamed.id ? renamed : x)))}
              onDeleted={() => setPasskeys((all) => all.filter((x) => x.id !== p.id))}
            />
          ))}
        </ul>
      )}
      {isMe && (
        <div className="mt-3 flex flex-col gap-2">
          <AddPasskey
            key={added}
            userId={user.id}
            onAdded={(p) => {
              setPasskeys((all) => [...all, p])
              setAdded((n) => n + 1)
            }}
          />
        </div>
      )}
    </section>
  )
}

/** The Add a passkey button. The passkey is named after this browser, and the user can rename it. */
function AddPasskey({ userId, onAdded }: { userId: string; onAdded: (p: Passkey) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function add() {
    setBusy(true)
    setError(null)
    try {
      onAdded(await addPasskey(userId, deviceName(navigator.userAgent)))
    } catch (e) {
      setError(e instanceof PasskeyError ? e.message : 'Something went wrong. Try again.')
      setBusy(false)
    }
  }

  return (
    <>
      <button type="button" className={button} disabled={busy} onClick={add}>
        Add a passkey
      </button>
      {error && <p role="alert">{error}</p>}
    </>
  )
}

function PasskeyRow({
  userId,
  passkey,
  isLast,
  onRenamed,
  onDeleted,
}: {
  userId: string
  passkey: Passkey
  isLast: boolean
  onRenamed: (p: Passkey) => void
  onDeleted: () => void
}) {
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(passkey.name)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function save(e: FormEvent) {
    e.preventDefault()
    const trimmed = name.trim()
    if (!trimmed) {
      setError('Enter a name.')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await renamePasskey(userId, passkey.id, trimmed)
      if (res.ok) {
        onRenamed(res.value)
        setEditing(false)
      } else {
        setError(res.detail)
      }
    } catch {
      setError('Could not rename that passkey. Try again.')
    }
    setBusy(false)
  }

  async function remove() {
    setBusy(true)
    setError(null)
    try {
      await deletePasskey(userId, passkey.id)
      onDeleted()
    } catch {
      setError('Could not delete that passkey. Try again.')
      setBusy(false)
    }
  }

  if (editing) {
    return (
      <li className="border-t border-hair py-3">
        <form onSubmit={save} className="flex flex-col gap-2" noValidate>
          <label className="flex flex-col gap-1">
            Name
            <input className={field} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
          </label>
          <button type="submit" className={button} disabled={busy}>
            Save
          </button>
          <button
            type="button"
            className={button}
            disabled={busy}
            onClick={() => {
              setName(passkey.name)
              setError(null)
              setEditing(false)
            }}
          >
            Cancel
          </button>
          {error && <p role="alert">{error}</p>}
        </form>
      </li>
    )
  }

  return (
    <li className="flex flex-col gap-2 border-t border-hair py-3">
      <span className="flex flex-col">
        <strong className="font-extrabold">{passkey.name}</strong>
        <span className="text-mute">Added {day(passkey.created_at)}</span>
        <span className="text-mute">
          {passkey.last_used_at ? (
            <>
              Last used <time dateTime={passkey.last_used_at}>{day(passkey.last_used_at)}</time>
            </>
          ) : (
            'Never used'
          )}
        </span>
      </span>
      <div className="flex flex-wrap gap-2">
        {passkey.can_edit && (
          <button type="button" className={button} aria-label={`Rename ${passkey.name}`} disabled={busy} onClick={() => setEditing(true)}>
            Rename
          </button>
        )}
        {passkey.can_delete &&
          (isLast ? (
            <a href={deletePasskeyPath(userId, passkey.id)} className={buttonLink} aria-label={`Delete ${passkey.name}`}>
              Delete
            </a>
          ) : (
            <button type="button" className={button} aria-label={`Delete ${passkey.name}`} disabled={busy} onClick={remove}>
              Delete
            </button>
          ))}
      </div>
      {error && <p role="alert">{error}</p>}
    </li>
  )
}
