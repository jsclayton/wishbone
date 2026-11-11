import { useState } from 'react'
import { makeLoginLink, revokeSession, type Collection, type LoginLinkMade, type Passkey, type Session, type User } from './api'
import { deviceName } from './deviceName'
import { LinkPanel } from './LinkPanel'
import { Passkeys } from './Passkeys'
import { Resource } from './Resource'
import { Screen } from './Screen'
import { button } from './ui'

const heading = 'text-2xl font-extrabold'

function lastUsed(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

/**
 * A user's sign-in screen, for the user and for anyone who acts for them
 * (docs/spec.md, Users and parents, Screens). What it shows follows the
 * user's `can_*` fields: Make a login link, the sessions with Revoke, and the
 * passkeys.
 */
export function SignIn({ userId, me, onLoggedOut }: { userId: string; me: User; onLoggedOut: () => void }) {
  return (
    <Resource<User> path={`/api/v2/users/${userId}`}>
      {(user) => (
        <Screen title={user.id === me.id ? 'Your devices' : `Sign ${user.name} in`}>
          {user.can_make_login_link && <MakeLoginLink user={user} isMe={user.id === me.id} />}
          {readsSignIn(user) && (
            <Resource<Collection<Session>> path={`/api/v2/users/${user.id}/sessions`} all>
              {(sessions) => <Sessions userId={user.id} initial={sessions.data} onLoggedOut={onLoggedOut} />}
            </Resource>
          )}
          {/* The passkeys have the same readers as the sessions: the user, a parent, and the admin. */}
          {readsSignIn(user) && (
            <Resource<Collection<Passkey>> path={`/api/v2/users/${user.id}/passkeys`} all>
              {(passkeys) => <Passkeys user={user} isMe={user.id === me.id} initial={passkeys.data} />}
            </Resource>
          )}
        </Screen>
      )}
    </Resource>
  )
}

/**
 * Whether the caller reads the user's sessions and passkeys: the user, their
 * parents, and the admin. For an active user that is `can_manage_sessions`. A
 * deactivated user's `can_*` fields are all false, but the same callers still
 * read, and they are the callers who get `deactivated_at` (it is `null` for an
 * active user and absent for everyone else).
 */
function readsSignIn(user: User): boolean {
  return user.can_manage_sessions || user.deactivated_at !== undefined
}

function MakeLoginLink({ user, isMe }: { user: User; isMe: boolean }) {
  const [made, setMade] = useState<LoginLinkMade | null>(null)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)

  async function make() {
    setBusy(true)
    setFailed(false)
    try {
      setMade(await makeLoginLink(user.id))
    } catch {
      setFailed(true)
    }
    setBusy(false)
  }

  return (
    <section className="flex flex-col gap-4">
      {made && (
        <LinkPanel
          key={made.url}
          label={isMe ? 'Login link for your other device' : `Login link for ${user.name}`}
          qrAlt="QR code for the login link"
          link={made}
          prompt={isMe ? 'Scan this with your other device.' : `Scan this with ${user.name}'s device.`}
          copyLabel="Copy the link instead"
        />
      )}
      <button type="button" className={button} onClick={make} disabled={busy}>
        {made ? 'Make another login link' : 'Make a login link'}
      </button>
      {failed && <p role="alert">Could not make a login link. Try again.</p>}
    </section>
  )
}

function Sessions({ userId, initial, onLoggedOut }: { userId: string; initial: Session[]; onLoggedOut: () => void }) {
  const [sessions, setSessions] = useState(initial)
  const [busy, setBusy] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)

  async function revoke(s: Session) {
    setBusy(s.id)
    setFailed(false)
    try {
      await revokeSession(userId, s.id)
    } catch {
      setFailed(true)
      setBusy(null)
      return
    }
    // The server cleared the cookie of this browser's own session.
    if (s.is_current) {
      onLoggedOut()
      return
    }
    setSessions((all) => all.filter((x) => x.id !== s.id))
    setBusy(null)
  }

  return (
    <section className="flex flex-col gap-1">
      <h2 className={heading}>Signed in</h2>
      {sessions.length === 0 ? (
        <p className="border-y border-hair py-3.5 text-mute">Not signed in on any device</p>
      ) : (
        <ul className="flex flex-col border-b border-hair">
          {sessions.map((s) => (
            <li key={s.id} className="grid min-h-17 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 border-t border-hair py-2">
              <span className="flex flex-col">
                <strong className="font-extrabold">{deviceName(s.user_agent)}</strong>
                {s.is_current ? (
                  <span className="text-mute">This device, in use now</span>
                ) : (
                  <span className="text-mute">
                    Last used <time dateTime={s.last_used_at}>{lastUsed(s.last_used_at)}</time>
                  </span>
                )}
              </span>
              {s.can_delete && (
                <button type="button" className={button} disabled={busy === s.id} onClick={() => revoke(s)}>
                  Revoke
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {failed && <p role="alert">Could not revoke that session. Try again.</p>}
    </section>
  )
}
