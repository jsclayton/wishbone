import { useEffect, useState } from 'react'
import { checkLoginLink, getMe, RateLimitedError, redeemLoginLink } from './api'
import { bandAction, BrandHeading, focusMain, SignInFrame, SignInMessage, TooManyTries, useSignInStep } from './SignInFrame'
import { link } from './ui'

/** What the page knows about its link. A first look decides which page it is. */
type Look =
  | { page: 'sign-in'; expiresAt: string }
  | { page: 'switch'; current: string }
  | { page: 'problem' }
  | { page: 'too-many' }
  | { page: 'unreachable' }

/**
 * A failed check is not a bad link, and with no answer on who is signed in the
 * page can't offer a button that might skip the warning.
 */
async function lookAt(token: string): Promise<Look> {
  try {
    const check = await checkLoginLink(token)
    if (!check) return { page: 'problem' }
    if (check.for_current_user) return { page: 'sign-in', expiresAt: check.expires_at }
    const me = await getMe()
    return me ? { page: 'switch', current: me.name } : { page: 'sign-in', expiresAt: check.expires_at }
  } catch (e) {
    return { page: e instanceof RateLimitedError ? 'too-many' : 'unreachable' }
  }
}

/** "Sat Nov 7, 3:40 PM" in the browser's own zone. */
function formatExpiry(iso: string) {
  const at = new Date(iso)
  const day = at.toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric' }).replace(',', '')
  return `${day}, ${at.toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' })}`
}

function LinkProblem() {
  return (
    <SignInMessage heading="This link doesn't work" body="It may have expired or already been used. Ask the person who sent it for a new one.">
      <a href="/" className={bandAction}>
        Go to Wishbone
      </a>
    </SignInMessage>
  )
}

/**
 * Opening a login link doesn't use it up: messaging apps load links to build
 * previews. Only the tap redeems it. Every link gets the same page when it
 * doesn't work, and nothing here names the link's user.
 */
export function LoginLink({ token, onDone }: { token: string; onDone: (banner: string | null) => void }) {
  const [step, showStep] = useSignInStep()
  const [look, setLook] = useState<Look | null>(null)
  const [retrying, setRetrying] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Coming back from a step looks again: the link may have stopped working.
  useEffect(() => {
    if (step) return
    let live = true
    void lookAt(token).then((next) => live && setLook(next))
    return () => {
      live = false
    }
  }, [token, step])

  async function lookAgain() {
    setRetrying(true)
    setLook(await lookAt(token))
    setRetrying(false)
    focusMain()
  }

  async function redeem() {
    setBusy(true)
    setError(null)
    try {
      const res = await redeemLoginLink(token)
      if (res) {
        onDone(res.switched_user ? `You're now signed in as ${res.name}.` : null)
        return
      }
      showStep('link-problem')
    } catch (e) {
      if (e instanceof RateLimitedError) showStep('too-many')
      else setError('Something went wrong. Reload and try again.')
    }
    setBusy(false)
  }

  /**
   * Someone may have signed in on this browser since the page opened, and
   * redeeming revokes whatever session the cookie holds. Only the warning page
   * skips this look, because the person has just agreed.
   */
  async function signIn() {
    setBusy(true)
    setError(null)
    const next = await lookAt(token)
    if (next.page === 'sign-in') return redeem()
    setLook(next)
    setBusy(false)
    focusMain()
  }

  const failure = error && <p role="alert">{error}</p>

  if (step === 'too-many') return <TooManyTries onTryAgain={() => window.history.back()} />
  if (step === 'link-problem') return <LinkProblem />
  if (!look) return <SignInFrame />

  switch (look.page) {
    case 'problem':
      return <LinkProblem />
    case 'too-many':
      return <TooManyTries onTryAgain={lookAgain} />
    case 'unreachable':
      return (
        <SignInMessage heading="Can't reach Wishbone" body="Check your internet connection and try again. If that doesn't help, Wishbone may be down for a while.">
          <button type="button" className={bandAction} onClick={lookAgain} disabled={retrying}>
            Try again
          </button>
        </SignInMessage>
      )
    case 'switch':
      return (
        <SignInMessage
          heading={`Signed in as ${look.current}`}
          body={`This link is for a different user. If you use it, ${look.current} is signed out on this browser and needs a login link to get back in.`}
        >
          <button type="button" className={bandAction} onClick={redeem} disabled={busy}>
            Sign in with this link
          </button>
          <a href="/" className={`${link} justify-center font-semibold`}>
            Stay signed in as {look.current}
          </a>
          {failure}
        </SignInMessage>
      )
    case 'sign-in':
      return (
        <SignInFrame>
          <div className="flex flex-col gap-2">
            <BrandHeading />
            <p className="text-xl">Tap the button to sign in. This link works once.</p>
            <p className="font-mono text-[1.0625rem]">
              Expires <time dateTime={look.expiresAt}>{formatExpiry(look.expiresAt)}</time>
            </p>
          </div>
          <button type="button" className={bandAction} onClick={signIn} disabled={busy}>
            Sign in
          </button>
          {failure}
        </SignInFrame>
      )
  }
}
