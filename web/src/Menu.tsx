import { useEffect, useState } from 'react'
import { fullName, getVersion, logOut, type User } from './api'
import { UserAvatar } from './Avatar'
import { Screen } from './Screen'
import { sourceUrl } from './source'
import { link } from './ui'

const row = 'flex min-h-15.5 items-center border-t border-hair text-xl font-semibold'

/** Every screen a user can open, and Log out. A change that adds a screen adds a row. */
export function Menu({ me, onLoggedOut }: { me: User; onLoggedOut: () => void }) {
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  // The server's version, once it says. Without it the link shows on its own.
  const [version, setVersion] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    getVersion().then(
      (v) => live && setVersion(v),
      () => {},
    )
    return () => {
      live = false
    }
  }, [])

  async function run() {
    setBusy(true)
    setFailed(false)
    try {
      await logOut()
      onLoggedOut()
    } catch {
      setFailed(true)
      setBusy(false)
    }
  }

  return (
    <Screen title="Menu" menu={false}>
      <p className="flex items-center gap-3">
        <UserAvatar user={me} size={52} />
        <strong className="text-xl font-extrabold">{fullName(me)}</strong>
      </p>
      <nav aria-label="Your screens" className="flex flex-col">
        <h2 className="mb-1 font-extrabold">Mine</h2>
        <div className="flex flex-col border-b border-hair">
          <a href="/" className={row}>
            Home
          </a>
          <a href="/lists" className={row}>
            My lists
          </a>
          <a href="/giving" className={row}>
            Giving
          </a>
          <a href="/received" className={row}>
            Received
          </a>
          <a href="/given" className={row}>
            Given
          </a>
          <a href={`/users/${me.id}/profile`} className={row}>
            Your profile
          </a>
          <a href={`/users/${me.id}/sign-in`} className={row}>
            Your devices
          </a>
          <a href="/groups" className={row}>
            Groups
          </a>
        </div>
        {me.is_admin && (
          <>
            <h2 className="mt-5 mb-1 font-extrabold">For the admin</h2>
            <div className="flex flex-col border-b border-hair">
              <a href="/users" className={row}>
                Users
              </a>
              <a href="/occasions" className={row}>
                Shared occasions
              </a>
              <a href="/blocked-stores" className={row}>
                Blocked stores
              </a>
            </div>
          </>
        )}
      </nav>
      <div className="mt-auto flex flex-col items-center pt-4">
        {failed && <p role="alert">Something went wrong. Try again.</p>}
        <button type="button" className={`${link} justify-center font-semibold`} onClick={run} disabled={busy}>
          Log out
        </button>
        <a href={sourceUrl} target="_blank" rel="noopener noreferrer" className={`${link} justify-center`}>
          Source code
        </a>
        {version && <p className="text-mute">Version {version}</p>}
      </div>
    </Screen>
  )
}
