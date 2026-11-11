import { useEffect, useRef, useState } from 'react'
import { getMe, getSetup, reportTimezone, type User } from './api'
import { FinishProfile } from './FinishProfile'
import { LoginLink } from './LoginLink'
import { isInAppClick, pushTo } from './navigation'
import { SignedOutContext } from './Resource'
import { findRoute } from './routes'
import { CantReach } from './Screen'
import { Setup } from './Setup'
import { SignedOut } from './SignedOut'
import { isUpdateWaiting } from './updates'

/**
 * Who is using the app. `undefined` until the server answers. A `User` when
 * signed in. `'setup'` and `'signed-out'` for a browser with no session, on an
 * instance that has no users or has some. `'unreachable'` when the server did
 * not answer: only a 401 means signed out, because a cached app shell with no
 * server would otherwise tell a signed-in user they had been logged out.
 */
type Session = User | 'setup' | 'signed-out' | 'unreachable' | undefined

async function loadSession(): Promise<Session> {
  const me = await getMe()
  if (me) return me
  return (await getSetup()) ? 'setup' : 'signed-out'
}

/**
 * Reports the browser's timezone when it is not the signed-in user's. It
 * doesn't wait and says nothing: the app does not depend on it. Only
 * the signed-in user's own timezone is set, never a child's: `session` is the
 * user `GET /users/me` returned.
 */
function reportZone(session: Session) {
  if (typeof session !== 'object') return
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone
  if (zone && zone !== session.timezone) void reportTimezone(zone)
}

export function App() {
  const [path, setPath] = useState(window.location.pathname)
  const main = useRef<HTMLElement>(null)
  const [session, setSession] = useState<Session>(undefined)
  // A one-time line on Home after a login link switched users.
  const [banner, setBanner] = useState<string | null>(null)
  // A login link works with no session, so its page comes before the route table.
  const loginToken = /^\/login\/([^/]+)$/.exec(path)?.[1]

  // Links stay real anchors, so a new tab, a long press, and a copied address
  // keep working. A plain tap moves to the screen here instead of loading the
  // page again. Back and Forward come through popstate.
  useEffect(() => {
    function moveTo(href: string) {
      pushTo(href)
      const url = new URL(href, window.location.origin)
      setBanner(null)
      setPath(url.pathname)
      // A page load put the user at the top and made a screen reader announce
      // the new page. A link with a hash points at a place on the screen,
      // which the screen scrolls to once its data is in.
      if (!url.hash) {
        main.current?.focus({ preventScroll: true })
        window.scrollTo(0, 0)
      }
    }
    function onClick(event: MouseEvent) {
      const anchor = event.target instanceof Element ? event.target.closest('a[href]') : null
      if (!(anchor instanceof HTMLAnchorElement)) return
      if (!isInAppClick(event, anchor, window.location.origin, isUpdateWaiting())) return
      event.preventDefault()
      const url = new URL(anchor.href)
      moveTo(url.pathname + url.search + url.hash)
    }
    function onPopState() {
      if (isUpdateWaiting()) {
        window.location.reload()
        return
      }
      setBanner(null)
      setPath(window.location.pathname)
    }
    document.addEventListener('click', onClick)
    window.addEventListener('popstate', onPopState)
    return () => {
      document.removeEventListener('click', onClick)
      window.removeEventListener('popstate', onPopState)
    }
  }, [])

  // The server answers who is using the app when the app opens, and again each
  // time `reload` clears the answer: after a sign-in or a retry. Moving between
  // screens asks nothing.
  useEffect(() => {
    if (session !== undefined || loginToken) return
    let live = true
    loadSession()
      .then((next) => {
        if (!live) return
        reportZone(next)
        setSession(next)
      })
      .catch(() => live && setSession('unreachable'))
    return () => {
      live = false
    }
  }, [session, loginToken])

  function reload() {
    setSession(undefined)
  }

  function onLoggedOut() {
    window.history.replaceState(null, '', '/')
    setPath('/')
    setSession('signed-out')
  }

  // The old user's Home must not draw before the server says who is signed in
  // now, so the session on hand is dropped.
  function onSignedInByLink(next: string | null) {
    window.history.replaceState(null, '', '/')
    setBanner(next)
    setPath('/')
    setSession(undefined)
  }

  function screen() {
    if (loginToken) return <LoginLink key={loginToken} token={loginToken} onDone={onSignedInByLink} />
    if (session === undefined) return null
    if (session === 'unreachable') return <CantReach onRetry={reload} retrying={false} />
    if (session === 'setup') return <Setup onDone={reload} />
    if (session === 'signed-out') return <SignedOut onSignedIn={reload} />
    // Until the user confirms their profile, the step comes before every screen.
    if (session.can_confirm_profile) return <FinishProfile me={session} onDone={reload} onLoggedOut={onLoggedOut} />
    // A path that matches no screen opens Home.
    const found = findRoute(path) ?? findRoute('/')
    return found?.route.screen({ me: session, banner, onLoggedOut, onMeChange: setSession, params: found.params })
  }

  return (
    <main ref={main} tabIndex={-1} className="flex min-h-dvh flex-col text-base wrap-anywhere outline-none">
      <SignedOutContext value={reload}>{screen()}</SignedOutContext>
    </main>
  )
}
